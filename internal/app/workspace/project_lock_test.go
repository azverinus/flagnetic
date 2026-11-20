package workspace_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// Through the API a write reaches its handler only for a project the request
// found, so only the use cases themselves show a project deleted by the time
// its row is locked: each answers the unknown project, not a failure.
func TestWritesOfAProjectGoneBeforeItsLockAreNotFound(t *testing.T) {
	pool := testkit.Pool(t)
	c := clock.System{}
	ids := id.NewSource(c)
	tx, ws, flagStore := postgres.NewTransactor(pool), pgworkspace.NewStore(pool), pgflags.NewStore(pool)
	rec := appworkspace.NewRecorder(testkit.Record(pool), c, ids)
	rulesets := appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool), c)
	guard := appflags.NewDeclarationGuard(flagStore)

	gone := id.New[workspace.Project](ids)
	req := governance.Request{Actor: governance.CLIActor("test")}
	name, desc := "n", "d"
	writes := map[string]func(ctx context.Context) error{
		"create environment": func(ctx context.Context) error {
			_, err := appworkspace.NewCreateEnvironment(tx, ws, appflags.NewConfigureEnvironment(flagStore), rec).Handle(ctx,
				appworkspace.CreateEnvironmentCommand{Project: gone, Key: "qa", Name: "QA", Request: req})
			return err
		},
		"update environment": func(ctx context.Context) error {
			_, err := appworkspace.NewUpdateEnvironment(tx, ws, rec).Handle(ctx, appworkspace.UpdateEnvironmentCommand{
				Project: gone, Key: "production", Change: workspace.EnvironmentChange{Name: &name}, Expected: precondition.On(1), Request: req})
			return err
		},
		"set environment protection": func(ctx context.Context) error {
			_, err := appworkspace.NewSetEnvironmentProtection(tx, ws, rulesets, rec).Handle(ctx, appworkspace.SetEnvironmentProtectionCommand{
				Project: gone, Key: "production", Protected: true, Expected: precondition.On(1), Request: req})
			return err
		},
		"delete environment": func(ctx context.Context) error {
			return appworkspace.NewDeleteEnvironment(tx, ws, rec).Handle(ctx, appworkspace.DeleteEnvironmentCommand{
				Project: gone, Key: "production", Expected: precondition.On(1), Request: req})
		},
		"create unit type": func(ctx context.Context) error {
			_, err := appworkspace.NewCreateUnitType(tx, ws, rulesets, rec).Handle(ctx, appworkspace.CreateUnitTypeCommand{
				Project: gone, Key: "user", Name: "User", Request: req})
			return err
		},
		"update unit type": func(ctx context.Context) error {
			_, err := appworkspace.NewUpdateUnitType(tx, ws, rec).Handle(ctx, appworkspace.UpdateUnitTypeCommand{
				Project: gone, Key: "user", Change: workspace.UnitTypeChange{Name: &name}, Expected: precondition.On(1), Request: req})
			return err
		},
		"delete unit type": func(ctx context.Context) error {
			return appworkspace.NewDeleteUnitType(tx, ws, guard, rulesets, rec).Handle(ctx, appworkspace.DeleteUnitTypeCommand{
				Project: gone, Key: "user", Expected: precondition.On(1), Request: req})
		},
		"update attribute": func(ctx context.Context) error {
			_, err := appworkspace.NewUpdateAttribute(tx, ws, rec).Handle(ctx, appworkspace.UpdateAttributeCommand{
				Project: gone, Key: "plan", Change: workspace.AttributeChange{Name: &name, Description: &desc}, Expected: precondition.On(1), Request: req})
			return err
		},
		"delete attribute": func(ctx context.Context) error {
			return appworkspace.NewDeleteAttribute(tx, ws, guard, rulesets, rec).Handle(ctx, appworkspace.DeleteAttributeCommand{
				Project: gone, Key: "plan", Expected: precondition.On(1), Request: req})
		},
		"update project": func(ctx context.Context) error {
			_, err := appworkspace.NewUpdateProject(tx, ws, rec).Handle(ctx, appworkspace.UpdateProjectCommand{
				Project: gone, Change: workspace.ProjectChange{Name: &name}, Expected: precondition.On(1), Request: req})
			return err
		},
		"delete project": func(ctx context.Context) error {
			return appworkspace.NewDeleteProject(tx, ws, rec).Handle(ctx, appworkspace.DeleteProjectCommand{
				Project: gone, Expected: precondition.On(1), Request: req})
		},
	}
	for what, write := range writes {
		t.Run(what, func(t *testing.T) {
			if err := write(t.Context()); !errors.Is(err, access.ErrProjectNotFound) {
				t.Errorf("error = %v, want %v", err, access.ErrProjectNotFound)
			}
		})
	}
}

// Renaming a unit type or an attribute takes the project's row, so an import
// that read the declaration's version under that row never meets a rename
// committed in between.
func TestRenamingADeclarationWaitsForTheProjectsRow(t *testing.T) {
	pool := testkit.Pool(t)
	c := clock.System{}
	ids := id.NewSource(c)
	tx, ws := postgres.NewTransactor(pool), pgworkspace.NewStore(pool)
	rec := appworkspace.NewRecorder(testkit.Record(pool), c, ids)
	rulesets := appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool), c)
	req := governance.Request{Actor: governance.CLIActor("test")}

	p, err := workspace.NewProject(id.New[workspace.Project](ids), "p"+id.New[workspace.Project](ids).String()[24:], "P", "", testkit.Start)
	if err != nil {
		t.Fatal(err)
	}
	if err = ws.CreateProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = appworkspace.NewCreateUnitType(tx, ws, rulesets, rec).Handle(t.Context(), appworkspace.CreateUnitTypeCommand{
		Project: p.ID, Key: "user", Name: "User", Request: req}); err != nil {
		t.Fatal(err)
	}
	if _, err = appworkspace.NewCreateAttribute(tx, ws, rulesets, rec).Handle(t.Context(), appworkspace.CreateAttributeCommand{
		Project: p.ID, Key: "plan", Name: "Plan", Type: workspace.TypeString, Request: req}); err != nil {
		t.Fatal(err)
	}

	name := "Renamed"
	renames := map[string]func(ctx context.Context) error{
		"unit type": func(ctx context.Context) error {
			_, err := appworkspace.NewUpdateUnitType(tx, ws, rec).Handle(ctx, appworkspace.UpdateUnitTypeCommand{
				Project: p.ID, Key: "user", Change: workspace.UnitTypeChange{Name: &name}, Expected: precondition.On(1), Request: req})
			return err
		},
		"attribute": func(ctx context.Context) error {
			_, err := appworkspace.NewUpdateAttribute(tx, ws, rec).Handle(ctx, appworkspace.UpdateAttributeCommand{
				Project: p.ID, Key: "plan", Change: workspace.AttributeChange{Name: &name}, Expected: precondition.On(1), Request: req})
			return err
		},
	}
	for what, rename := range renames {
		t.Run(what, func(t *testing.T) {
			release := make(chan struct{})
			locked := make(chan struct{})
			holder := make(chan error, 1)
			go func() {
				holder <- tx.Within(t.Context(), func(ctx context.Context) error {
					if _, err := ws.LockProject(ctx, p.ID); err != nil {
						return err
					}
					close(locked)
					<-release
					return nil
				})
			}()
			<-locked
			done := make(chan error, 1)
			go func() { done <- rename(t.Context()) }()
			select {
			case err := <-done:
				t.Fatalf("rename finished while the project's row was held: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			close(release)
			if err := <-holder; err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Errorf("rename after the row was released = %v", err)
			}
		})
	}
}
