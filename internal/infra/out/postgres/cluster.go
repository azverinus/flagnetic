package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdoptCluster makes the events in the database follow the transactions of
// the PostgreSQL cluster the server now runs on, and reports whether it had
// to.
//
// A cursor names the transaction of the last row its stream read, and a
// stream reads rows of later transactions. Within one cluster a cursor is
// always behind the next transaction. A database restored from a logical dump
// or moved by logical replication keeps its rows and cursors while the new
// cluster numbers its transactions from the start, so every new row would lie
// behind the cursors and nothing would read it. When the database was last
// used on another cluster (its system identifier differs), the offset added to
// the transaction IDs of new rows is raised so that they all come after every
// ID stored, and the identifier is recorded; rows and cursors stay as they
// were, and the order of every stream is kept. A physical copy keeps the
// identifier and the counter, and nothing happens.
//
// It must run before the replica accepts events: a row written under the old
// offset would be lost behind the cursors.
func AdoptCluster(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Replicas starting together take turns; the schema is part of the lock so
	// instances sharing a database in separate schemas never wait on each other.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('event_cluster@' || current_schema(), 0))`); err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	// Everything that holds a transaction ID of the old cluster: the rows, the
	// cursors of rollups and sinks, and the records of what retention dropped
	// or a sink set aside. A greater ID anywhere would otherwise lie ahead of
	// the new rows.
	tag, err := tx.Exec(ctx, `
		WITH ours AS (
			SELECT c.system_identifier, c.xid_offset, now_cluster.system_identifier AS current, now_cluster.next
			FROM event_cluster c, (
				SELECT s.system_identifier, pg_snapshot_xmax(pg_current_snapshot())::text::bigint AS next
				FROM pg_control_system() s) now_cluster
			WHERE c.system_identifier <> now_cluster.system_identifier),
		stored AS (
			SELECT max(x) AS greatest FROM (
				SELECT max(xid::text::bigint) AS x FROM exposure_events
				UNION ALL SELECT max(xid::text::bigint) FROM evaluation_summary_events
				UNION ALL SELECT max(xid::text::bigint) FROM alias_events
				UNION ALL SELECT max(xid::text::bigint) FROM rollup_cursors
				UNION ALL SELECT max(greatest(cursor_xid, inflight_xid, pending_cursor_xid, replay_stop_xid, replay_resume_xid)::text::bigint) FROM sink_streams
				UNION ALL SELECT max(to_xid::text::bigint) FROM event_gaps
				UNION ALL SELECT max(to_xid::text::bigint) FROM sink_dead_letters) all_of_them)
		UPDATE event_cluster SET system_identifier = ours.current,
			xid_offset = greatest(event_cluster.xid_offset, coalesce(stored.greatest, 0) + 1 - ours.next, 0)
		FROM ours, stored`)
	if err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
