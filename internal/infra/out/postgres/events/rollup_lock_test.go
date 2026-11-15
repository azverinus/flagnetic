package events_test

import (
	"context"
	"testing"
	"time"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A rollup batch holds every project it names against deletion only: a write
// in one project neither waits for a write held in another project the batch
// names, nor makes the batch wait for it.
func TestARollupBatchDoesNotHoldUpAProjectsWrites(t *testing.T) {
	pool := testkit.FreshPool(t)
	ids := testkit.IDs(testkit.Clock())
	ws := pgworkspace.NewStore(pool)
	var projects [2]workspace.Project
	for i, key := range []string{"alpha", "beta"} {
		p, err := workspace.NewProject(id.New[workspace.Project](ids), key, key, "", testkit.Start)
		if err != nil {
			t.Fatal(err)
		}
		if err = ws.CreateProject(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		projects[i] = p
	}
	tx, locks, rollup := postgres.NewTransactor(pool), pgflags.NewStore(pool), pgevents.NewStore(pool)

	// A long write holds beta.
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- tx.Within(t.Context(), func(ctx context.Context) error {
			if err := locks.LockProject(ctx, projects[1].ID); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-done
	})
	<-held

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := tx.Within(ctx, func(ctx context.Context) error {
		found, err := rollup.LockTargets(ctx, appevents.RollupTargets{Projects: []workspace.ProjectID{projects[0].ID, projects[1].ID}})
		if err != nil {
			return err
		}
		if len(found.Projects) != 2 {
			t.Errorf("locked %d projects, want 2", len(found.Projects))
		}
		// The batch is still open: a write in alpha must go through.
		other, cancelWrite := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancelWrite()
		return tx.Within(other, func(ctx context.Context) error { return locks.LockProject(ctx, projects[0].ID) })
	})
	if err != nil {
		t.Fatalf("a write in alpha, or the batch, waited for the write held in beta: %v", err)
	}
}
