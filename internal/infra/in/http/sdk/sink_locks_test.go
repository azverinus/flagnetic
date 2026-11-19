package sdk_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// heldUnit is a unit of work on the test database kept open, running what it
// is told to in turn, until it is released.
type heldUnit struct {
	// pid is the database session the unit runs in, which the sessions
	// waiting for what it holds are found by.
	pid  int
	jobs chan func(ctx context.Context) error
	done chan error
	once sync.Once
}

// openUnit opens a unit of work on the test database; the test's end releases
// it if it was not.
func openUnit(t *testing.T) *heldUnit {
	t.Helper()
	pool := testkit.Pool(t)
	u := &heldUnit{jobs: make(chan func(ctx context.Context) error), done: make(chan error, 1)}
	ready := make(chan error, 1)
	go func() {
		u.done <- postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
			if err := pgxtx.Querier(ctx, pool).QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&u.pid); err != nil {
				ready <- err
				return err
			}
			ready <- nil
			for job := range u.jobs {
				if err := job(ctx); err != nil {
					return err
				}
			}
			return nil
		})
	}()
	if err := <-ready; err != nil {
		t.Fatalf("opening a unit of work: %v", err)
	}
	t.Cleanup(func() { u.release(t) })
	return u
}

// run runs fn in the unit of work and returns its error, which ends the unit.
func (u *heldUnit) run(fn func(ctx context.Context) error) error {
	result := make(chan error, 1)
	u.jobs <- func(ctx context.Context) error {
		err := fn(ctx)
		result <- err
		return err
	}
	return <-result
}

// release commits the unit of work.
func (u *heldUnit) release(t *testing.T) {
	t.Helper()
	u.once.Do(func() {
		close(u.jobs)
		if err := <-u.done; err != nil {
			t.Errorf("the unit of work that held the locks: %v", err)
		}
	})
}

// holdProject holds project's lock, the one every write of it takes, as a
// writer of the project does.
func holdProject(t *testing.T, project workspace.ProjectID) *heldUnit {
	t.Helper()
	u := openUnit(t)
	store := pgsinks.NewStore(testkit.Pool(t))
	if err := u.run(func(ctx context.Context) error { return store.LockProject(ctx, project) }); err != nil {
		t.Fatalf("locking the project: %v", err)
	}
	return u
}

// holdSink holds the sink's row, as resuming or deleting the sink does before
// it takes its streams; the sink is not stopped, so resuming changes nothing.
func holdSink(t *testing.T, sink management.Sink) *heldUnit {
	t.Helper()
	u := openUnit(t)
	store := pgsinks.NewStore(testkit.Pool(t))
	err := u.run(func(ctx context.Context) error {
		_, err := store.Resume(ctx, id.FromBytes[sinks.Sink](sink.Id), time.Time{})
		return err
	})
	if err != nil {
		t.Fatalf("locking the sink: %v", err)
	}
	return u
}

// lockStream takes the sink's stream of kind in the unit of work, giving up
// after a second.
func (u *heldUnit) lockStream(t *testing.T, sink management.Sink, kind sinks.EventKind) error {
	t.Helper()
	store := pgsinks.NewStore(testkit.Pool(t))
	return u.run(func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_, err := store.LockStream(ctx, id.FromBytes[sinks.Sink](sink.Id), kind)
		return err
	})
}

// streamFreeWhileHeld reports whether the sink's stream of kind can be locked
// within a second, which it cannot while a writer that took the sink's rows
// before the project waits for the project.
func streamFreeWhileHeld(t *testing.T, sink management.Sink, kind sinks.EventKind) bool {
	t.Helper()
	store := pgsinks.NewStore(testkit.Pool(t))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := postgres.NewTransactor(testkit.Pool(t)).Within(ctx, func(ctx context.Context) error {
		_, lockErr := store.LockStream(ctx, id.FromBytes[sinks.Sink](sink.Id), kind)
		return lockErr
	})
	return err == nil
}
