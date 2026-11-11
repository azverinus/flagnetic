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
	// Every replica asks at every start, so the common answer, this cluster's
	// own database, is found without looking at the events.
	var same bool
	if err = tx.QueryRow(ctx, `SELECT c.system_identifier = s.system_identifier
		FROM event_cluster c, pg_control_system() s`).Scan(&same); err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	if same {
		return false, nil
	}
	// event_xid_stored() is the greatest ID anything holds of the old
	// cluster; a greater one anywhere would lie ahead of the new rows.
	tag, err := tx.Exec(ctx, `
		UPDATE event_cluster SET system_identifier = s.system_identifier,
			xid_offset = greatest(event_cluster.xid_offset,
				event_xid_stored() + 1 - pg_snapshot_xmax(pg_current_snapshot())::text::bigint, 0)
		FROM pg_control_system() s`)
	if err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("adopting the cluster: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
