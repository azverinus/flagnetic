package governance_test

import (
	"context"
	"errors"
	"os"
	"testing"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// entry returns a well-formed entry of a configuration change in a project
// and environment of its own.
func entry(t *testing.T, ids *id.Source) governance.Entry {
	t.Helper()
	e, err := governance.NewEntry(id.New[governance.Entry](ids), testkit.Start,
		governance.Request{Actor: governance.CLIActor("test")}, governance.Record{
			Project:     id.New[workspace.Project](ids),
			Environment: id.New[workspace.Environment](ids),
			Action:      "flag.configuration_updated",
			Entity:      governance.Entity{Kind: "flag", ID: "0190d2a0-0000-7000-8000-0000000000f1", Key: "banner"},
		})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAnEntryRecordedInACommittedWriteIsKept(t *testing.T) {
	pool := testkit.FreshPool(t)
	log := pggovernance.NewAuditLog(pool)
	ids := testkit.IDs(testkit.Clock())
	record := appgovernance.NewRecord(log, pggovernance.NewChangeRequests(pool), appflags.NewSegmentReferrers(pgflags.NewStore(pool)), ids)
	e := entry(t, ids)

	err := postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
		return record.Record(ctx, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := log.Entry(t.Context(), e.Project, e.ID)
	if err != nil {
		t.Fatalf("Entry = %v; want the recorded entry", err)
	}
	if got.Entity != e.Entity || got.Environment != e.Environment || got.Action != e.Action {
		t.Errorf("entry = %+v, want %+v", got, e)
	}
}

func TestAnEntryRecordedInARolledBackWriteIsNotKept(t *testing.T) {
	pool := testkit.FreshPool(t)
	log := pggovernance.NewAuditLog(pool)
	ids := testkit.IDs(testkit.Clock())
	record := appgovernance.NewRecord(log, pggovernance.NewChangeRequests(pool), appflags.NewSegmentReferrers(pgflags.NewStore(pool)), ids)
	e := entry(t, ids)
	refused := errors.New("the write failed after its entry was recorded")

	err := postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
		if err := record.Record(ctx, e); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("Within = %v; want the write's own failure", err)
	}
	if _, err := log.Entry(t.Context(), e.Project, e.ID); !errors.Is(err, appgovernance.ErrNotFound) {
		t.Errorf("Entry = %v; want no entry of the rolled-back write", err)
	}
}

func TestAnEntryTheLogRefusesFailsTheWrite(t *testing.T) {
	pool := testkit.FreshPool(t)
	record := testkit.Record(pool)
	e := entry(t, testkit.IDs(testkit.Clock()))

	err := postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
		if err := record.Record(ctx, e); err != nil {
			return err
		}
		// The same entry twice is one the log refuses.
		return record.Record(ctx, e)
	})
	if err == nil {
		t.Fatal("Within = nil; want the log's refusal of an entry it holds already")
	}
	if _, err := pggovernance.NewAuditLog(pool).Entry(t.Context(), e.Project, e.ID); !errors.Is(err, appgovernance.ErrNotFound) {
		t.Errorf("Entry = %v; want nothing kept of the failed write", err)
	}
}
