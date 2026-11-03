package access_test

import (
	"bytes"
	"crypto/rand"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// invitationGrants are the use cases that issue, accept and drop the grants
// of invitations, on a fresh database with a project of their own.
type invitationGrants struct {
	pool       *pgxpool.Pool
	store      *pgaccess.Store
	project    workspace.ProjectID
	invite     *appaccess.InviteToProject
	accept     *appaccess.AcceptInvitation
	deactivate *appaccess.DeactivateMember
	update     *appaccess.UpdateMember
	deleteG    *appaccess.DeleteGrant
	reissue    *appaccess.ReissueInvitation
}

func newInvitationGrants(t *testing.T) invitationGrants {
	t.Helper()
	pool, c := testkit.FreshPool(t), clock.System{}
	ids := id.NewSource(c)
	project, err := workspace.NewProject(id.New[workspace.Project](ids), "p"+strings.ToLower(rand.Text()[:12]), "Invitations", "", c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = pgworkspace.NewStore(pool).CreateProject(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	tx, store, audit := postgres.NewTransactor(pool), pgaccess.NewStore(pool), testkit.Record(pool)
	return invitationGrants{
		pool: pool, store: store, project: project.ID,
		invite: appaccess.NewInviteToProject(tx, store, audit, c, ids, rand.Reader),
		accept: appaccess.NewAcceptInvitation(appaccess.AcceptInvitationDeps{
			Tx: tx, Store: store, Settings: store, Hasher: password.NewHasher(cheap), Audit: audit,
			Clock: c, IDs: ids, Random: rand.Reader, Key: bytes.Repeat([]byte{1}, 32),
		}),
		deactivate: appaccess.NewDeactivateMember(tx, store, audit, c, ids),
		update:     appaccess.NewUpdateMember(tx, store, audit, c, ids),
		deleteG:    appaccess.NewDeleteGrant(tx, store, audit, c, ids),
		reissue:    appaccess.NewReissueInvitation(tx, store, audit, c, ids, rand.Reader),
	}
}

// owner returns a new active member holding the owner role in the project,
// with the session they invite through and the grant they hold.
func (g invitationGrants) owner(t *testing.T, opts ...testkit.MemberOption) (access.Caller, access.GrantID) {
	t.Helper()
	m := testkit.NewMember(t, g.pool, opts...)
	testkit.Grant(t, g.pool, g.project, m.ID, access.RoleOwner)
	member, err := g.store.MemberByID(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	held, err := g.store.MemberGrants(t.Context(), m.ID, g.project)
	if err != nil {
		t.Fatal(err)
	}
	return access.SessionCaller(access.Session{ID: id.New[access.Session](id.NewSource(clock.System{}))}, member), held[0].ID
}

func (g invitationGrants) inviteBy(t *testing.T, caller access.Caller) (appaccess.InvitedMember, error) {
	t.Helper()
	res, err := g.invite.Handle(t.Context(), appaccess.InviteToProjectCommand{
		Caller: caller, Project: appaccess.ProjectRef{ID: g.project, Held: ownerHolds()},
		Email: strings.ToLower(rand.Text()) + "@example.com", DisplayName: "Invited", Role: access.RoleViewer,
		Request: governance.Request{Actor: cli},
	})
	if res.Invitation != nil {
		testkit.RecordSecret(res.Invitation.Secret.Reveal())
	}
	return res, err
}

func (g invitationGrants) acceptAs(t *testing.T, invited appaccess.InvitedMember) error {
	t.Helper()
	_, err := g.accept.Handle(t.Context(), appaccess.AcceptInvitationCommand{
		Secret: invited.Invitation.Secret, Password: secret.New("harbour-lantern-quiet-4410"), ClientIP: anAddress(),
	})
	return err
}

// An invitation accepted while its inviter is deactivated or demoted either
// grants first or finds its grant dropped; neither change waits on the other
// in a deadlock.
func TestAcceptingAnInvitationWhileItsInviterLosesTheRightToGrantEndsCleanly(t *testing.T) {
	g := newInvitationGrants(t)
	testkit.NewMember(t, g.pool, testkit.AsInstanceAdmin())
	req := governance.Request{Actor: cli}
	for round := range 60 {
		demoting := round%2 == 1
		var opts []testkit.MemberOption
		if demoting {
			opts = append(opts, testkit.AsInstanceAdmin())
		}
		inviter, grant := g.owner(t, opts...)
		if demoting {
			// An instance admin holding no grant in the project loses
			// grant.manage there once demoted.
			if err := g.deleteG.Handle(t.Context(), appaccess.DeleteGrantCommand{
				Project: appaccess.ProjectRef{ID: g.project, Held: ownerHolds()}, Grant: grant, Request: req,
			}); err != nil {
				t.Fatal(err)
			}
		}
		invited, err := g.inviteBy(t, inviter)
		if err != nil {
			t.Fatal(err)
		}
		var acceptErr, changeErr error
		var wg sync.WaitGroup
		wg.Go(func() { acceptErr = g.acceptAs(t, invited) })
		wg.Go(func() {
			if demoting {
				_, changeErr = g.update.Handle(t.Context(), appaccess.UpdateMemberCommand{
					Member: inviter.Member.ID, Change: access.MemberChange{InstanceAdmin: new(false)}, Request: req,
				})
				return
			}
			_, changeErr = g.deactivate.Handle(t.Context(), inviter.Member.ID, req)
		})
		wg.Wait()
		if acceptErr != nil {
			t.Fatalf("round %d: accepting = %v", round, acceptErr)
		}
		if changeErr != nil {
			t.Fatalf("round %d: the inviter's change = %v", round, changeErr)
		}
	}
}

// An invitation issued while its inviter loses grant.manage in the project
// never grants once they have lost it, whichever change commits first.
func TestAnInvitationIssuedWhileItsInviterLosesTheRightToGrantNeverGrants(t *testing.T) {
	g := newInvitationGrants(t)
	req := governance.Request{Actor: cli}
	for round := range 60 {
		inviter, grant := g.owner(t)
		var invited appaccess.InvitedMember
		var inviteErr, changeErr error
		var wg sync.WaitGroup
		wg.Go(func() { invited, inviteErr = g.inviteBy(t, inviter) })
		wg.Go(func() {
			if round%2 == 0 {
				_, changeErr = g.deactivate.Handle(t.Context(), inviter.Member.ID, req)
				return
			}
			changeErr = g.deleteG.Handle(t.Context(), appaccess.DeleteGrantCommand{
				Project: appaccess.ProjectRef{ID: g.project, Held: ownerHolds()}, Grant: grant, Request: req,
			})
		})
		wg.Wait()
		if inviteErr != nil || changeErr != nil {
			t.Fatalf("round %d: inviting = %v, the inviter's change = %v", round, inviteErr, changeErr)
		}
		if err := g.acceptAs(t, invited); err != nil {
			t.Fatalf("round %d: accepting = %v", round, err)
		}
		held, err := g.store.MemberGrants(t.Context(), invited.Member.ID, g.project)
		if err != nil {
			t.Fatal(err)
		}
		if len(held) != 0 {
			t.Fatalf("round %d: the invited member holds %v in the project, want nothing", round, held)
		}
	}
}

// A reissue carries the grant along only while the member who invited may
// still hand it out, whatever the way they lost the right, and an invited
// member with no invitation of record is issued one granting nothing.
func TestAReissueCarriesTheGrantOnlyWhileItsInviterMayHandItOut(t *testing.T) {
	g := newInvitationGrants(t)
	req := governance.Request{Actor: cli}
	granted := func(t *testing.T, invited appaccess.InvitedMember) []access.Grant {
		t.Helper()
		if err := g.acceptAs(t, invited); err != nil {
			t.Fatal(err)
		}
		held, err := g.store.MemberGrants(t.Context(), invited.Member.ID, g.project)
		if err != nil {
			t.Fatal(err)
		}
		return held
	}

	t.Run("the inviter still holds it", func(t *testing.T) {
		inviter, _ := g.owner(t)
		invited, err := g.inviteBy(t, inviter)
		if err != nil {
			t.Fatal(err)
		}
		again, err := g.reissue.Handle(t.Context(), invited.Member.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		testkit.RecordSecret(again.Invitation.Secret.Reveal())
		if held := granted(t, again); len(held) != 1 || held[0].CreatedBy != inviter.Member.ID {
			t.Errorf("grants = %v, want the one made by the inviter", held)
		}
	})

	t.Run("the inviter lost it without the invitation being told", func(t *testing.T) {
		inviter, grant := g.owner(t)
		invited, err := g.inviteBy(t, inviter)
		if err != nil {
			t.Fatal(err)
		}
		// The grant goes behind the use cases' back, so no drop was recorded.
		if _, err = g.store.DeleteGrant(t.Context(), g.project, grant); err != nil {
			t.Fatal(err)
		}
		again, err := g.reissue.Handle(t.Context(), invited.Member.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		testkit.RecordSecret(again.Invitation.Secret.Reveal())
		if again.Invitation.Grant != nil || again.Invitation.InvitedBy != inviter.Member.ID {
			t.Errorf("reissued invitation = %+v, want the same inviter and no grant", again.Invitation.Invitation)
		}
		if held := granted(t, again); len(held) != 0 {
			t.Errorf("grants = %v, want none", held)
		}
	})

	t.Run("no invitation of record", func(t *testing.T) {
		m := testkit.NewMember(t, g.pool, testkit.WithStatus(access.MemberInvited))
		again, err := g.reissue.Handle(t.Context(), m.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		testkit.RecordSecret(again.Invitation.Secret.Reveal())
		if again.Invitation.Grant != nil || !again.Invitation.InvitedBy.IsZero() {
			t.Errorf("reissued invitation = %+v, want neither grant nor inviter", again.Invitation.Invitation)
		}
	})
}
