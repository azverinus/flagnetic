package governance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

type envIDs struct{ src *id.Source }

func (i envIDs) Environment() workspace.EnvironmentID { return id.New[workspace.Environment](i.src) }
func (i envIDs) UnitType() workspace.UnitTypeID       { return id.New[workspace.UnitType](i.src) }
func (i envIDs) Attribute() workspace.AttributeID     { return id.New[workspace.Attribute](i.src) }

// The guard trusts the committed ruleset as a write's before only because
// the write holds its project's row: a bump without it is refused, an
// unprotected environment's included, and moves nothing.
func TestABumpWithoutTheProjectsRowHeldIsRefused(t *testing.T) {
	pool := testkit.FreshPool(t)
	ids := testkit.IDs(testkit.Clock())
	p, err := workspace.NewProject(id.New[workspace.Project](ids), "web", "Web", "", testkit.Start)
	if err != nil {
		t.Fatal(err)
	}
	ws := pgworkspace.NewStore(pool)
	if err = ws.CreateProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	var staging workspace.EnvironmentID
	for _, e := range workspace.DefaultsOf(p, envIDs{ids}).Environments {
		if err = ws.CreateEnvironment(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		if e.Key == "staging" {
			staging = e.ID
		}
	}
	deliveryStore := pgdelivery.NewStore(pool)
	guard := pggovernance.NewProtectionGuard(pggovernance.ProtectionGuardDeps{
		Pool: pool, Next: appdelivery.NewBumpRulesets(deliveryStore, testkit.Clock()), Registerer: prometheus.NewRegistry(),
	})
	tx, locker := postgres.NewTransactor(pool), pgflags.NewStore(pool)
	version := func() int64 {
		v, readErr := deliveryStore.RulesetVersions(t.Context(), p.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return v["staging"]
	}
	before := version()

	for name, bump := range map[string]func() error{
		"outside a transaction": func() error { return guard.Environment(t.Context(), staging) },
		"in a transaction without the lock": func() error {
			return tx.Within(t.Context(), func(ctx context.Context) error { return guard.Environment(ctx, staging) })
		},
		"a project's in a transaction without the lock": func() error {
			return tx.Within(t.Context(), func(ctx context.Context) error { return guard.Project(ctx, p.ID) })
		},
	} {
		if bumpErr := bump(); !errors.Is(bumpErr, pggovernance.ErrProjectNotHeld) {
			t.Errorf("%s: err = %v, want ErrProjectNotHeld", name, bumpErr)
		}
	}
	if got := version(); got != before {
		t.Errorf("staging moved to version %d by refused bumps, want %d", got, before)
	}

	err = tx.Within(t.Context(), func(ctx context.Context) error {
		if lockErr := locker.LockProject(ctx, p.ID); lockErr != nil {
			return lockErr
		}
		return guard.Environment(ctx, staging)
	})
	if err != nil || version() <= before {
		t.Errorf("a bump holding the lock = %v, staging at %d after %d; want it moved", err, version(), before)
	}
}
