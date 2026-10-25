package events

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// rawTables are the raw event table of each event type.
var rawTables = map[events.EventType]string{
	events.EventEvaluationSummary: "evaluation_summary_events",
	events.EventAlias:             "alias_events",
	events.EventExposure:          "exposure_events",
}

func rawTable(typ events.EventType) (string, error) {
	table, ok := rawTables[typ]
	if !ok {
		return "", fmt.Errorf("no raw table stores events of type %q", typ)
	}
	return table, nil
}

// partitionName is the name of table's partition of the UTC day of day.
func partitionName(table string, day time.Time) string {
	return table + "_p" + day.UTC().Format("20060102")
}

// LockRetention holds the retention of typ's raw table until the unit of work
// ends, waiting for the replica holding it. The schema is part of the lock,
// so instances sharing a database in separate schemas never wait on each
// other.
func (s *Store) LockRetention(ctx context.Context, typ events.EventType) error {
	if _, err := s.db(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('retention:' || $1 || '@' || current_schema(), 0))`,
		string(typ)); err != nil {
		return fmt.Errorf("locking the retention of %s: %w", typ, err)
	}
	return nil
}

// Partitions returns the UTC days of the partitions of typ's raw table,
// oldest first.
func (s *Store) Partitions(ctx context.Context, typ events.EventType) ([]time.Time, error) {
	table, err := rawTable(typ)
	if err != nil {
		return nil, err
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = $1::regclass`, pgx.Identifier{table}.Sanitize())
	if err != nil {
		return nil, fmt.Errorf("listing the partitions of %s: %w", table, err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("listing the partitions of %s: %w", table, err)
	}
	days := make([]time.Time, 0, len(names))
	for _, name := range names {
		suffix, ok := strings.CutPrefix(name, table+"_p")
		if !ok {
			continue
		}
		day, err := time.Parse("20060102", suffix)
		if err != nil {
			continue
		}
		days = append(days, day)
	}
	slices.SortFunc(days, time.Time.Compare)
	return days, nil
}

// lockTimeout bounds how long a drop waits for the partitions it takes: a
// transaction still writing one, or a long reader, holds it, and ingest
// queues behind the drop while it waits. The table is tried again at the
// next round.
const lockTimeout = "5s"

// LockPartitions takes typ's raw table, without its other partitions, and
// the partitions of days exclusively until the unit of work ends, giving up
// after lockTimeout.
func (s *Store) LockPartitions(ctx context.Context, typ events.EventType, days []time.Time) error {
	table, err := rawTable(typ)
	if err != nil {
		return err
	}
	if _, err = s.db(ctx).Exec(ctx, `SET LOCAL lock_timeout = '`+lockTimeout+`'`); err != nil {
		return fmt.Errorf("locking the partitions of %s: %w", table, err)
	}
	names := []string{"ONLY " + pgx.Identifier{table}.Sanitize()}
	for _, d := range days {
		names = append(names, "ONLY "+pgx.Identifier{partitionName(table, d)}.Sanitize())
	}
	if _, err = s.db(ctx).Exec(ctx, `LOCK TABLE `+strings.Join(names, ", ")+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("locking the partitions of %s due to go: %w", table, err)
	}
	return nil
}

// end returns when the last of days ends: the partitions of days hold every
// row received before it, since only partitions older than every other are
// dropped.
func end(days []time.Time) time.Time { return slices.MaxFunc(days, time.Time.Compare).AddDate(0, 0, 1) }

// Lost returns what the partitions of days of typ's raw table hold past from.
func (s *Store) Lost(ctx context.Context, typ events.EventType, days []time.Time, from events.Cursor) (events.Lost, error) {
	table, err := rawTable(typ)
	if err != nil {
		return events.Lost{}, err
	}
	var (
		lost                   events.Lost
		firstXID, lastXID      *string
		firstSeq, lastSeq      *int64
		periodStart, periodEnd *time.Time
	)
	err = s.db(ctx).QueryRow(ctx, `
		WITH l AS (
			SELECT xid, seq, received_at FROM `+pgx.Identifier{table}.Sanitize()+`
			WHERE received_at < $3 AND (xid, seq) > ($1::text::xid8, $2))
		SELECT count(*), min(received_at), max(received_at),
			(SELECT xid::text FROM l ORDER BY xid, seq LIMIT 1), (SELECT seq FROM l ORDER BY xid, seq LIMIT 1),
			(SELECT xid::text FROM l ORDER BY xid DESC, seq DESC LIMIT 1), (SELECT seq FROM l ORDER BY xid DESC, seq DESC LIMIT 1)
		FROM l`, xidText(from), from.Seq, end(days)).
		Scan(&lost.Rows, &periodStart, &periodEnd, &firstXID, &firstSeq, &lastXID, &lastSeq)
	if err != nil {
		return events.Lost{}, fmt.Errorf("reading the rows of %s due to go: %w", table, err)
	}
	if lost.Rows == 0 {
		return events.Lost{}, nil
	}
	if lost.First, err = cursorOf(*firstXID, *firstSeq); err != nil {
		return events.Lost{}, err
	}
	if lost.Last, err = cursorOf(*lastXID, *lastSeq); err != nil {
		return events.Lost{}, err
	}
	lost.PeriodStart, lost.PeriodEnd = periodStart.UTC(), periodEnd.UTC()
	return lost, nil
}

// FirstStoredAfter returns the first row of typ's raw table past from outside
// the partitions of days, nil when there is none.
func (s *Store) FirstStoredAfter(ctx context.Context, typ events.EventType, days []time.Time, from events.Cursor,
) (*events.Cursor, error) {
	table, err := rawTable(typ)
	if err != nil {
		return nil, err
	}
	var (
		xid string
		seq int64
	)
	err = s.db(ctx).QueryRow(ctx, `SELECT xid::text, seq FROM `+pgx.Identifier{table}.Sanitize()+`
		WHERE received_at >= $3 AND (xid, seq) > ($1::text::xid8, $2) ORDER BY xid, seq LIMIT 1`,
		xidText(from), from.Seq, end(days)).Scan(&xid, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the first row of %s kept: %w", table, err)
	}
	c, err := cursorOf(xid, seq)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Horizon returns the ID of the oldest transaction still running.
func (s *Store) Horizon(ctx context.Context) (uint64, error) {
	var x string
	if err := s.db(ctx).QueryRow(ctx, `SELECT pg_snapshot_xmin(pg_current_snapshot())::text`).Scan(&x); err != nil {
		return 0, fmt.Errorf("reading the snapshot horizon: %w", err)
	}
	c, err := cursorOf(x, 0)
	if err != nil {
		return 0, err
	}
	return c.XID, nil
}

// SaveGap records gap.
func (s *Store) SaveGap(ctx context.Context, gap events.Gap) error {
	_, err := s.db(ctx).Exec(ctx, `INSERT INTO event_gaps (id, stream, event_type, from_xid, from_seq, to_xid, to_seq,
			period_start, period_end, rows, dropped_at)
		VALUES ($1, $2, $3, $4::text::xid8, $5, $6::text::xid8, $7, $8, $9, $10, $11)`,
		gap.ID.Bytes(), string(gap.Stream), string(gap.EventType), xidText(gap.From), gap.From.Seq, xidText(gap.To), gap.To.Seq,
		gap.PeriodStart, gap.PeriodEnd, gap.Rows, gap.DroppedAt)
	if err != nil {
		return fmt.Errorf("recording the gap of %s: %w", gap.Stream, err)
	}
	return nil
}

// Gaps returns the gaps whose rows were received from from to to, in the
// order they were received.
func (s *Store) Gaps(ctx context.Context, from, to time.Time) ([]events.Gap, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT id, stream, event_type, from_xid::text, from_seq, to_xid::text, to_seq,
			period_start, period_end, rows, dropped_at
		FROM event_gaps WHERE period_start <= $2 AND period_end >= $1 ORDER BY period_start, id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("reading gaps: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.Gap, error) {
		var (
			g              events.Gap
			gapID          [16]byte
			stream, typ    string
			fromXID, toXID string
			fromSeq, toSeq int64
		)
		if scanErr := row.Scan(&gapID, &stream, &typ, &fromXID, &fromSeq, &toXID, &toSeq, &g.PeriodStart, &g.PeriodEnd, &g.Rows,
			&g.DroppedAt); scanErr != nil {
			return g, scanErr
		}
		g.ID, g.Stream, g.EventType = id.FromBytes[events.Gap](gapID), events.Stream(stream), events.EventType(typ)
		var cursorErr error
		if g.From, cursorErr = cursorOf(fromXID, fromSeq); cursorErr != nil {
			return g, cursorErr
		}
		if g.To, cursorErr = cursorOf(toXID, toSeq); cursorErr != nil {
			return g, cursorErr
		}
		g.PeriodStart, g.PeriodEnd, g.DroppedAt = g.PeriodStart.UTC(), g.PeriodEnd.UTC(), g.DroppedAt.UTC()
		return g, nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading gaps: %w", err)
	}
	return out, nil
}

// DropPartitions drops the partitions of days of typ's raw table, never
// deleting a row: a dropped partition leaves no dead rows to vacuum.
func (s *Store) DropPartitions(ctx context.Context, typ events.EventType, days []time.Time) error {
	table, err := rawTable(typ)
	if err != nil {
		return err
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = pgx.Identifier{partitionName(table, d)}.Sanitize()
	}
	if _, err = s.db(ctx).Exec(ctx, `DROP TABLE `+strings.Join(names, ", ")); err != nil {
		return fmt.Errorf("dropping partitions of %s: %w", table, err)
	}
	for _, d := range days {
		// A day dropped is created again, should a row of it ever arrive.
		s.partitions.Delete(d.UTC().Format(time.DateOnly))
	}
	return nil
}

// PurgeClaims deletes at most limit event IDs of typ received before before
// and returns how many it deleted.
func (s *Store) PurgeClaims(ctx context.Context, typ events.EventType, before time.Time, limit int) (int, error) {
	table, ok := dedupTables[typ]
	if !ok {
		return 0, fmt.Errorf("purging event IDs: no events of type %q are stored", typ)
	}
	t := pgx.Identifier{table}.Sanitize()
	tag, err := s.db(ctx).Exec(ctx, `DELETE FROM `+t+` WHERE ctid IN (SELECT ctid FROM `+t+` WHERE received_at < $1 LIMIT $2)`,
		before, limit)
	if err != nil {
		return 0, fmt.Errorf("purging the event IDs of %s: %w", table, err)
	}
	return int(tag.RowsAffected()), nil
}
