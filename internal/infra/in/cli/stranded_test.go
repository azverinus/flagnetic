package cli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/events"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A database restored from a logical dump keeps the cursors of the cluster
// the dump came from, far ahead of the new cluster's transactions: rollups
// and sinks then read no new event and nothing else shows it, so a starting
// replica says so.
func TestServeSaysWhichEventStreamsHoldAnotherClustersCursors(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	const msg = "event streams hold cursors of another PostgreSQL cluster and read no new event"
	untouched := serve(t, map[string]string{"DATABASE_URL": url})
	if code, _ := untouched.stop(); code != 0 || untouched.findLog(msg) != nil {
		t.Fatalf("a database of this cluster: exit %d:\n%s", code, untouched.stderr)
	}

	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pgevents.NewStore(pool).MoveCursor(t.Context(), events.Stream("exposures"), events.Cursor{XID: 1 << 60, Seq: 9}, testkit.Start); err != nil {
		t.Fatal(err)
	}
	restored := serve(t, map[string]string{"DATABASE_URL": url, "FLAGNETIC_METRICS_LISTEN_ADDR": "127.0.0.1:0"})
	rec := restored.findLog(msg)
	if rec == nil {
		t.Fatalf("the replica says nothing of the stranded cursor:\n%s", restored.stderr)
	}
	streams, _ := rec["streams"].([]any)
	if rec["level"] != "ERROR" || !slices.Contains(streams, any("rollup exposures")) {
		t.Errorf("the record = %v, want an error naming the rollup", rec)
	}
	listening := restored.findLog("metrics listening")
	if listening == nil {
		t.Fatalf("no metrics listener:\n%s", restored.stderr)
	}
	if _, body := getStatus(t, "http://"+listening["addr"].(string)+"/metrics"); !strings.Contains(body, "flagnetic_event_cursors_stranded 1") {
		t.Errorf("the metrics do not count the stranded cursor:\n%s", body)
	}
	// Flags are served as ever: the replica started and stops cleanly.
	if code, _ := restored.stop(); code != 0 {
		t.Errorf("exit %d:\n%s", code, restored.stderr)
	}
}
