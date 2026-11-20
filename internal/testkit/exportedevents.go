package testkit

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
)

// What follows reads the events an ingest stored the way a sink's delivery
// reads them: through the events store's own batch read, in the order the
// rows were committed, as the rows are exported. A test asserts on the
// stored events through it and never on the tables behind.

// exported returns every row of the stream of kind of environment in
// project, in commit order.
func exported[R sinks.Row](t testing.TB, pool *pgxpool.Pool, kind sinks.EventKind, project workspace.ProjectID,
	environment workspace.EnvironmentID, wait bool,
) []R {
	t.Helper()
	if wait {
		waitForEventHorizon(t, pool)
	}
	store := pgsinks.NewStore(pool)
	scope := sinks.Scope{Project: project, Environments: []workspace.EnvironmentID{environment}}
	batch, err := store.NextBatch(t.Context(), kind, scope, events.Cursor{}, 1_000_000, nil)
	if err != nil {
		t.Fatalf("reading the stored %s rows: %v", kind, err)
	}
	if batch.Rows == 0 {
		return nil
	}
	page, err := store.ReadBatch(t.Context(), kind, scope, batch)
	if err != nil {
		t.Fatalf("reading the stored %s rows: %v", kind, err)
	}
	out := make([]R, len(page.Rows))
	for i, r := range page.Rows {
		out[i] = r.(R)
	}
	return out
}

// ExportedEvaluationSummaries returns the evaluation summary rows stored for
// environment, in the order they were committed.
func ExportedEvaluationSummaries(t testing.TB, pool *pgxpool.Pool, project workspace.ProjectID, environment workspace.EnvironmentID) []sinks.SummaryRow {
	t.Helper()
	return exported[sinks.SummaryRow](t, pool, sinks.EventEvaluationSummary, project, environment, true)
}

// ExportedAliases returns the aliases stored for environment, in the order
// they were committed.
func ExportedAliases(t testing.TB, pool *pgxpool.Pool, project workspace.ProjectID, environment workspace.EnvironmentID) []sinks.AliasRow {
	t.Helper()
	return exported[sinks.AliasRow](t, pool, sinks.EventAlias, project, environment, true)
}

// ExportedExposures returns the exposures stored for environment, in the
// order they were committed.
func ExportedExposures(t testing.TB, pool *pgxpool.Pool, project workspace.ProjectID, environment workspace.EnvironmentID) []sinks.ExposureRow {
	t.Helper()
	return exported[sinks.ExposureRow](t, pool, sinks.EventExposure, project, environment, true)
}

// waitForEventHorizon returns once every transaction that was running on the
// database when it was called has ended. A stream reads only the rows of
// transactions below that horizon, which any open transaction of the cluster
// holds back, one of another package's tests on the same server included; a
// row a test just saw committed is not read before then.
func waitForEventHorizon(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	var target string
	if err := pool.QueryRow(t.Context(), `SELECT pg_snapshot_xmax(pg_current_snapshot())::text`).Scan(&target); err != nil {
		t.Fatalf("reading the transaction high-water mark: %v", err)
	}
	if !Eventually(60*time.Second, func() bool {
		var passed bool
		if err := pool.QueryRow(t.Context(), `SELECT pg_snapshot_xmin(pg_current_snapshot()) >= $1::text::xid8`, target).Scan(&passed); err != nil {
			t.Fatalf("reading the snapshot horizon: %v", err)
		}
		return passed
	}) {
		t.Fatal("a transaction open on the server holds the event horizon back")
	}
}

// ExportedAliasesBelowHorizon is ExportedAliases without waiting for the
// event horizon: for a test that holds a transaction open itself, which holds
// the horizon back, and reads the rows older than it.
func ExportedAliasesBelowHorizon(t testing.TB, pool *pgxpool.Pool, project workspace.ProjectID, environment workspace.EnvironmentID) []sinks.AliasRow {
	t.Helper()
	return exported[sinks.AliasRow](t, pool, sinks.EventAlias, project, environment, false)
}
