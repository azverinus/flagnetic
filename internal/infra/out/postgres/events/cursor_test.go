package events_test

import (
	"context"
	"os"
	"testing"

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
	b, err := pgevents.Bound(t.Context(), pool, "stream_by_number", events.Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rows != 2 || b.To != (events.Cursor{XID: 10, Seq: 2}) {
		t.Errorf("batch = %d rows to %+v, want 2 rows to transaction 10, seq 2", b.Rows, b.To)
	}
	b, err = pgevents.Bound(t.Context(), pool, "stream_by_number", b.To, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rows != 0 || b.To != (events.Cursor{XID: 10, Seq: 2}) {
		t.Errorf("the batch after = %d rows to %+v, want none, the cursor where it was", b.Rows, b.To)
	}
}
