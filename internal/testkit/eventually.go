package testkit

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Eventually polls cond every 10 ms until it holds or within passes,
// reporting whether it held: a test waits for what it expects to happen and
// fails only past a deadline long enough for a loaded runner, instead of
// asserting how soon it happened.
func Eventually(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// WaitForLockWaits returns once at least n backends of pool's database wait
// on a lock another transaction holds: a test that holds a lock and starts
// requests waits for them to reach it, as nothing but the database says they
// have, and a sleep stands for it only as long as the runner is idle. It
// fails the test after 10 seconds.
func WaitForLockWaits(t testing.TB, pool *pgxpool.Pool, n int) {
	t.Helper()
	WaitForLockWaitsOrDone(t, pool, n, nil)
}

// WaitForLockWaitsOrDone is WaitForLockWaits for a request that may also
// finish without waiting: it returns once done is closed as well.
func WaitForLockWaitsOrDone(t testing.TB, pool *pgxpool.Pool, n int, done <-chan struct{}) {
	t.Helper()
	var waiting int
	if !Eventually(10*time.Second, func() bool {
		select {
		case <-done:
			return true
		default:
		}
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatalf("reading the lock waits: %v", err)
		}
		return waiting >= n
	}) {
		t.Fatalf("%d backends wait on a lock, want %d", waiting, n)
	}
}
