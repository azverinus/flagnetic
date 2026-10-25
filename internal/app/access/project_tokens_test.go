package access_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// projectTokens are the project token use cases on the package's database,
// with a project of their own and the session of a member creating tokens
// in it.
type projectTokens struct {
	create  *appaccess.CreateProjectToken
	revoke  *appaccess.RevokeProjectToken
	list    *appaccess.ListProjectTokens
	project workspace.ProjectID
	session access.Caller
}

func newProjectTokens(t *testing.T) projectTokens {
	t.Helper()
	pool, c := testkit.Pool(t), testkit.Clock()
	ids := id.NewSource(c)
	project, err := workspace.NewProject(id.New[workspace.Project](ids), "p"+strings.ToLower(rand.Text()[:12]), "Tokens", "", c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = pgworkspace.NewStore(pool).CreateProject(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	m := testkit.NewMember(t, pool)
	member, err := pgaccess.NewStore(pool).MemberByID(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, store, audit := postgres.NewTransactor(pool), pgaccess.NewStore(pool), testkit.Record(pool)
	return projectTokens{
		create:  appaccess.NewCreateProjectToken(tx, store, pgaccess.NewAuthSettingsCache(pool), audit, c, ids, rand.Reader),
		revoke:  appaccess.NewRevokeProjectToken(tx, store, audit, c, ids),
		list:    appaccess.NewListProjectTokens(store),
		project: project.ID,
		session: access.SessionCaller(access.Session{ID: id.New[access.Session](ids)}, member),
	}
}

// ownerHolds is what an owner holds in a project.
func ownerHolds() []access.Permission {
	owner, _ := access.RoleNamed(access.RoleOwner)
	return owner.Permissions
}

func (p projectTokens) command(caller access.Caller, held []access.Permission, role access.RoleName) appaccess.CreateProjectTokenCommand {
	return appaccess.CreateProjectTokenCommand{
		Caller: caller, Project: appaccess.ProjectRef{ID: p.project, Held: held}, Name: "ci", Role: role,
		Request: governance.Request{Actor: appaccess.ActorOf(caller)},
	}
}

// wantNoToken fails if the project holds a token.
func (p projectTokens) wantNoToken(t *testing.T, why string) {
	t.Helper()
	tokens, err := p.list.Handle(t.Context(), p.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Errorf("%s: the project holds %d tokens, want none", why, len(tokens))
	}
}

// The seam refuses every bearer before the use case runs; the use case
// refuses one too, so no other entry point can let a token mint another.
func TestOnlyASessionCreatesAProjectToken(t *testing.T) {
	p := newProjectTokens(t)
	bearer := access.TokenCaller(access.APIToken{ID: id.New[access.APIToken](id.NewSource(testkit.Clock())), Kind: access.TokenPersonal,
		Scopes: access.AllScopes()}, p.session.Member)
	_, err := p.create.Handle(t.Context(), p.command(bearer, ownerHolds(), access.RoleViewer))
	if e, ok := apperrors.From(err); !ok || e.Code != "session_required" {
		t.Errorf("a token creating = %v, want session_required", err)
	}
	p.wantNoToken(t, "refused")
}

// With the built-in roles only an owner reaches the operation, so only the
// use case shows a caller holding less than the role they hand out.
func TestAProjectTokenOfARoleBeyondTheCallersReachIsRefused(t *testing.T) {
	p := newProjectTokens(t)
	maintainer, _ := access.RoleNamed(access.RoleMaintainer)
	held := append(maintainer.Permissions, access.PermProjectTokenManage)
	_, err := p.create.Handle(t.Context(), p.command(p.session, held, access.RoleOwner))
	if e, ok := apperrors.From(err); !ok || e.Code != "role_not_allowed" || e.Extensions["permission"] != "project.update" {
		t.Errorf("creating an owner's token = %v, want role_not_allowed naming project.update", err)
	}
	p.wantNoToken(t, "refused")
	if _, err := p.create.Handle(t.Context(), p.command(p.session, held, access.RoleMaintainer)); err != nil {
		t.Errorf("creating a maintainer's token: %v", err)
	}
}

func TestAProjectTokenWithoutAnActorLeavesNothing(t *testing.T) {
	p := newProjectTokens(t)
	cmd := p.command(p.session, ownerHolds(), access.RoleViewer)
	cmd.Request = governance.Request{}
	_, err := p.create.Handle(t.Context(), cmd)
	if err == nil {
		t.Fatal("creating without an actor succeeded")
	}
	if e, ok := apperrors.From(err); ok {
		t.Errorf("err = %v of kind %v, want a failure of no kind: a missing actor is a programming error", e, e.Kind)
	}
	p.wantNoToken(t, "without an actor")
}

// A project that disappears between the request's lookup and the creation
// takes nothing with it: the token is not stored, nor is its audit entry.
func TestAProjectTokenOfAProjectThatIsGoneIsNotCreated(t *testing.T) {
	p := newProjectTokens(t)
	gone := p
	gone.project = id.New[workspace.Project](id.NewSource(testkit.Clock()))
	if _, err := gone.create.Handle(t.Context(), gone.command(p.session, ownerHolds(), access.RoleViewer)); err == nil {
		t.Fatal("a token of a project that does not exist was created")
	}
	gone.wantNoToken(t, "a project gone")
}

// A request abandoned before the use case reads what it needs changes
// nothing, whichever step it reaches.
func TestAnAbandonedRequestCreatesAndRevokesNothing(t *testing.T) {
	p := newProjectTokens(t)
	created, err := p.create.Handle(t.Context(), p.command(p.session, ownerHolds(), access.RoleViewer))
	if err != nil {
		t.Fatal(err)
	}
	testkit.RecordSecret(created.Value.Reveal())
	abandoned, cancel := context.WithCancel(t.Context())
	cancel()

	if _, createErr := p.create.Handle(abandoned, p.command(p.session, ownerHolds(), access.RoleViewer)); createErr == nil {
		t.Error("an abandoned creation succeeded")
	}
	if revokeErr := p.revoke.Handle(abandoned, appaccess.RevokeProjectTokenCommand{
		Project: appaccess.ProjectRef{ID: p.project}, Token: created.Token.ID, Request: governance.Request{Actor: appaccess.ActorOf(p.session)},
	}); revokeErr == nil {
		t.Error("an abandoned revocation succeeded")
	}
	tokens, err := p.list.Handle(t.Context(), p.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].RevokedAt != nil {
		t.Errorf("tokens = %+v, want the one created, unrevoked", tokens)
	}
}
