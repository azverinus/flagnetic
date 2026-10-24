package access_test

import (
	"context"
	"crypto/rand"
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
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

var cli = governance.CLIActor("admin bootstrap")

// givenPassword is the password every bootstrap of these tests gives.
const givenPassword = "a long enough password"

func bootstrap(t *testing.T, pool *pgxpool.Pool, email string) (appaccess.BootstrapResult, error) {
	t.Helper()
	c := testkit.Clock()
	testkit.RecordSecret(givenPassword)
	res, err := compose.Bootstrap(compose.Deps{Pool: pool, Clock: c, IDs: testkit.IDs(c)}).Handle(t.Context(),
		appaccess.BootstrapCommand{Email: email, Password: secret.New(givenPassword), Request: governance.Request{Actor: cli}})
	testkit.RecordSecret(res.Token.Reveal())
	return res, err
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
	entries, err := pggovernance.NewAuditLog(pool).Latest(t.Context(), 10)
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
		if e.Actor != cli || !e.OccurredAt.Equal(testkit.Start) || !e.OccurredAt.Equal(res.Member.CreatedAt) || !e.Project.IsZero() {
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
	if e, ok := apperrors.From(err); ok {
		t.Errorf("err = %v of kind %v, want a failure of no kind: a missing actor is a programming error", e, e.Kind)
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

func TestABootstrapWithoutAPasswordGeneratesOne(t *testing.T) {
	c := testkit.Clock()
	res, err := compose.Bootstrap(compose.Deps{Pool: testkit.FreshPool(t), Clock: c, IDs: testkit.IDs(c)}).Handle(t.Context(),
		appaccess.BootstrapCommand{Email: "first@example.com", Request: governance.Request{Actor: cli}})
	if err != nil {
		t.Fatal(err)
	}
	testkit.RecordSecret(res.GeneratedPassword.Reveal())
	testkit.RecordSecret(res.Token.Reveal())
	if len(res.GeneratedPassword.Reveal()) != 24 {
		t.Errorf("generated password = %d characters, want 24", len(res.GeneratedPassword.Reveal()))
	}
	given, err := bootstrap(t, testkit.FreshPool(t), "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !given.GeneratedPassword.IsZero() {
		t.Error("a password was generated although the command gave one")
	}
}

// countingHasher counts the passwords it hashes.
type countingHasher struct {
	*password.Hasher
	mu     sync.Mutex
	hashed int
}

func (h *countingHasher) Hash(ctx context.Context, p secret.String) (string, error) {
	h.mu.Lock()
	h.hashed++
	h.mu.Unlock()
	return h.Hasher.Hash(ctx, p)
}

func TestARefusedBootstrapHashesNoPassword(t *testing.T) {
	pool := testkit.FreshPool(t)
	if _, err := bootstrap(t, pool, "first@example.com"); err != nil {
		t.Fatal(err)
	}
	c := testkit.Clock()
	hasher := &countingHasher{Hasher: password.NewHasher(password.DefaultParams)}
	h := appaccess.NewBootstrap(postgres.NewTransactor(pool), pgaccess.NewStore(pool), hasher,
		appgovernance.NewRecord(pggovernance.NewAuditLog(pool)), c, id.NewSource(c), rand.Reader)
	_, err := h.Handle(t.Context(), appaccess.BootstrapCommand{
		Email: "second@example.com", Password: secret.New("a long enough password"), Request: governance.Request{Actor: cli},
	})
	if e, _ := apperrors.From(err); e == nil || e.Code != "instance_admin_exists" {
		t.Fatalf("err = %v, want instance_admin_exists", err)
	}
	if hasher.hashed != 0 {
		t.Errorf("%d passwords hashed, want none: the instance admin is looked for under the lock first", hasher.hashed)
	}
}

func TestWhetherAnInstanceAdminExistsIsAnswered(t *testing.T) {
	pool := testkit.FreshPool(t)
	q := compose.InstanceAdminExists(compose.NewDeps(pool, testkit.Clock()))
	if exists, err := q.Handle(t.Context()); err != nil || exists {
		t.Fatalf("on a fresh instance: %v, %v; want false", exists, err)
	}
	if _, err := bootstrap(t, pool, "first@example.com"); err != nil {
		t.Fatal(err)
	}
	if exists, err := q.Handle(t.Context()); err != nil || !exists {
		t.Errorf("after bootstrap: %v, %v; want true", exists, err)
	}
}
