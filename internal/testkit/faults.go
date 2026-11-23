package testkit

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The helpers of this file, and of restore.go, are the only code of the
// test suite that runs a statement on an application table. Each one injects
// a state no API reaches, and says which, so a test that needs it names the
// fault it stands for instead of writing SQL. TestNoTestRunsAStatementOnAnApplicationTable
// keeps it so.

// SeedAuditEntries writes n entries of the instance's audit log in one
// statement, each carrying an `after` document of about payload bytes: a
// volume the API would take n changes to make, which a download test needs
// to be large. The entries are system entries of no project. The padding is
// words, not one repeated character: the audit guard looks for every secret
// the package's tests recorded, and a test that sent a password of a few
// thousand x's would otherwise find it inside the padding.
func SeedAuditEntries(t testing.TB, pool *pgxpool.Pool, n, payload int) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
INSERT INTO audit_entries (id, occurred_at, actor_kind, actor_id, actor_name, action, entity_kind, entity_id, entity_key, after)
SELECT gen_random_uuid(), $1, 'system', 'seed', 'seed', 'seed.created', 'seed', 'seed-' || g, 'seed-' || g,
       jsonb_build_object('padding', $2::text)
FROM generate_series(1, $3) AS g`, Start.Add(-time.Hour), padding(payload), n)
	if err != nil {
		t.Fatalf("seeding the audit log: %v", err)
	}
}

// padding returns n bytes of filler no secret a test records can be found in.
func padding(n int) string {
	const word = "seed padding "
	return strings.Repeat(word, n/len(word)+1)[:n]
}

// PointSCIMTokenAtProvider moves a SCIM token to another provider: a
// database writer changes what the token may do, which the sealed hash of
// the token binds and no API can change.
func PointSCIMTokenAtProvider(t testing.TB, pool *pgxpool.Pool, token, provider [16]byte) {
	t.Helper()
	exec(t, pool, "pointing a SCIM token at another provider", `UPDATE scim_tokens SET provider_id = $2 WHERE id = $1`, token, provider)
}

// MoveSCIMTokenExpiry sets the expiry of a SCIM token, which only a database
// writer can change after the token is created, and the sealed hash of the
// token binds.
func MoveSCIMTokenExpiry(t testing.TB, pool *pgxpool.Pool, token [16]byte, expires time.Time) {
	t.Helper()
	exec(t, pool, "moving the expiry of a SCIM token", `UPDATE scim_tokens SET expires_at = $2 WHERE id = $1`, token, expires)
}

// ResealSCIMTokenHash replaces the sealed hash of a SCIM token with sealed:
// a token created before the hash was bound to its provider and expiry
// holds a hash sealed to its row alone.
func ResealSCIMTokenHash(t testing.TB, pool *pgxpool.Pool, token [16]byte, sealed string) {
	t.Helper()
	exec(t, pool, "resealing the hash of a SCIM token", `UPDATE scim_tokens SET token_hash_sealed = $2 WHERE id = $1`, token, sealed)
}

// MoveConfigurationBehindChangeRequest moves the flag configuration a change
// request is based on without superseding the request, as a write committed
// while the request was being applied would: no API path moves a
// configuration and leaves its change requests approved.
func MoveConfigurationBehindChangeRequest(t testing.TB, pool *pgxpool.Pool, changeRequest [16]byte) {
	t.Helper()
	exec(t, pool, "moving a configuration behind a change request", `
		UPDATE flag_configurations SET version = version + 1
		WHERE flag_id = (SELECT flag_id FROM change_requests WHERE id = $1)
		  AND environment_id = (SELECT environment_id FROM change_requests WHERE id = $1)`, changeRequest)
}

// exec runs a fault statement, failing the test with what it was for.
func exec(t testing.TB, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// ChangeClusterIdentity makes the database look restored from another
// PostgreSQL cluster: the identifier the server recorded for the cluster it
// ran on no longer matches the cluster it runs on, which a base backup
// restored elsewhere does and no API can.
func ChangeClusterIdentity(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, "changing the recorded cluster identity", `UPDATE event_cluster SET system_identifier = system_identifier + 1`)
}

// RaiseEventTransactionOffset makes the cluster the database ran on look as
// if it had begun offset transactions before the events recorded so far.
func RaiseEventTransactionOffset(t testing.TB, pool *pgxpool.Pool, offset int64) {
	t.Helper()
	exec(t, pool, "raising the event transaction offset", `UPDATE event_cluster SET xid_offset = $1`, offset)
}

// DropClusterRecord removes what the database records of its cluster, as a
// schema made by hand, or by a version before the record, lacks it.
func DropClusterRecord(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, "dropping the cluster record", `DROP TABLE event_cluster CASCADE`)
}

// CopyTableAsALogicalRestore copies the rows of table from one cluster's
// database to another's as a logical dump and its restore do: the rows as
// they were, over those the migrations seeded, whatever the new cluster's
// transaction counter reads. No API moves a database to another cluster.
func CopyTableAsALogicalRestore(t testing.TB, from, to *pgxpool.Pool, table string) {
	t.Helper()
	name := pgx.Identifier{table}.Sanitize()
	exec(t, to, "emptying "+table+" for the restore", "TRUNCATE "+name)
	var dump bytes.Buffer
	src, err := from.Acquire(t.Context())
	if err != nil {
		t.Fatalf("dumping %s: %v", table, err)
	}
	defer src.Release()
	if _, err = src.Conn().PgConn().CopyTo(t.Context(), &dump, "COPY (SELECT * FROM "+name+") TO STDOUT"); err != nil {
		t.Fatalf("dumping %s: %v", table, err)
	}
	dst, err := to.Acquire(t.Context())
	if err != nil {
		t.Fatalf("restoring %s: %v", table, err)
	}
	defer dst.Release()
	if _, err = dst.Conn().PgConn().CopyFrom(t.Context(), &dump, "COPY "+name+" FROM STDIN"); err != nil {
		t.Fatalf("restoring %s: %v", table, err)
	}
}

// HoldEvaluationSummariesOpenForReading holds the evaluation summaries' parent
// table as a reader that stays open does, until the test ends: a long report
// query no API keeps running on demand.
func HoldEvaluationSummariesOpenForReading(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	reader, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("opening the reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Rollback(context.Background()) })
	if _, err = reader.Exec(t.Context(), `LOCK TABLE evaluation_summary_events IN ACCESS SHARE MODE`); err != nil {
		t.Fatalf("holding the evaluation summaries: %v", err)
	}
}
