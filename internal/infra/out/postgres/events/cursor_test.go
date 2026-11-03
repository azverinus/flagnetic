package events_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// A batch ends at its greatest transaction ID by number: transaction 10 comes
// after transaction 9 although "10" sorts before "9" as text. A cursor
// stopped short would read the rows past it again once transaction IDs gain
// a digit.
func TestABatchEndsAtItsGreatestTransactionByNumber(t *testing.T) {
	pool := testkit.Pool(t)
	if _, err := pool.Exec(t.Context(), `
		CREATE TABLE stream_by_number (xid xid8 NOT NULL, seq bigint NOT NULL);
		INSERT INTO stream_by_number VALUES ('9', 1), ('10', 2)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE stream_by_number`) })
	b, err := pgevents.Bound(t.Context(), pool, "stream_by_number", pgevents.Scope{}, events.Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rows != 2 || b.To != (events.Cursor{XID: 10, Seq: 2}) {
		t.Errorf("batch = %d rows to %+v, want 2 rows to transaction 10, seq 2", b.Rows, b.To)
	}
	b, err = pgevents.Bound(t.Context(), pool, "stream_by_number", pgevents.Scope{}, b.To, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rows != 0 || b.To != (events.Cursor{XID: 10, Seq: 2}) {
		t.Errorf("the batch after = %d rows to %+v, want none, the cursor where it was", b.Rows, b.To)
	}
}

// The end of a table and the first row received after a time are found by
// transaction ID by number, as a batch is: transaction 100 comes after
// transaction 99 although "100" sorts before "99" as text.
func TestTheTailAndTheFirstRowAfterATimeAreFoundByTransactionNumber(t *testing.T) {
	pool := testkit.Pool(t)
	if _, err := pool.Exec(t.Context(), `
		CREATE TABLE tail_by_number (xid xid8 NOT NULL, seq bigint NOT NULL, received_at timestamptz NOT NULL);
		INSERT INTO tail_by_number VALUES
			('99', 1, '2026-11-03 10:00:00+00'), ('99', 2, '2026-11-03 11:00:00+00'),
			('100', 3, '2026-11-03 12:00:00+00'), ('100', 4, '2026-11-03 13:00:00+00')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE tail_by_number`) })

	at, err := pgevents.Tail(t.Context(), pool, "tail_by_number")
	if err != nil || at != (events.Cursor{XID: 100, Seq: 4}) {
		t.Errorf("tail = %+v, %v, want the last row, transaction 100, seq 4", at, err)
	}
	for name, tc := range map[string]struct {
		from time.Time
		want events.Cursor
		ok   bool
	}{
		"the first row":                {time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), events.Cursor{XID: 99, Seq: 0}, true},
		"a row of transaction 99":      {time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC), events.Cursor{XID: 99, Seq: 1}, true},
		"the first of transaction 100": {time.Date(2026, 11, 3, 11, 30, 0, 0, time.UTC), events.Cursor{XID: 100, Seq: 2}, true},
		"the last row":                 {time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC), events.Cursor{XID: 100, Seq: 3}, true},
		"after every row":              {time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC), events.Cursor{}, false},
	} {
		got, ok, err := pgevents.Before(t.Context(), pool, "tail_by_number", pgevents.Scope{}, tc.from)
		if err != nil || ok != tc.ok || got != tc.want {
			t.Errorf("before %s = %+v, %v, %v, want %+v, %v", name, got, ok, err, tc.want, tc.ok)
		}
	}
}
