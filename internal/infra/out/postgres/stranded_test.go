package postgres_test

import (
	"context"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A cursor names the transaction of the last row its stream read. A logical
// dump keeps the cursors and its restore starts the transaction counter
// again, far below them: every stream then waits, silently, for
// transactions the new cluster will not reach for a long time.
func TestACursorAheadOfTheClustersTransactionsIsStranded(t *testing.T) {
	pool := testkit.Pool(t)
	store := pgevents.NewStore(pool)
	const here, restored = events.Stream("stranded_test_here"), events.Stream("stranded_test_restored")
	// The test's own context has ended by the time its cleanup runs.
	ctx := context.WithoutCancel(t.Context())
	move := func(stream events.Stream, to events.Cursor) {
		t.Helper()
		if err := store.MoveCursor(ctx, stream, to, testkit.Start); err != nil {
			t.Fatal(err)
		}
	}
	// Left behind the cluster's transactions for the tests that follow.
	t.Cleanup(func() { move(restored, events.Cursor{XID: 1, Seq: 1}) })

	move(here, events.Cursor{XID: 1, Seq: 1})
	move(restored, events.Cursor{XID: 1 << 60, Seq: 7})
	got, err := postgres.StrandedCursors(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "rollup "+string(restored)) || slices.Contains(got, "rollup "+string(here)) {
		t.Errorf("stranded cursors = %v, want the one ahead of this cluster's transactions alone", got)
	}
}
