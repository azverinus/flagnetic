package testkit

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedAuditEntries writes n entries of the instance's audit log in one
// statement, each carrying an `after` document of about payload bytes, so a
// test can make a download large without making n changes through the API.
// The entries are system entries of no project.
func SeedAuditEntries(t testing.TB, pool *pgxpool.Pool, n, payload int) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
INSERT INTO audit_entries (id, occurred_at, actor_kind, actor_id, actor_name, action, entity_kind, entity_id, entity_key, after)
SELECT gen_random_uuid(), $1, 'system', 'seed', 'seed', 'seed.created', 'seed', 'seed-' || g, 'seed-' || g,
       jsonb_build_object('padding', $2::text)
FROM generate_series(1, $3) AS g`, Start.Add(-time.Hour), strings.Repeat("x", payload), n)
	if err != nil {
		t.Fatalf("seeding the audit log: %v", err)
	}
}
