package sinks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func xid(c events.Cursor) string { return strconv.FormatUint(c.XID, 10) }

func cursorOf(text string, seq int64) (events.Cursor, error) {
	x, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return events.Cursor{}, fmt.Errorf("reading a transaction ID: %w", err)
	}
	return events.Cursor{XID: x, Seq: seq}, nil
}

// streamColumns are a stream's as the API and the worker read them.
const streamColumns = `ss.sink_id, ss.event_kind, ss.cursor_xid::text, ss.cursor_seq, ss.last_delivered_at,
	ss.consecutive_failures, ss.last_failure_class, ss.last_failure_status, ss.last_failure_code, ss.last_failure_at,
	ss.inflight_xid::text, ss.inflight_seq, ss.inflight_environments, ss.inflight_rejections,
	ss.replay_stop_xid::text, ss.replay_stop_seq, ss.replay_resume_xid::text, ss.replay_resume_seq`

type streamRow struct {
	sink       sinks.ID
	stream     sinks.Stream
	inflight   *appsinks.Inflight
	rejections int
}

func scanStream(row pgx.Row) (streamRow, error) {
	var (
		r                        streamRow
		sk                       [16]byte
		kind, cursorXID          string
		delivered, failedAt      *time.Time
		class, code, inflightXID *string
		failStatus               *int
		cursorSeq                int64
		inflightSeq              *int64
		environments             [][16]byte
		failures                 int
		inflightRejections       int
		stopXID, resumeXID       *string
		stopSeq, resumeSeq       *int64
	)
	if err := row.Scan(&sk, &kind, &cursorXID, &cursorSeq, &delivered, &failures, &class, &failStatus, &code, &failedAt,
		&inflightXID, &inflightSeq, &environments, &inflightRejections, &stopXID, &stopSeq, &resumeXID, &resumeSeq); err != nil {
		return r, err
	}
	r.sink = id.FromBytes[sinks.Sink](sk)
	r.stream = sinks.Stream{EventKind: sinks.EventKind(kind), LastDeliveredAt: utc(delivered), ConsecutiveFailures: failures}
	var err error
	if r.stream.Cursor, err = cursorOf(cursorXID, cursorSeq); err != nil {
		return r, err
	}
	if class != nil && failedAt != nil {
		f := sinks.FailureAt{Failure: sinks.Failure{Class: sinks.FailureClass(*class)}, At: failedAt.UTC()}
		if failStatus != nil {
			f.Status = *failStatus
		}
		if code != nil {
			f.Code = *code
		}
		r.stream.LastFailure = &f
	}
	if inflightXID != nil {
		in := appsinks.Inflight{}
		if in.To, err = cursorOf(*inflightXID, *inflightSeq); err != nil {
			return r, err
		}
		for _, e := range environments {
			in.Environments = append(in.Environments, id.FromBytes[workspace.Environment](e))
		}
		r.inflight = &in
	}
	r.rejections = inflightRejections
	if stopXID != nil {
		// The table ties each position's two columns together but not the
		// stop to the resume position; a row with one and not the other is
		// reported rather than read as a replay half set.
		if resumeXID == nil {
			return r, errors.New("reading a stream: a replay stop without a resume position")
		}
		stop, stopErr := cursorOf(*stopXID, *stopSeq)
		if stopErr != nil {
			return r, stopErr
		}
		resume, resumeErr := cursorOf(*resumeXID, *resumeSeq)
		if resumeErr != nil {
			return r, resumeErr
		}
		r.stream.Stop, r.stream.Resume = &stop, &resume
	}
	return r, nil
}

// withStreams sets the streams of each of list's sinks, in the order the API
// lists the event kinds.
func (s *Store) withStreams(ctx context.Context, list []sinks.Sink) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([][16]byte, len(list))
	for i, v := range list {
		ids[i] = v.ID.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT `+streamColumns+` FROM sink_streams ss WHERE ss.sink_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return fmt.Errorf("reading streams: %w", err)
	}
	defer rows.Close()
	by := map[sinks.ID][]sinks.Stream{}
	for rows.Next() {
		r, scanErr := scanStream(rows)
		if scanErr != nil {
			return fmt.Errorf("reading a stream: %w", scanErr)
		}
		by[r.sink] = append(by[r.sink], r.stream)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range list {
		streams := by[list[i].ID]
		slices.SortFunc(streams, func(a, b sinks.Stream) int {
			return slices.Index(sinks.EventKinds, a.EventKind) - slices.Index(sinks.EventKinds, b.EventKind)
		})
		list[i].Streams = streams
	}
	return s.withDeadLetters(ctx, list)
}

// LockStream reads the stream of kind of sink for update, holding it against
// the delivery worker's writes until the unit of work ends.
func (s *Store) LockStream(ctx context.Context, sink sinks.ID, kind sinks.EventKind) (sinks.Stream, error) {
	row := s.db(ctx).QueryRow(ctx, `SELECT `+streamColumns+` FROM sink_streams ss WHERE ss.sink_id = $1 AND ss.event_kind = $2 FOR UPDATE`,
		sink.Bytes(), string(kind))
	r, err := scanStream(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return sinks.Stream{}, appsinks.ErrNotFound
	}
	if err != nil {
		return sinks.Stream{}, fmt.Errorf("locking a stream: %w", err)
	}
	return r.stream, nil
}

// Replay puts the stream of kind of sink as plan has it. The position it reads
// on from is stored as pending and applied by the worker as it leases the
// stream; the batch in flight and the lease are dropped now, so that a send
// under way records nothing over the replay. The plan carries the position to
// return to, which the caller read from the stream under lock.
func (s *Store) Replay(ctx context.Context, sink sinks.ID, kind sinks.EventKind, plan sinks.ReplayPlan, now time.Time) error {
	var stopXID, resumeXID *string
	var stopSeq, resumeSeq *int64
	if plan.Stop != nil {
		sx, rx := xid(*plan.Stop), xid(*plan.Resume)
		stopXID, stopSeq, resumeXID, resumeSeq = &sx, &plan.Stop.Seq, &rx, &plan.Resume.Seq
	}
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sink_streams SET
		    pending_cursor_xid = $3::text::xid8, pending_cursor_seq = $4,
		    replay_stop_xid = $5::text::xid8, replay_stop_seq = $6,
		    replay_resume_xid = $7::text::xid8, replay_resume_seq = $8,
		    inflight_xid = NULL, inflight_seq = NULL, inflight_environments = NULL, inflight_rejections = 0,
		    lease_token = NULL, lease_until = NULL, next_attempt_at = $9
		WHERE sink_id = $1 AND event_kind = $2`,
		sink.Bytes(), string(kind), xid(plan.Cursor), plan.Cursor.Seq, stopXID, stopSeq, resumeXID, resumeSeq, now)
	if err != nil {
		return fmt.Errorf("replaying a stream: %w", err)
	}
	return nil
}

// CreateStream starts the stream of kind of sink at the position at, due now.
func (s *Store) CreateStream(ctx context.Context, sink sinks.ID, kind sinks.EventKind, at events.Cursor, now time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO sink_streams (sink_id, event_kind, cursor_xid, cursor_seq, next_attempt_at, created_at)
		VALUES ($1, $2, $3::text::xid8, $4, $5, $5)`, sink.Bytes(), string(kind), xid(at), at.Seq, now)
	if err != nil {
		return fmt.Errorf("creating a stream: %w", err)
	}
	return nil
}

// DeleteStream drops the stream of kind of sink.
func (s *Store) DeleteStream(ctx context.Context, sink sinks.ID, kind sinks.EventKind) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM sink_streams WHERE sink_id = $1 AND event_kind = $2`, sink.Bytes(), string(kind)); err != nil {
		return fmt.Errorf("deleting a stream: %w", err)
	}
	return nil
}

// SaveWarnings stores what the last check of sink's target found wrong.
func (s *Store) SaveWarnings(ctx context.Context, sink sinks.ID, warnings []string) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE sinks SET warnings = $2 WHERE id = $1`, sink.Bytes(), nonNil(warnings)); err != nil {
		return fmt.Errorf("storing a sink's warnings: %w", err)
	}
	return nil
}

// Claim leases to token, until until, the streams due by now of sinks that are
// enabled or still have a batch in flight and are not stopped, leaving out the
// sinks busy, the longest due first. A sink disabled with a batch in flight
// finishes it and starts nothing else.
func (s *Store) Claim(ctx context.Context, token appsinks.LeaseToken, now, until time.Time, limit int, busy []sinks.ID) ([]appsinks.ClaimedSink, error) {
	busyIDs := make([][16]byte, len(busy))
	for i, b := range busy {
		busyIDs[i] = b.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		WITH due AS (
		    SELECT ss.sink_id, ss.event_kind FROM sink_streams ss JOIN sinks s ON s.id = ss.sink_id
		    WHERE (s.enabled OR ss.inflight_xid IS NOT NULL) AND s.stopped_at IS NULL
		      AND ss.next_attempt_at <= $1 AND (ss.lease_until IS NULL OR ss.lease_until <= $1)
		      AND NOT (s.id = ANY($4::uuid[]))
		    ORDER BY ss.next_attempt_at, ss.sink_id, ss.event_kind
		    LIMIT $2
		    FOR UPDATE OF ss SKIP LOCKED
		)
		UPDATE sink_streams ss SET lease_token = $5, lease_until = $3,
		    cursor_xid = coalesce(ss.pending_cursor_xid, ss.cursor_xid), cursor_seq = coalesce(ss.pending_cursor_seq, ss.cursor_seq),
		    pending_cursor_xid = NULL, pending_cursor_seq = NULL
		FROM due WHERE ss.sink_id = due.sink_id AND ss.event_kind = due.event_kind
		RETURNING `+streamColumns, now, limit, until, busyIDs, token.String())
	if err != nil {
		return nil, fmt.Errorf("claiming streams: %w", err)
	}
	var (
		order  []sinks.ID
		leased = map[sinks.ID][]appsinks.Due{}
	)
	for rows.Next() {
		r, scanErr := scanStream(rows)
		if scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("reading a claimed stream: %w", scanErr)
		}
		if _, seen := leased[r.sink]; !seen {
			order = append(order, r.sink)
		}
		leased[r.sink] = append(leased[r.sink], appsinks.Due{Stream: r.stream, Inflight: r.inflight, Rejections: r.rejections})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, nil
	}
	return s.claimedSinks(ctx, order, leased)
}

// claimedSinks reads the sinks of the streams leased, with their project's
// key, in the order of order.
func (s *Store) claimedSinks(ctx context.Context, order []sinks.ID, leased map[sinks.ID][]appsinks.Due) ([]appsinks.ClaimedSink, error) {
	ids := make([][16]byte, len(order))
	for i, o := range order {
		ids[i] = o.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT `+sinkColumns+`, p.key`+sinkFrom+` JOIN projects p ON p.id = s.project_id
		WHERE s.id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("reading the sinks claimed: %w", err)
	}
	defer rows.Close()
	by := map[sinks.ID]appsinks.ClaimedSink{}
	for rows.Next() {
		var key string
		v, scanErr := scanSink(withTrailing(rows, &key))
		if scanErr != nil {
			return nil, fmt.Errorf("reading a sink claimed: %w", scanErr)
		}
		by[v.ID] = appsinks.ClaimedSink{Sink: v, ProjectKey: key, Streams: leased[v.ID]}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]appsinks.ClaimedSink, 0, len(order))
	for _, o := range order {
		if c, ok := by[o]; ok {
			slices.SortFunc(c.Streams, func(a, b appsinks.Due) int {
				return slices.Index(sinks.EventKinds, a.Stream.EventKind) - slices.Index(sinks.EventKinds, b.Stream.EventKind)
			})
			out = append(out, c)
		}
	}
	return out, nil
}

// trailing scans the columns of a row but its last into the destinations scan
// is given, and the last into one of its own.
type trailing struct {
	pgx.Row
	last any
}

func withTrailing(row pgx.Row, last any) pgx.Row { return trailing{Row: row, last: last} }

func (t trailing) Scan(dest ...any) error { return t.Row.Scan(append(dest, t.last)...) }

// Renew pushes the lease token holds on stream out to until and reports the
// state of its sink.
func (s *Store) Renew(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken, until time.Time) (appsinks.Renewal, error) {
	var r appsinks.Renewal
	err := s.db(ctx).QueryRow(ctx, `
		UPDATE sink_streams ss SET lease_until = $4 FROM sinks s
		WHERE ss.sink_id = $1 AND ss.event_kind = $2 AND ss.lease_token = $3 AND s.id = ss.sink_id
		RETURNING s.enabled, s.stopped_at IS NOT NULL`, stream.Sink.Bytes(), string(stream.Kind), token.String(), until).
		Scan(&r.Enabled, &r.Stopped)
	if errors.Is(err, pgx.ErrNoRows) {
		return appsinks.Renewal{}, nil
	}
	if err != nil {
		return appsinks.Renewal{}, fmt.Errorf("renewing a stream's lease: %w", err)
	}
	r.Held = true
	return r, nil
}

// Begin commits the batch to deliver next of stream, as in flight, while its
// sink is enabled and not stopped.
func (s *Store) Begin(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken, in appsinks.Inflight) (bool, error) {
	environments := make([][16]byte, len(in.Environments))
	for i, e := range in.Environments {
		environments[i] = e.Bytes()
	}
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE sink_streams ss
		SET inflight_xid = $4::text::xid8, inflight_seq = $5, inflight_environments = $6::uuid[], inflight_rejections = 0
		FROM sinks s
		WHERE ss.sink_id = $1 AND ss.event_kind = $2 AND ss.lease_token = $3 AND s.id = ss.sink_id
		  AND s.enabled AND s.stopped_at IS NULL`,
		stream.Sink.Bytes(), string(stream.Kind), token.String(), xid(in.To), in.To.Seq, environments)
	if err != nil {
		return false, fmt.Errorf("committing a batch in flight: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	// Either the lease is gone or the sink was stopped or disabled.
	held, err := s.holds(ctx, stream, token)
	if err != nil {
		return false, err
	}
	if held {
		return true, appsinks.ErrHalted
	}
	return false, nil
}

func (s *Store) holds(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken) (bool, error) {
	var held bool
	err := s.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sink_streams WHERE sink_id = $1 AND event_kind = $2 AND lease_token = $3)`,
		stream.Sink.Bytes(), string(stream.Kind), token.String()).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("reading a stream's lease: %w", err)
	}
	return held, nil
}

// Ack moves stream's cursor to to and clears everything an acknowledged batch
// ends, releasing the lease, while token holds it.
func (s *Store) Ack(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken, to events.Cursor, delivered int, next, now time.Time) (bool, error) {
	if delivered > 0 && s.fault.loses() {
		return true, nil
	}
	// A bounded replay that the batch reaches the end of is over: the cursor
	// goes back to where the replay was to return to, not to the batch's end.
	tag, err := s.db(ctx).Exec(ctx, `
		WITH s AS (
		    SELECT sink_id, event_kind,
		        coalesce(replay_stop_xid IS NOT NULL AND ($4::text::xid8, $5::bigint) >= (replay_stop_xid, replay_stop_seq), false) AS over
		    FROM sink_streams
		    WHERE sink_id = $1 AND event_kind = $2 AND lease_token = $3
		    FOR UPDATE)
		UPDATE sink_streams ss SET
		    cursor_xid = CASE WHEN s.over THEN ss.replay_resume_xid ELSE $4::text::xid8 END,
		    cursor_seq = CASE WHEN s.over THEN ss.replay_resume_seq ELSE $5::bigint END,
		    replay_stop_xid = CASE WHEN s.over THEN NULL ELSE ss.replay_stop_xid END,
		    replay_stop_seq = CASE WHEN s.over THEN NULL ELSE ss.replay_stop_seq END,
		    replay_resume_xid = CASE WHEN s.over THEN NULL ELSE ss.replay_resume_xid END,
		    replay_resume_seq = CASE WHEN s.over THEN NULL ELSE ss.replay_resume_seq END,
		    inflight_xid = NULL, inflight_seq = NULL, inflight_environments = NULL, inflight_rejections = 0,
		    lease_token = NULL, lease_until = NULL, next_attempt_at = $6,
		    consecutive_failures = 0,
		    consecutive_dead_letters = CASE WHEN $7::bigint > 0 THEN 0 ELSE consecutive_dead_letters END,
		    last_failure_class = NULL, last_failure_status = NULL, last_failure_code = NULL, last_failure_at = NULL,
		    last_delivered_at = CASE WHEN $7::bigint > 0 THEN $8::timestamptz ELSE ss.last_delivered_at END,
		    delivered_total = ss.delivered_total + $7::bigint
		FROM s
		WHERE ss.sink_id = s.sink_id AND ss.event_kind = s.event_kind`,
		stream.Sink.Bytes(), string(stream.Kind), token.String(), xid(to), to.Seq, next, int64(delivered), now)
	if err != nil {
		return false, fmt.Errorf("recording a delivery: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Fail records a failed attempt of stream, while token holds its lease. It
// takes the sink's row first, so that two streams of one sink failing on two
// replicas at once do not both begin its episode.
func (s *Store) Fail(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken, f sinks.Failure, rejected bool, next, now time.Time) (held, entered bool, err error) {
	db := s.db(ctx)
	if _, err = db.Exec(ctx, `SELECT 1 FROM sinks WHERE id = $1 FOR NO KEY UPDATE`, stream.Sink.Bytes()); err != nil {
		return false, false, fmt.Errorf("locking a sink: %w", err)
	}
	var before int
	var others bool
	err = db.QueryRow(ctx, `
		SELECT ss.consecutive_failures, EXISTS (
		    SELECT 1 FROM sink_streams o WHERE o.sink_id = ss.sink_id AND o.event_kind <> ss.event_kind AND o.consecutive_failures > 0)
		FROM sink_streams ss WHERE ss.sink_id = $1 AND ss.event_kind = $2 AND ss.lease_token = $3`,
		stream.Sink.Bytes(), string(stream.Kind), token.String()).Scan(&before, &others)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading a stream's failures: %w", err)
	}
	status, code := failureArgs(f)
	_, err = db.Exec(ctx, `
		UPDATE sink_streams SET
		    consecutive_failures = consecutive_failures + 1,
		    last_failure_class = $4, last_failure_status = $5, last_failure_code = $6, last_failure_at = $7,
		    inflight_rejections = inflight_rejections + CASE WHEN $8 THEN 1 ELSE 0 END,
		    next_attempt_at = $9, lease_token = NULL, lease_until = NULL
		WHERE sink_id = $1 AND event_kind = $2 AND lease_token = $3`,
		stream.Sink.Bytes(), string(stream.Kind), token.String(), string(f.Class), status, code, now, rejected, next)
	if err != nil {
		return false, false, fmt.Errorf("recording a failed attempt: %w", err)
	}
	return true, before == 0 && !others, nil
}

// Idle releases stream's lease with nothing to send, to be attempted again at
// next.
func (s *Store) Idle(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken, next time.Time) (bool, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE sink_streams SET lease_token = NULL, lease_until = NULL, next_attempt_at = $4
		WHERE sink_id = $1 AND event_kind = $2 AND lease_token = $3`,
		stream.Sink.Bytes(), string(stream.Kind), token.String(), next)
	if err != nil {
		return false, fmt.Errorf("releasing a stream: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Release gives up stream's lease without an attempt.
func (s *Store) Release(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken) (bool, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE sink_streams SET lease_token = NULL, lease_until = NULL
		WHERE sink_id = $1 AND event_kind = $2 AND lease_token = $3`,
		stream.Sink.Bytes(), string(stream.Kind), token.String())
	if err != nil {
		return false, fmt.Errorf("releasing a stream: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// failureArgs returns a failure's status and code as query arguments: null
// where the target gave none.
func failureArgs(f sinks.Failure) (status, code any) {
	if f.Status != 0 {
		status = f.Status
	}
	if f.Code != "" {
		code = f.Code
	}
	return status, code
}
