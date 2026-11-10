package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// NewStrandedCursorsGauge returns the gauge of how many event streams a
// replica found stranded when it started, registered on reg.
func NewStrandedCursorsGauge(reg prometheus.Registerer) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metrics.Namespace, Name: "event_cursors_stranded",
		Help: "Event streams, rollups and sinks, whose cursor was ahead of every transaction of this PostgreSQL cluster " +
			"when the replica started, as after restoring a logical dump: they read no new event.",
	})
	reg.MustRegister(g)
	return g
}

// StrandedCursors returns the streams of raw events, rollups and sinks,
// whose cursor is ahead of every transaction this cluster has begun.
//
// A cursor names the transaction of the last row its stream read, and a
// stream reads the rows of later transactions that have ended. Within one
// cluster a cursor is therefore always behind the next transaction. A
// database restored from a logical dump keeps the cursors while the new
// cluster numbers its transactions from the start again: every new event
// then lies behind the cursors, so rollups count nothing and sinks deliver
// nothing until the cluster has begun as many transactions as the one the
// dump was taken from, and nothing else shows it, since the lag is measured
// with the same cursors. A physical copy keeps the counter and strands
// nothing.
func StrandedCursors(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		WITH cluster AS (SELECT pg_snapshot_xmax(pg_current_snapshot()) AS next)
		SELECT 'rollup ' || stream FROM rollup_cursors, cluster WHERE xid >= cluster.next
		UNION ALL
		SELECT 'sink ' || sink_id::text || ' ' || event_kind FROM sink_streams, cluster WHERE cursor_xid >= cluster.next
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("looking for stranded event cursors: %w", err)
	}
	stranded, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("looking for stranded event cursors: %w", err)
	}
	return stranded, nil
}
