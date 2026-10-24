package access_test

import (
	"bytes"
	"crypto/rand"
	"slices"
	"sync"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Through the API a demoted instance admin is refused before reaching the
// lock, so only the use cases themselves show two changes made at once.
func TestTwoInstanceAdminsDemotedAtOnceLeaveOne(t *testing.T) {
	pool := testkit.FreshPool(t)
	c := clock.System{}
	tx, store, audit := postgres.NewTransactor(pool), pgaccess.NewStore(pool), appgovernance.NewRecord(pggovernance.NewAuditLog(pool))
	update := appaccess.NewUpdateMember(tx, store, audit, c, id.NewSource(c))
	deactivate := appaccess.NewDeactivateMember(tx, store, audit, c, id.NewSource(c))
	req := governance.Request{Actor: cli}
	for round := range 10 {
		a := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
		b := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
		codes := make([]string, 2)
		var wg sync.WaitGroup
		wg.Go(func() {
			_, err := update.Handle(t.Context(), appaccess.UpdateMemberCommand{
				Member: a.ID, Change: access.MemberChange{InstanceAdmin: new(false)}, Request: req,
			})
			codes[0] = codeOf(err)
		})
		wg.Go(func() {
			_, err := deactivate.Handle(t.Context(), b.ID, req)
			codes[1] = codeOf(err)
		})
		wg.Wait()
		slices.Sort(codes)
		if !slices.Equal(codes, []string{"", "last_instance_admin"}) {
			t.Fatalf("round %d: refusals = %q, want one change and one last_instance_admin", round, codes)
		}
		// The admin left steps down before the next round, as nobody else could.
		for _, m := range []testkit.Member{a, b} {
			if err := store.SaveMember(t.Context(), access.Member{
				ID: m.ID, Email: m.Email, DisplayName: "Test Member", Status: access.MemberActive,
			}, c.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// cheap hashes passwords fast, so racing changes meet inside their units of
// work rather than one waiting out the other's hash.
var cheap = password.Params{MemoryKiB: 64, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}

// An invitation accepted while its member is deactivated or their link
// reissued either joins first or finds the link gone; neither change waits
// on the other in a deadlock.
func TestAcceptingAnInvitationWhileItIsReplacedOrItsMemberDeactivatedEndsCleanly(t *testing.T) {
	pool := testkit.Pool(t)
	c := clock.System{}
	tx, store, audit := postgres.NewTransactor(pool), pgaccess.NewStore(pool), appgovernance.NewRecord(pggovernance.NewAuditLog(pool))
	ids := id.NewSource(c)
	invite := appaccess.NewInviteMember(tx, store, audit, c, ids, rand.Reader)
	reissue := appaccess.NewReissueInvitation(tx, store, audit, c, ids, rand.Reader)
	deactivate := appaccess.NewDeactivateMember(tx, store, audit, c, ids)
	accept := appaccess.NewAcceptInvitation(appaccess.AcceptInvitationDeps{
		Tx: tx, Store: store, Settings: store, Hasher: password.NewHasher(cheap), Audit: audit,
		Clock: c, IDs: ids, Random: rand.Reader, Key: bytes.Repeat([]byte{1}, 32),
	})
	req := governance.Request{Actor: cli}
	for round := range 200 {
		invited, err := invite.Handle(t.Context(), appaccess.InviteMemberCommand{
			Email: rand.Text() + "@example.com", DisplayName: "Racing", Request: req,
		})
		if err != nil {
			t.Fatal(err)
		}
		testkit.RecordSecret(invited.Invitation.Secret.Reveal())
		member := invited.Member.ID
		var acceptErr, changeErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			_, acceptErr = accept.Handle(t.Context(), appaccess.AcceptInvitationCommand{
				Secret: invited.Invitation.Secret, Password: secret.New("harbour-lantern-quiet-4410"), ClientIP: anAddress(),
			})
		})
		wg.Go(func() {
			if round%2 == 0 {
				_, changeErr = deactivate.Handle(t.Context(), member, req)
				return
			}
			var res appaccess.InvitedMember
			res, changeErr = reissue.Handle(t.Context(), member, req)
			if res.Invitation != nil {
				testkit.RecordSecret(res.Invitation.Secret.Reveal())
			}
			if codeOf(changeErr) == "member_not_invited" {
				changeErr = nil // the member joined first
			}
		})
		wg.Wait()
		if acceptErr != nil && codeOf(acceptErr) != "invitation_gone" {
			t.Fatalf("round %d: accepting = %v, want success or invitation_gone", round, acceptErr)
		}
		if changeErr != nil {
			t.Fatalf("round %d: the concurrent change = %v", round, changeErr)
		}
	}
}
