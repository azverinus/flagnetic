package events_test

import (
	"testing"
	"time"

	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A replica waiting for the advisory lock of partition creation gives up at
// the lock timeout, as it does for a table lock, instead of holding its
// connection for as long as another replica holds the lock.
func TestCreatingPartitionsGivesUpWhileAnotherReplicaHoldsTheLock(t *testing.T) {
	pool := testkit.FreshPool(t)
	defer pgevents.SetPartitionLockTimeout(300 * time.Millisecond)()
	store := pgevents.NewStore(pool)

	holder, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(t.Context()) }()
	if _, err = holder.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('event_partitions', 0))`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err = store.CreateEventPartitions(t.Context(), time.Date(2031, 1, 2, 0, 0, 0, 0, time.UTC), 0); err == nil {
		t.Fatal("creating partitions succeeded while the lock was held")
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("waited %s for the lock, want it bounded by the lock timeout", waited)
	}
}
