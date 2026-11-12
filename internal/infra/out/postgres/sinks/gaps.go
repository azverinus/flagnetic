package sinks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// streamLockTimeout bounds how long retention waits for a stream a delivery is
// writing. It is longer than the wait for a partition: the row is held for the
// length of one statement, and failing the round for it would leave the
// partition, and every sink behind it, undropped until the next.
const streamLockTimeout = "30s"

// LockStreams holds every stream of kind, disabled and stopped sinks' included,
// until the unit of work ends, in the order of their sinks, so that two
// holders never wait on each other.
//
// The streams' projects and sinks are held first, against being deleted or
// locked for update, in the order deleting a project or a sink, or resuming
// one, takes them: the gap and the sink.data_lost recorded later reference
// both, and taking that reference only after the streams would wait on a
// transaction that waits on a stream, a deadlock, while holding the
// partitions every ingest needs.
func (s *Store) LockStreams(ctx context.Context, kind sinks.EventKind) error {
	db := s.db(ctx)
	if _, err := db.Exec(ctx, `SET LOCAL lock_timeout = '`+streamLockTimeout+`'`); err != nil {
		return fmt.Errorf("locking the sink streams of %s: %w", kind, err)
	}
	for _, q := range []string{
		`SELECT 1 FROM projects WHERE id IN (SELECT s.project_id FROM sinks s JOIN sink_streams ss ON ss.sink_id = s.id
			WHERE ss.event_kind = $1) ORDER BY id FOR KEY SHARE`,
		`SELECT 1 FROM sinks WHERE id IN (SELECT sink_id FROM sink_streams WHERE event_kind = $1) ORDER BY id FOR KEY SHARE`,
		`SELECT 1 FROM sink_streams WHERE event_kind = $1 ORDER BY sink_id FOR UPDATE`,
	} {
		if _, err := db.Exec(ctx, q, string(kind)); err != nil {
			return fmt.Errorf("locking the sink streams of %s: %w", kind, err)
		}
	}
	return nil
}

// GapStreams returns every stream of kind with its sink's project, name, kind
// and environments, the streams a drop of kind's rows concerns.
func (s *Store) GapStreams(ctx context.Context, kind sinks.EventKind) ([]appsinks.GapStream, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+streamColumns+` FROM sink_streams ss WHERE ss.event_kind = $1 ORDER BY ss.sink_id`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("reading the sink streams of %s: %w", kind, err)
	}
	streams, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (streamRow, error) { return scanStream(row) })
	if err != nil {
		return nil, fmt.Errorf("reading the sink streams of %s: %w", kind, err)
	}
	owners, err := s.db(ctx).Query(ctx, `SELECT s.id, s.project_id, s.name, s.kind, s.environments FROM sinks s
		WHERE EXISTS (SELECT 1 FROM sink_streams ss WHERE ss.sink_id = s.id AND ss.event_kind = $1)`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("reading the sinks of %s: %w", kind, err)
	}
	list, err := pgx.CollectRows(owners, func(row pgx.CollectableRow) (sinks.Sink, error) {
		var (
			sk, project [16]byte
			v           sinks.Sink
			sinkKind    string
		)
		if scanErr := row.Scan(&sk, &project, &v.Name, &sinkKind, &v.Environments); scanErr != nil {
			return v, scanErr
		}
		v.ID, v.Project, v.Kind = id.FromBytes[sinks.Sink](sk), id.FromBytes[workspace.Project](project), sinks.Kind(sinkKind)
		return v, nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the sinks of %s: %w", kind, err)
	}
	by := make(map[sinks.ID]sinks.Sink, len(list))
	for _, v := range list {
		by[v.ID] = v
	}
	out := make([]appsinks.GapStream, 0, len(streams))
	for _, r := range streams {
		out = append(out, appsinks.GapStream{Sink: by[r.sink], Stream: r.stream})
	}
	return out, nil
}

// Lost returns what the partitions of days of kind's raw table hold past from
// in scope.
func (s *Store) Lost(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, days []time.Time, from events.Cursor) (events.Lost, error) {
	table, err := rawTable(kind)
	if err != nil {
		return events.Lost{}, err
	}
	return pgevents.LostIn(ctx, s.db(ctx), table, pgScope(scope), days, from)
}

// FirstStored returns the first row of kind in scope past from outside the
// partitions of days, nil when there is none.
func (s *Store) FirstStored(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, days []time.Time, from events.Cursor) (*events.Cursor, error) {
	table, err := rawTable(kind)
	if err != nil {
		return nil, err
	}
	return pgevents.FirstStoredIn(ctx, s.db(ctx), table, pgScope(scope), days, from)
}

// RecordGap saves gap as the gap of sink's stream of kind and moves the
// stream past it: the live position to gap.Moved, which is the cursor, or, while a
// bounded replay runs, the position the replay returns to; the batch in
// flight and the lease are dropped, so that a send under way records nothing
// over the move and the next batch is chosen anew under a new delivery ID.
func (s *Store) RecordGap(ctx context.Context, sink sinks.ID, kind sinks.EventKind, replaying bool, gap events.Gap) error {
	db := s.db(ctx)
	if _, err := db.Exec(ctx, `INSERT INTO event_gaps (id, stream, event_type, from_xid, from_seq, to_xid, to_seq,
			period_start, period_end, rows, dropped_at, sink_id)
		VALUES ($1, $2, $3, $4::text::xid8, $5, $6::text::xid8, $7, $8, $9, $10, $11, $12)`,
		gap.ID.Bytes(), string(gap.Stream), string(gap.EventType), xid(gap.From), gap.From.Seq, xid(gap.To), gap.To.Seq,
		gap.PeriodStart, gap.PeriodEnd, gap.Rows, gap.DroppedAt, sink.Bytes()); err != nil {
		return fmt.Errorf("recording the gap of a sink: %w", err)
	}
	position := "cursor"
	if replaying {
		position = "replay_resume"
	}
	if _, err := db.Exec(ctx, `
		UPDATE sink_streams SET
		    `+position+`_xid = $3::text::xid8, `+position+`_seq = $4,
		    inflight_xid = NULL, inflight_seq = NULL, inflight_environments = NULL, inflight_rejections = 0,
		    lease_token = NULL, lease_until = NULL
		WHERE sink_id = $1 AND event_kind = $2`, sink.Bytes(), string(kind), xid(gap.Moved), gap.Moved.Seq); err != nil {
		return fmt.Errorf("moving a stream past a gap: %w", err)
	}
	return nil
}

// ExpireDeadLetters expires the pending dead letters of kind none of whose
// rows is stored outside the partitions ending at before, which are about to
// go: nothing is left to redeliver.
func (s *Store) ExpireDeadLetters(ctx context.Context, kind sinks.EventKind, before time.Time) error {
	table, err := rawTable(kind)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		UPDATE sink_dead_letters d SET status = 'expired'
		FROM sinks s
		WHERE s.id = d.sink_id AND d.event_kind = $1 AND d.status = 'pending'
		  AND NOT EXISTS (
		      SELECT 1 FROM `+pgx.Identifier{table}.Sanitize()+` r
		      WHERE r.received_at >= $2 AND (r.xid, r.seq) > (d.from_xid, d.from_seq) AND (r.xid, r.seq) <= (d.to_xid, d.to_seq)
		        AND r.project_id = s.project_id
		        AND (cardinality(d.environments) = 0 OR r.environment_id = ANY(d.environments))
		        AND (d.received_floor IS NULL OR r.received_at >= d.received_floor))`, string(kind), before)
	if err != nil {
		return fmt.Errorf("expiring dead letters: %w", err)
	}
	return nil
}

const gapColumns = `g.id, g.sink_id, g.event_type, g.rows, g.period_start, g.period_end, g.dropped_at, g.dismissed_at`

func scanGap(row pgx.Row) (sinks.Gap, error) {
	var (
		g                   sinks.Gap
		gapID, sk           [16]byte
		kind                string
		start, end, dropped time.Time
		dismissed           *time.Time
	)
	if err := row.Scan(&gapID, &sk, &kind, &g.Rows, &start, &end, &dropped, &dismissed); err != nil {
		return g, err
	}
	g.ID, g.Sink, g.EventKind = id.FromBytes[events.Gap](gapID), id.FromBytes[sinks.Sink](sk), sinks.EventKind(kind)
	g.PeriodStart, g.PeriodEnd, g.DroppedAt, g.DismissedAt = start.UTC(), end.UTC(), dropped.UTC(), utc(dismissed)
	return g, nil
}

// Gaps returns sink's gaps, the oldest first, dismissed ones included.
func (s *Store) Gaps(ctx context.Context, sink sinks.ID) ([]sinks.Gap, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+gapColumns+` FROM event_gaps g WHERE g.sink_id = $1
		ORDER BY g.period_start, g.id`, sink.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing gaps: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (sinks.Gap, error) { return scanGap(row) })
	if err != nil {
		return nil, fmt.Errorf("reading gaps: %w", err)
	}
	return out, nil
}

// Dismiss marks sink's gap dismissed at now, keeping the first time of a gap
// dismissed already, and returns the gap as it is; first says whether this
// call was the one that dismissed it. appsinks.ErrNotFound when the sink has
// no such gap.
func (s *Store) Dismiss(ctx context.Context, sink sinks.ID, gap sinks.GapID, now time.Time) (g sinks.Gap, first bool, err error) {
	row := s.db(ctx).QueryRow(ctx, `
		UPDATE event_gaps g SET dismissed_at = coalesce(g.dismissed_at, $3)
		FROM (SELECT id, dismissed_at FROM event_gaps WHERE sink_id = $1 AND id = $2 FOR UPDATE) prev
		WHERE g.id = prev.id
		RETURNING `+gapColumns+`, prev.dismissed_at IS NULL`, sink.Bytes(), gap.Bytes(), now)
	var gapID, sk [16]byte
	var (
		kind                string
		start, end, dropped time.Time
		dismissed           *time.Time
	)
	err = row.Scan(&gapID, &sk, &kind, &g.Rows, &start, &end, &dropped, &dismissed, &first)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, false, appsinks.ErrNotFound
	}
	if err != nil {
		return g, false, fmt.Errorf("dismissing a gap: %w", err)
	}
	g.ID, g.Sink, g.EventKind = id.FromBytes[events.Gap](gapID), id.FromBytes[sinks.Sink](sk), sinks.EventKind(kind)
	g.PeriodStart, g.PeriodEnd, g.DroppedAt, g.DismissedAt = start.UTC(), end.UTC(), dropped.UTC(), utc(dismissed)
	return g, first, nil
}

// withGaps sets how many gaps not yet dismissed each of list's sinks has.
func (s *Store) withGaps(ctx context.Context, list []sinks.Sink) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([][16]byte, len(list))
	for i, v := range list {
		ids[i] = v.ID.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT sink_id, count(*) FROM event_gaps WHERE dismissed_at IS NULL AND sink_id = ANY($1::uuid[]) GROUP BY sink_id`, ids)
	if err != nil {
		return fmt.Errorf("counting gaps: %w", err)
	}
	defer rows.Close()
	counts := map[sinks.ID]int{}
	for rows.Next() {
		var sk [16]byte
		var n int
		if err = rows.Scan(&sk, &n); err != nil {
			return fmt.Errorf("reading a count of gaps: %w", err)
		}
		counts[id.FromBytes[sinks.Sink](sk)] = n
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range list {
		list[i].Gaps = counts[list[i].ID]
	}
	return nil
}
