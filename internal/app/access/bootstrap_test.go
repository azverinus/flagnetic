package access_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/compose"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

var cli = governance.Actor{Kind: governance.ActorCLI, ID: "cli", Name: "flagnetic admin bootstrap"}

func bootstrap(t *testing.T, pool *pgxpool.Pool, email string) (appaccess.BootstrapResult, error) {
	t.Helper()
	c := testkit.Clock()
	ctx := governance.WithRequest(t.Context(), governance.Request{Actor: cli})
	return compose.Bootstrap(compose.Deps{Pool: pool, Clock: c, IDs: testkit.IDs(c)}).Handle(ctx,
		appaccess.BootstrapCommand{Email: email, Password: secret.New("a long enough password")})
}

func TestTheBootstrapTokenCarriesEveryScope(t *testing.T) {
	pool := testkit.FreshPool(t)
	res, err := bootstrap(t, pool, "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := access.ParseTokenValue(res.Token.Reveal())
	if err != nil {
		t.Fatalf("the token %q is not well formed: %v", res.Token.Reveal(), err)
	}
	tok, holder, err := pgaccess.NewStore(pool).TokenByHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "bootstrap" || !slices.Equal(tok.Scopes, access.AllScopes()) || tok.ExpiresAt != nil {
		t.Errorf("token = %+v, want bootstrap with every scope and no expiry", tok)
	}
	if holder == nil || holder.ID != res.Member.ID || !holder.InstanceAdmin || holder.Status != access.MemberActive ||
		holder.DisplayName != "first" {
		t.Errorf("holder = %+v, want the active instance admin named after their email", holder)
	}
}

func TestBootstrapIsAuditedAsTheCommandLine(t *testing.T) {
	pool := testkit.FreshPool(t)
	res, err := bootstrap(t, pool, "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := pggovernance.NewAuditLog(pool, testkit.Clock(), testkit.IDs(testkit.Clock())).Latest(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	token, member := entries[0], entries[1]
	if member.Action != "member.created" || member.Entity.ID != res.Member.ID.String() || member.Entity.Key != "first@example.com" {
		t.Errorf("first entry = %+v, want member.created of the admin", member)
	}
	if token.Action != "api_token.created" || token.Entity.Kind != "api_token" || token.Entity.Key != "bootstrap" {
		t.Errorf("second entry = %+v, want api_token.created of the bootstrap token", token)
	}
	for _, e := range entries {
		if e.Actor != cli || !e.OccurredAt.Equal(testkit.Start) || !e.Project.IsZero() {
			t.Errorf("%s: actor %+v at %v in project %v, want the CLI at the clock's time on the instance", e.Action, e.Actor, e.OccurredAt, e.Project)
		}
	}
	after, _ := json.Marshal(token.After)
	if strings.Contains(string(after), res.Token.Reveal()) || !strings.Contains(string(after), res.Token.Reveal()[:11]) {
		t.Errorf("token entry after = %s, want the hint and never the value", after)
	}
}

func TestOnlyOneOfConcurrentBootstrapsCreatesAnInstanceAdmin(t *testing.T) {
	pool := testkit.FreshPool(t)
	const runs = 4
	errs := make([]error, runs)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() { _, errs[i] = bootstrap(t, pool, "admin"+string(rune('a'+i))+"@example.com") })
	}
	wg.Wait()
	created := 0
	for _, err := range errs {
		switch e, _ := apperrors.From(err); {
		case err == nil:
			created++
		case e == nil || e.Code != "instance_admin_exists":
			t.Errorf("err = %v, want instance_admin_exists", err)
		}
	}
	if created != 1 {
		t.Errorf("%d bootstraps created an instance admin, want 1", created)
	}
}

func TestABootstrapWithoutAnActorLeavesNothing(t *testing.T) {
	pool := testkit.FreshPool(t)
	c := testkit.Clock()
	_, err := compose.Bootstrap(compose.Deps{Pool: pool, Clock: c, IDs: id.NewSource(c)}).Handle(t.Context(),
		appaccess.BootstrapCommand{Email: "first@example.com", Password: secret.New("a long enough password")})
	if err == nil {
		t.Fatal("bootstrap without an actor succeeded")
	}
	if exists, err := pgaccess.NewStore(pool).AnyInstanceAdmin(t.Context()); err != nil || exists {
		t.Errorf("AnyInstanceAdmin = %v, %v; want the change rolled back", exists, err)
	}
}

func TestABootstrapNeedsAnEmail(t *testing.T) {
	_, err := bootstrap(t, testkit.FreshPool(t), "not an email")
	if !apperrors.IsKind(err, apperrors.KindInvalid) {
		t.Errorf("err = %v, want invalid", err)
	}
}
