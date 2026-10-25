package governance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/eval"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/protection"
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
		// Inserting a row that references the project takes a key share
		// lock on its row, which leaves the transaction's id in the row
		// without keeping any other write out of the project.
		"in a transaction that only references the project's row": func() error {
			return tx.Within(t.Context(), func(ctx context.Context) error {
				qa, newErr := workspace.NewEnvironment(id.New[workspace.Environment](ids), p.ID, "qa", "QA", 4, testkit.Start)
				if newErr != nil {
					return newErr
				}
				if createErr := ws.CreateEnvironment(ctx, qa); createErr != nil {
					return createErr
				}
				return guard.Environment(ctx, staging)
			})
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

// switchedOn answers, for every environment, a ruleset that switched the
// flag checkout on, as a write's transaction would leave it.
type switchedOn struct{}

func (switchedOn) InUnitOfWork(context.Context, workspace.EnvironmentID) (appdelivery.Ruleset, error) {
	return appdelivery.Ruleset{Evaluable: &eval.Ruleset{Flags: []eval.Flag{{Key: "checkout", On: true}}}}, nil
}

// switchedOff answers, for every committed version, a ruleset holding
// checkout off.
type switchedOff struct{}

func (switchedOff) CommittedRuleset(_ context.Context, _ workspace.EnvironmentID, version int64) (appdelivery.Ruleset, error) {
	return appdelivery.Ruleset{Version: version, Evaluable: &eval.Ruleset{Flags: []eval.Flag{{Key: "checkout", On: false}}}}, nil
}

// The application of a change request bypasses the environments its
// approval covered and no other: a write that also changes another
// protected environment is refused for that one, and the kill switch's
// bypass lets everything through.
func TestABypassCoversOnlyTheEnvironmentsItNames(t *testing.T) {
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
	var production workspace.EnvironmentID
	for _, e := range workspace.DefaultsOf(p, envIDs{ids}).Environments {
		e.Protected = e.Key == "staging" || e.Key == "production"
		if err = ws.CreateEnvironment(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		if e.Key == "production" {
			production = e.ID
		}
	}
	guard := pggovernance.NewProtectionGuard(pggovernance.ProtectionGuardDeps{
		Pool: pool, Next: appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool), testkit.Clock()),
		After: switchedOn{}, Before: switchedOff{}, Registerer: prometheus.NewRegistry(),
	})
	tx, locker := postgres.NewTransactor(pool), pgflags.NewStore(pool)
	bump := func(ctx context.Context) error {
		return tx.Within(ctx, func(ctx context.Context) error {
			if lockErr := locker.LockProject(ctx, p.ID); lockErr != nil {
				return lockErr
			}
			return guard.Project(ctx, p.ID)
		})
	}

	err = bump(protection.Bypass(t.Context(), production))
	var refusal *apperrors.Error
	if !errors.As(err, &refusal) || refusal.Code != "change_request_required" ||
		!strings.Contains(refusal.Detail, "staging") || strings.Contains(refusal.Detail, "production") {
		t.Errorf("a write bypassing production only = %v, want change_request_required naming staging alone", err)
	}
	if err = bump(protection.BypassEverywhere(t.Context())); err != nil {
		t.Errorf("a write bypassing every environment = %v, want it let through", err)
	}
}
