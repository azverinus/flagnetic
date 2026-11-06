package events

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

// Batch is the rows of a raw event table past one cursor up to and including
// another. Every one of them belongs to a transaction that had ended when the
// batch was chosen, so each statement of the unit of work reading the range
// finds the same rows.
type Batch struct {
	From, To events.Cursor
	// Rows is how many rows the range holds.
	Rows int
}

// Where is the condition selecting the batch's rows of the table aliased
// alias, its arguments numbered from $1.
func (b Batch) Where(alias string) (string, []any) {
	c := alias + ".xid, " + alias + ".seq"
	return `(` + c + `) > ($1::text::xid8, $2) AND (` + c + `) <= ($3::text::xid8, $4)`,
		[]any{xidText(b.From), b.From.Seq, xidText(b.To), b.To.Seq}
}

func xidText(c events.Cursor) string { return strconv.FormatUint(c.XID, 10) }

// Scope narrows a stream to the rows of one project and, when Environments
// is not empty, of those environments. A rollup reads the whole table, so
// its cursor passes every project's rows; a sink's stream reads one
// project's, and its cursor moves past the rows it will never send, so the
// scope is part of the bound and of the lag, never only of the read. The zero
// Scope is the whole table.
type Scope struct {
	Project      workspace.ProjectID
	Environments []workspace.EnvironmentID
}

// Where is the condition selecting the rows of the table aliased alias, empty
// for it itself, that are in the scope, its arguments numbered from first;
// it is empty for the whole table.
func (sc Scope) Where(alias string, first int) (string, []any) {
	if sc.Project.IsZero() {
		return "", nil
	}
	if alias != "" {
		alias += "."
	}
	cond, args := alias+"project_id = $"+strconv.Itoa(first), []any{sc.Project.Bytes()}
	if len(sc.Environments) > 0 {
		ids := make([][16]byte, len(sc.Environments))
		for i, e := range sc.Environments {
			ids[i] = e.Bytes()
		}
		cond += " AND " + alias + "environment_id = ANY($" + strconv.Itoa(first+1) + "::uuid[])"
		args = append(args, ids)
	}
	return cond, args
}

// Bound returns the next batch of at most limit rows of table in scope past
// from, of transactions below the snapshot horizon: every transaction with a smaller ID
// has ended, and every later one gets a larger ID, so no row can commit
// behind the batch's end however long another transaction stays open. An
// insert's transaction ID is taken when it first writes, so an ingest that
// began before the horizon but writes after it still lands past every cursor.
//
// A batch with fewer than limit rows holds every row in scope below the
// horizon, so it ends at the table's last row there, in scope or not: a
// stream of one project passes the other projects' rows then, even with no
// row of its own to read, rather than scanning them again on every call.
func Bound(ctx context.Context, db pgxtx.DB, table string, scope Scope, from events.Cursor, limit int) (Batch, error) {
	return BoundUntil(ctx, db, table, scope, from, nil, limit)
}

// BoundUntil is Bound for a stream that must not read past until, when it is
// not nil: a batch ends at or before it, and one short of limit ends at the
// last row up to it, which is until itself when it names a row of the table.
func BoundUntil(ctx context.Context, db pgxtx.DB, table string, scope Scope, from events.Cursor, until *events.Cursor, limit int) (Batch, error) {
	b := Batch{From: from, To: from}
	where, scoped := scope.Where("", 4)
	if where != "" {
		where = " AND " + where
	}
	args := append([]any{xidText(from), from.Seq, limit}, scoped...)
	var capped string
	if until != nil {
		capped = " AND (xid, seq) <= ($" + strconv.Itoa(len(args)+1) + "::text::xid8, $" + strconv.Itoa(len(args)+2) + ")"
		args = append(args, xidText(*until), until.Seq)
	}
	// The end is chosen by the xid8 column, never by its text: "10" sorts
	// before "9" as text, and a cursor stopped short would read rows again.
	// Both parts read one statement's snapshot, so they share its horizon: no
	// row in scope can fall between the last one counted and the table's last.
	var (
		toXID, endXID *string
		toSeq, endSeq *int64
	)
	tbl := pgx.Identifier{table}.Sanitize()
	err := db.QueryRow(ctx, `
		WITH b AS (
			SELECT xid, seq FROM `+tbl+`
			WHERE (xid, seq) > ($1::text::xid8, $2) AND xid < pg_snapshot_xmin(pg_current_snapshot())`+where+capped+`
			ORDER BY xid, seq LIMIT $3),
		e AS (
			SELECT xid, seq FROM `+tbl+`
			WHERE (xid, seq) > ($1::text::xid8, $2) AND xid < pg_snapshot_xmin(pg_current_snapshot())`+capped+`
			ORDER BY xid DESC, seq DESC LIMIT 1)
		SELECT count(*), (SELECT b.xid::text FROM b ORDER BY b.xid DESC, b.seq DESC LIMIT 1),
			(SELECT b.seq FROM b ORDER BY b.xid DESC, b.seq DESC LIMIT 1),
			(SELECT e.xid::text FROM e), (SELECT e.seq FROM e)
		FROM b`, args...).Scan(&b.Rows, &toXID, &toSeq, &endXID, &endSeq)
	if err != nil {
		return Batch{}, fmt.Errorf("choosing a batch of %s: %w", table, err)
	}
	if b.Rows < limit {
		toXID, toSeq = endXID, endSeq
	}
	if toXID == nil {
		return b, nil
	}
	if b.To, err = cursorOf(*toXID, *toSeq); err != nil {
		return Batch{}, err
	}
	return b, nil
}

func cursorOf(xid string, seq int64) (events.Cursor, error) {
	x, err := strconv.ParseUint(xid, 10, 64)
	if err != nil {
		return events.Cursor{}, fmt.Errorf("reading a transaction ID: %w", err)
	}
	return events.Cursor{XID: x, Seq: seq}, nil
}

// LockStream takes the lock of stream until the unit of work ctx carries
// ends, waiting for the replica holding it: one replica at a time moves a
// stream, so no row is counted twice. The schema is part of the lock, so
// instances sharing a database in separate schemas never wait on each other.
func (s *Store) LockStream(ctx context.Context, stream events.Stream) error {
	if _, err := s.db(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('rollup:' || $1 || '@' || current_schema(), 0))`,
		string(stream)); err != nil {
		return fmt.Errorf("locking the stream %s: %w", stream, err)
	}
	return nil
}

// Cursor returns where stream has read to: the zero cursor before it first
// moved.
func (s *Store) Cursor(ctx context.Context, stream events.Stream) (events.Cursor, error) {
	var (
		xid string
		seq int64
	)
	err := s.db(ctx).QueryRow(ctx, `SELECT xid::text, seq FROM rollup_cursors WHERE stream = $1`, string(stream)).Scan(&xid, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return events.Cursor{}, nil
	}
	if err != nil {
		return events.Cursor{}, fmt.Errorf("reading the cursor of %s: %w", stream, err)
	}
	return cursorOf(xid, seq)
}

// MoveCursor moves stream's cursor to to, as of at.
func (s *Store) MoveCursor(ctx context.Context, stream events.Stream, to events.Cursor, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO rollup_cursors (stream, xid, seq, advanced_at) VALUES ($1, $2::text::xid8, $3, $4)
		ON CONFLICT (stream) DO UPDATE SET xid = excluded.xid, seq = excluded.seq, advanced_at = excluded.advanced_at`,
		string(stream), xidText(to), to.Seq, at)
	if err != nil {
		return fmt.Errorf("moving the cursor of %s: %w", stream, err)
	}
	return nil
}

// CursorMovedAt returns when stream's cursor last moved; ok is false before
// it first did.
func (s *Store) CursorMovedAt(ctx context.Context, stream events.Stream) (at time.Time, ok bool, err error) {
	err = s.db(ctx).QueryRow(ctx, `SELECT advanced_at FROM rollup_cursors WHERE stream = $1`, string(stream)).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the cursor of %s: %w", stream, err)
	}
	return at.UTC(), true, nil
}

// streamTables are the raw event tables each stream reads.
var streamTables = map[events.Stream]string{
	events.StreamExposures:           "exposure_events",
	events.StreamEvaluationSummaries: "evaluation_summary_events",
	events.StreamAliases:             "alias_events",
}

// OldestUnread returns when the first row past stream's cursor was received,
// held back by the horizon or not; ok is false when the stream has read every
// committed row. Reading past the horizon shows how far a long transaction
// anywhere in the database holds the stream back.
func (s *Store) OldestUnread(ctx context.Context, stream events.Stream) (at time.Time, ok bool, err error) {
	table, known := streamTables[stream]
	if !known {
		return time.Time{}, false, fmt.Errorf("no raw event table for the stream %s", stream)
	}
	from, err := s.Cursor(ctx, stream)
	if err != nil {
		return time.Time{}, false, err
	}
	return OldestAfter(ctx, s.db(ctx), table, Scope{}, from)
}

// OldestAfter returns when the first row of table in scope past from was
// received, held back by the horizon or not; ok is false when there is none.
func OldestAfter(ctx context.Context, db pgxtx.DB, table string, scope Scope, from events.Cursor) (at time.Time, ok bool, err error) {
	where, scoped := scope.Where("", 3)
	if where != "" {
		where = " AND " + where
	}
	err = db.QueryRow(ctx, `SELECT received_at FROM `+pgx.Identifier{table}.Sanitize()+`
		WHERE (xid, seq) > ($1::text::xid8, $2)`+where+` ORDER BY xid, seq LIMIT 1`,
		append([]any{xidText(from), from.Seq}, scoped...)...).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the oldest row of %s not read: %w", table, err)
	}
	return at.UTC(), true, nil
}

// Tail returns the position of the last row of table below the snapshot
// horizon, the zero position when there is none: where a stream that is to
// read what happens from now on begins.
func Tail(ctx context.Context, db pgxtx.DB, table string) (events.Cursor, error) {
	var xid *string
	var seq *int64
	// The column is named for its text apart from its own: an output column
	// called xid would be what ORDER BY xid sorts by, as text, where "999"
	// comes after "1000".
	err := db.QueryRow(ctx, `SELECT xid::text AS xid_text, seq FROM `+pgx.Identifier{table}.Sanitize()+`
		WHERE xid < pg_snapshot_xmin(pg_current_snapshot()) ORDER BY xid DESC, seq DESC LIMIT 1`).Scan(&xid, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return events.Cursor{}, nil
	}
	if err != nil {
		return events.Cursor{}, fmt.Errorf("reading the last row of %s: %w", table, err)
	}
	return cursorOf(*xid, *seq)
}

// Before returns the position just before the first row of table in scope
// received at or after from, in the order a stream reads them; ok is false
// when there is none. A row's sequence number is its own, so the position
// one before it names no other row, and a read past it starts at the row.
func Before(ctx context.Context, db pgxtx.DB, table string, scope Scope, from time.Time) (at events.Cursor, ok bool, err error) {
	where, scoped := scope.Where("", 2)
	if where != "" {
		where = " AND " + where
	}
	var xid string
	var seq int64
	err = db.QueryRow(ctx, `SELECT xid::text AS xid_text, seq FROM `+pgx.Identifier{table}.Sanitize()+`
		WHERE received_at >= $1`+where+` ORDER BY xid, seq LIMIT 1`, append([]any{from}, scoped...)...).Scan(&xid, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return events.Cursor{}, false, nil
	}
	if err != nil {
		return events.Cursor{}, false, fmt.Errorf("finding the first row of %s received at %s: %w", table, from.Format(time.RFC3339), err)
	}
	if at, err = cursorOf(xid, seq-1); err != nil {
		return events.Cursor{}, false, err
	}
	return at, true, nil
}

// Last returns the position of the last row of table in scope received at or
// before to, of transactions below the snapshot horizon, in the order a stream
// reads them; ok is false when there is none.
func Last(ctx context.Context, db pgxtx.DB, table string, scope Scope, to time.Time) (at events.Cursor, ok bool, err error) {
	where, scoped := scope.Where("", 2)
	if where != "" {
		where = " AND " + where
	}
	var xid string
	var seq int64
	// The column is named for its text apart from its own, as Tail's is.
	err = db.QueryRow(ctx, `SELECT xid::text AS xid_text, seq FROM `+pgx.Identifier{table}.Sanitize()+`
		WHERE received_at <= $1 AND xid < pg_snapshot_xmin(pg_current_snapshot())`+where+`
		ORDER BY xid DESC, seq DESC LIMIT 1`, append([]any{to}, scoped...)...).Scan(&xid, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return events.Cursor{}, false, nil
	}
	if err != nil {
		return events.Cursor{}, false, fmt.Errorf("finding the last row of %s received at %s: %w", table, to.Format(time.RFC3339), err)
	}
	if at, err = cursorOf(xid, seq); err != nil {
		return events.Cursor{}, false, err
	}
	return at, true, nil
}

// ExposuresReadThrough reports whether the exposures stream has read every
// exposure received at or before at: the oldest one it has not read, when any,
// was received after.
func (s *Store) ExposuresReadThrough(ctx context.Context, at time.Time) (bool, error) {
	oldest, unread, err := s.OldestUnread(ctx, events.StreamExposures)
	if err != nil {
		return false, err
	}
	return !unread || oldest.After(at), nil
}
