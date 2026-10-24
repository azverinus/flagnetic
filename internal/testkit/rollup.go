package testkit

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// held are the IDs of the transactions tests of the package hold open on
// purpose, which TickRollup does not wait for.
var held struct {
	mu   sync.Mutex
	xids map[string]bool
}

// HoldNextIngest keeps the transaction of the next batch of events the server
// ingests open once its rows are written, so a test can show what a
// transaction still open holds back. written is closed then; release lets it
// commit, and is called when the test ends at the latest.
func (s *Server) HoldNextIngest(t testing.TB) (written <-chan struct{}, release func()) {
	t.Helper()
	xid, letGo := s.hold.HoldNext()
	w := make(chan struct{})
	released := make(chan struct{})
	// mine and done are guarded by held.mu: the transaction's ID may arrive
	// after the test let it go, and must then not be left marked as held.
	var (
		mine string
		done bool
	)
	go func() {
		var x string
		select {
		case x = <-xid:
		case <-released:
			return
		}
		held.mu.Lock()
		if done {
			held.mu.Unlock()
			return
		}
		if held.xids == nil {
			held.xids = map[string]bool{}
		}
		mine = x
		held.xids[mine] = true
		held.mu.Unlock()
		close(w)
	}()
	var once sync.Once
	release = func() {
		once.Do(func() {
			held.mu.Lock()
			done = true
			if mine != "" {
				delete(held.xids, mine)
			}
			held.mu.Unlock()
			close(released)
			letGo()
		})
	}
	t.Cleanup(release)
	return w, release
}

// TickRollup runs one round of the rollup worker called name, once every
// transaction running in the database when it was called has ended, except
// those a test holds open on purpose: a rollup reads only below the snapshot
// horizon, which any transaction of another test, of this package or another
// one sharing the database, holds back while it runs. What the test committed
// before the call is then read by the round.
func (s *Server) TickRollup(t testing.TB, name string) {
	t.Helper()
	s.awaitHorizon(t)
	if err := s.Tick(t, name); err != nil {
		t.Fatalf("tick %s: %v", name, err)
	}
}

func (s *Server) awaitHorizon(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `SELECT x::text FROM pg_snapshot_xip(pg_current_snapshot()) x`)
	if err != nil {
		t.Fatalf("reading the running transactions: %v", err)
	}
	running, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("reading the running transactions: %v", err)
	}
	held.mu.Lock()
	running = slices.DeleteFunc(running, func(x string) bool { return held.xids[x] })
	held.mu.Unlock()
	deadline := time.Now().Add(30 * time.Second)
	for len(running) > 0 {
		var n int
		err := s.pool.QueryRow(ctx, `SELECT count(*) FROM unnest($1::text[]) x WHERE pg_xact_status(x::xid8) = 'in progress'`,
			running).Scan(&n)
		if err != nil {
			t.Fatalf("reading the status of the running transactions: %v", err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("transactions %v still running after 30 s", running)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
