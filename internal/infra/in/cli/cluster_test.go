package cli_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/testkit"
)

// A database restored from a logical dump was last used on another
// PostgreSQL cluster: a starting replica adopts this one before it accepts
// an event, and says so, while a database of this cluster is left alone.
func TestServeAdoptsADatabaseFromAnotherCluster(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	const msg = "the database came from another PostgreSQL cluster: event streams go on where they stopped"
	untouched := serve(t, map[string]string{"DATABASE_URL": url})
	if code, _ := untouched.stop(); code != 0 || untouched.findLog(msg) != nil {
		t.Fatalf("a database of this cluster: exit %d:\n%s", code, untouched.stderr)
	}

	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), `UPDATE event_cluster SET system_identifier = system_identifier + 1`); err != nil {
		t.Fatal(err)
	}
	restored := serve(t, map[string]string{"DATABASE_URL": url})
	if code, _ := restored.stop(); code != 0 || restored.findLog(msg) == nil {
		t.Fatalf("a database of another cluster: exit %d, no record of adopting it:\n%s", code, restored.stderr)
	}
	again := serve(t, map[string]string{"DATABASE_URL": url})
	if code, _ := again.stop(); code != 0 || again.findLog(msg) != nil {
		t.Fatalf("a database already adopted: exit %d:\n%s", code, again.stderr)
	}
}
