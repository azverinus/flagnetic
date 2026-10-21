package flags_test

import (
	"context"
	"errors"
	"os"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// Through the API a write reaches its handler only for a project the
// request found, and no operation deletes a project, so only the use cases
// themselves show a project missing by the time its row is locked.
func TestWritesOfAProjectGoneBeforeItsLockAreNotFound(t *testing.T) {
	pool := testkit.Pool(t)
	c := clock.System{}
	ids := id.NewSource(c)
	tx, ws, store := postgres.NewTransactor(pool), pgworkspace.NewStore(pool), pgflags.NewStore(pool)
	rec := appflags.NewRecorder(pggovernance.NewAuditLog(pool), c, ids)
	rulesets := appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool))
	owners := appaccess.NewOwnership(pgaccess.NewStore(pool))
	archival := appflags.ArchivalDeps{Tx: tx, Projects: store, Store: store, Rulesets: rulesets, Recorder: rec}

	gone := id.New[workspace.Project](ids)
	req := governance.Request{Actor: governance.CLIActor("test")}
	flag := appflags.FlagCommand{Project: gone, Key: "checkout.newFlow", Request: req}
	segment := appflags.SegmentCommand{Project: gone, Key: "beta", Request: req}
	writes := map[string]func(ctx context.Context) error{
		"create flag": func(ctx context.Context) error {
			_, err := appflags.NewCreateFlag(appflags.CreateFlagDeps{
				Tx: tx, Projects: store, Environments: ws, Store: store, Owners: owners, Rulesets: rulesets, Recorder: rec,
			}).Handle(ctx, appflags.CreateFlagCommand{
				Project: gone, Key: "checkout.newFlow", Type: flags.TypeBoolean, Name: "New checkout", Request: req,
			})
			return err
		},
		"update flag": func(ctx context.Context) error {
			_, err := appflags.NewUpdateFlag(appflags.UpdateFlagDeps{
				Tx: tx, Projects: store, Store: store, Owners: owners, Rulesets: rulesets, Recorder: rec,
			}).Handle(ctx, appflags.UpdateFlagCommand{Project: gone, Key: flag.Key, Request: req})
			return err
		},
		"archive flag": func(ctx context.Context) error {
			_, err := appflags.NewArchiveFlag(archival).Handle(ctx, flag)
			return err
		},
		"restore flag": func(ctx context.Context) error {
			_, err := appflags.NewRestoreFlag(archival).Handle(ctx, flag)
			return err
		},
		"delete flag": func(ctx context.Context) error {
			return appflags.NewDeleteFlag(appflags.DeleteFlagDeps{
				Tx: tx, Projects: store, Store: store, Recorder: rec,
			}).Handle(ctx, flag)
		},
		"create segment": func(ctx context.Context) error {
			_, err := appflags.NewCreateSegment(appflags.CreateSegmentDeps{
				Tx: tx, Projects: store, Declarations: ws, Store: store, Rulesets: rulesets, Recorder: rec,
			}).Handle(ctx, appflags.CreateSegmentCommand{Project: gone, Key: segment.Key, Name: "Beta", Request: req})
			return err
		},
		"update segment": func(ctx context.Context) error {
			_, err := appflags.NewUpdateSegment(appflags.UpdateSegmentDeps{
				Tx: tx, Projects: store, Declarations: ws, Store: store, Rulesets: rulesets, Recorder: rec,
			}).Handle(ctx, appflags.UpdateSegmentCommand{Project: gone, Key: segment.Key, Request: req})
			return err
		},
		"delete segment": func(ctx context.Context) error {
			return appflags.NewDeleteSegment(appflags.DeleteSegmentDeps{
				Tx: tx, Projects: store, Store: store, Rulesets: rulesets, Recorder: rec,
			}).Handle(ctx, segment)
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			if err := write(t.Context()); !errors.Is(err, access.ErrProjectNotFound) {
				t.Fatalf("error = %v, want the project not found", err)
			}
		})
	}
}
