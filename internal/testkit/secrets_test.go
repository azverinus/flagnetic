package testkit_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// planted is a secret the tests below write into audit entries themselves,
// without recording it, so the guard run after them does not refuse it.
const planted = "fn_pat_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcd"

func TestTheAuditGuardFindsASecretInAnyFieldOfAnEntry(t *testing.T) {
	for name, change := range map[string]func(*governance.Request, *governance.Record){
		"before": func(_ *governance.Request, r *governance.Record) { r.Before = map[string]any{"value": planted} },
		"after": func(_ *governance.Request, r *governance.Record) {
			r.After = map[string]any{"nested": []any{"x " + planted}}
		},
		"entity key": func(_ *governance.Request, r *governance.Record) { r.Entity.Key = planted },
		"actor name": func(q *governance.Request, _ *governance.Record) { q.Actor.Name = "token " + planted },
		"request":    func(q *governance.Request, _ *governance.Record) { q.ID = planted },
	} {
		t.Run(name, func(t *testing.T) {
			pool := testkit.FreshPool(t)
			clock := testkit.Clock()
			req := governance.Request{Actor: governance.CLIActor("admin bootstrap")}
			rec := governance.Record{Action: "member.created", Entity: governance.Entity{Kind: "member", ID: "m1", Key: "first@example.com"}}
			change(&req, &rec)
			e, err := governance.NewEntry(id.New[governance.Entry](testkit.IDs(clock)), clock.Now(), req, rec)
			if err != nil {
				t.Fatal(err)
			}
			if err = pggovernance.NewAuditLog(pool).Record(t.Context(), e); err != nil {
				t.Fatal(err)
			}
			err = testkit.FindSecretsInAudit(t.Context(), pool, []string{"another secret value", planted})
			if err == nil {
				t.Fatal("the guard found no secret in the audit log")
			}
			if strings.Contains(err.Error(), planted) {
				t.Errorf("the guard's report repeats the secret: %v", err)
			}
		})
	}
}

func TestTheAuditGuardAcceptsAnEntryNamingATokenByItsHint(t *testing.T) {
	pool := testkit.FreshPool(t)
	clock := testkit.Clock()
	e, err := governance.NewEntry(id.New[governance.Entry](testkit.IDs(clock)), clock.Now(),
		governance.Request{Actor: governance.CLIActor("admin bootstrap")},
		governance.Record{Action: "api_token.created", Entity: governance.Entity{Kind: "api_token", ID: "t1", Key: "bootstrap"},
			After: map[string]any{"hint": planted[:11]}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pggovernance.NewAuditLog(pool).Record(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if err := testkit.FindSecretsInAudit(t.Context(), pool, []string{planted}); err != nil {
		t.Errorf("the guard refused an entry holding a hint alone: %v", err)
	}
}

func TestTheAuditGuardPassesADatabaseNotYetMigrated(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), testkit.EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := testkit.FindSecretsInAudit(t.Context(), pool, []string{planted}); err != nil {
		t.Errorf("the guard failed on a database with no audit log: %v", err)
	}
}

func TestSecretsAreRecordedFromTextByTheirShape(t *testing.T) {
	// Another value than planted, which entries above hold on purpose and
	// the guard must therefore not learn.
	const printed = "fn_prj_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN"
	testkit.RecordSecretsIn("Token: " + printed + "\nHint: " + printed[:11])
	if !slices.Contains(testkit.RecordedSecrets(), printed) {
		t.Error("a token value in text was not recorded")
	}
	if slices.Contains(testkit.RecordedSecrets(), printed[:11]) {
		t.Error("a token hint was recorded as a secret")
	}
}

func TestTheTestClientRecordsThePasswordsItSends(t *testing.T) {
	c := testkit.Client(t, testkit.StartServer(t), "")
	res, err := c.CreateProjectWithBodyWithResponse(t.Context(), "application/json",
		strings.NewReader(`{"key":"web","current_password":"a password the test sent"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusUnauthorized {
		t.Errorf("status = %d, want the request sent and refused", res.StatusCode())
	}
	if !slices.Contains(testkit.RecordedSecrets(), "a password the test sent") {
		t.Error("the password a request carried was not recorded")
	}
}
