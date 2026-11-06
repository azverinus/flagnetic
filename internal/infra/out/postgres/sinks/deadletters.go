package sinks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SetAside sets the batch in flight of stream aside as the dead letter dl and
// moves the stream past it, while token holds the lease. It takes the sink's
// row first, as a failure does, so that two streams of one sink setting a
// batch aside on two replicas at once do not both stop it.
func (s *Store) SetAside(ctx context.Context, stream appsinks.StreamRef, token appsinks.LeaseToken, dl appsinks.NewDeadLetter, stopAfter int, now time.Time) (appsinks.SetAsideResult, error) {
	db := s.db(ctx)
	var res appsinks.SetAsideResult
	if _, err := db.Exec(ctx, `SELECT 1 FROM sinks WHERE id = $1 FOR NO KEY UPDATE`, stream.Sink.Bytes()); err != nil {
		return res, fmt.Errorf("locking a sink: %w", err)
	}
	status, code := failureArgs(dl.Failure.Failure)
	err := db.QueryRow(ctx, `
		INSERT INTO sink_dead_letters (id, sink_id, event_kind, from_xid, from_seq, to_xid, to_seq, environments, rows,
		    first_received_at, last_received_at, attempts, last_failure_class, last_failure_status, last_failure_code,
		    last_failure_at, status, created_at)
		SELECT $4, ss.sink_id, ss.event_kind, ss.cursor_xid, ss.cursor_seq, ss.inflight_xid, ss.inflight_seq,
		    ss.inflight_environments, $5, $6, $7, ss.inflight_rejections + 1, $8, $9, $10, $11, 'pending', $11
		FROM sink_streams ss
		WHERE ss.sink_id = $1 AND ss.event_kind = $2 AND ss.lease_token = $3 AND ss.inflight_xid IS NOT NULL
		RETURNING attempts`,
		stream.Sink.Bytes(), string(stream.Kind), token.String(), dl.ID.Bytes(), dl.Rows, dl.First, dl.Last,
		string(dl.Failure.Class), status, code, now).Scan(&res.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return appsinks.SetAsideResult{}, nil
	}
	if err != nil {
		return res, fmt.Errorf("setting a batch aside: %w", err)
	}
	res.Held = true
	err = db.QueryRow(ctx, `
		UPDATE sink_streams SET
		    cursor_xid = inflight_xid, cursor_seq = inflight_seq,
		    inflight_xid = NULL, inflight_seq = NULL, inflight_environments = NULL, inflight_rejections = 0,
		    lease_token = NULL, lease_until = NULL, next_attempt_at = $4,
		    consecutive_failures = 0,
		    last_failure_class = $5, last_failure_status = $6, last_failure_code = $7, last_failure_at = $4,
		    consecutive_dead_letters = consecutive_dead_letters + 1
		WHERE sink_id = $1 AND event_kind = $2 AND lease_token = $3
		RETURNING consecutive_dead_letters`,
		stream.Sink.Bytes(), string(stream.Kind), token.String(), now, string(dl.Failure.Class), status, code).Scan(&res.InARow)
	if err != nil {
		return res, fmt.Errorf("moving a stream past a dead letter: %w", err)
	}
	if sinks.StopsAfter(res.InARow, stopAfter) {
		tag, err := db.Exec(ctx, `UPDATE sinks SET stopped_at = $2 WHERE id = $1 AND stopped_at IS NULL`, stream.Sink.Bytes(), now)
		if err != nil {
			return res, fmt.Errorf("stopping a sink: %w", err)
		}
		res.Stopped = tag.RowsAffected() > 0
	}
	return res, nil
}

const deadLetterColumns = `d.id, d.sink_id, d.event_kind, d.from_xid::text, d.from_seq, d.to_xid::text, d.to_seq, d.environments,
	d.rows, d.first_received_at, d.last_received_at, d.attempts, d.last_failure_class, d.last_failure_status,
	d.last_failure_code, d.last_failure_at, d.status, d.created_at, d.redelivered_at`

func scanDeadLetter(row pgx.Row) (sinks.DeadLetter, error) {
	var (
		dl                  sinks.DeadLetter
		dk, sk              [16]byte
		kind, fromX, toX    string
		fromSeq, toSeq      int64
		environments        [][16]byte
		class, status       string
		failStatus          *int
		failCode            *string
		first, last, failAt time.Time
		created             time.Time
		redelivered         *time.Time
	)
	if err := row.Scan(&dk, &sk, &kind, &fromX, &fromSeq, &toX, &toSeq, &environments, &dl.Rows, &first, &last,
		&dl.Attempts, &class, &failStatus, &failCode, &failAt, &status, &created, &redelivered); err != nil {
		return dl, err
	}
	dl.ID, dl.Sink, dl.EventKind = id.FromBytes[sinks.DeadLetter](dk), id.FromBytes[sinks.Sink](sk), sinks.EventKind(kind)
	var err error
	if dl.From, err = cursorOf(fromX, fromSeq); err != nil {
		return dl, err
	}
	if dl.To, err = cursorOf(toX, toSeq); err != nil {
		return dl, err
	}
	for _, e := range environments {
		dl.Environments = append(dl.Environments, id.FromBytes[workspace.Environment](e))
	}
	dl.FirstReceivedAt, dl.LastReceivedAt = first.UTC(), last.UTC()
	dl.LastFailure = sinks.FailureAt{Failure: sinks.Failure{Class: sinks.FailureClass(class)}, At: failAt.UTC()}
	if failStatus != nil {
		dl.LastFailure.Status = *failStatus
	}
	if failCode != nil {
		dl.LastFailure.Code = *failCode
	}
	dl.Status, dl.CreatedAt, dl.RedeliveredAt = sinks.DeadLetterStatus(status), created.UTC(), utc(redelivered)
	return dl, nil
}

// DeadLetters returns sink's dead letters, the oldest first.
func (s *Store) DeadLetters(ctx context.Context, sink sinks.ID) ([]sinks.DeadLetter, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+deadLetterColumns+` FROM sink_dead_letters d
		WHERE d.sink_id = $1 ORDER BY d.created_at, d.id`, sink.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing dead letters: %w", err)
	}
	defer rows.Close()
	out := []sinks.DeadLetter{}
	for rows.Next() {
		dl, scanErr := scanDeadLetter(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("reading a dead letter: %w", scanErr)
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// DeadLetter returns sink's dead letter dl, or appsinks.ErrNotFound.
func (s *Store) DeadLetter(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID) (sinks.DeadLetter, error) {
	v, err := scanDeadLetter(s.db(ctx).QueryRow(ctx, `SELECT `+deadLetterColumns+` FROM sink_dead_letters d
		WHERE d.sink_id = $1 AND d.id = $2`, sink.Bytes(), dl.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, appsinks.ErrNotFound
	}
	if err != nil {
		return v, fmt.Errorf("reading a dead letter: %w", err)
	}
	return v, nil
}

// RecordRedelivery records a redelivery of dl: one more attempt, and the
// failure that ended it, or, when the target acknowledged it, that it is
// redelivered at now.
func (s *Store) RecordRedelivery(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID, failure *sinks.FailureAt, now time.Time) (sinks.DeadLetter, error) {
	var tag any
	if failure == nil {
		tag = now
	}
	var class, status, code, at any
	if failure != nil {
		class, at = string(failure.Class), failure.At
		status, code = failureArgs(failure.Failure)
	}
	row := s.db(ctx).QueryRow(ctx, `
		UPDATE sink_dead_letters d SET
		    attempts = attempts + 1,
		    status = CASE WHEN $3::timestamptz IS NOT NULL THEN 'redelivered' ELSE status END,
		    redelivered_at = CASE WHEN $3::timestamptz IS NOT NULL THEN $3::timestamptz ELSE redelivered_at END,
		    last_failure_class = coalesce($4, last_failure_class),
		    last_failure_status = CASE WHEN $4::text IS NOT NULL THEN $5::integer ELSE last_failure_status END,
		    last_failure_code = CASE WHEN $4::text IS NOT NULL THEN $6::text ELSE last_failure_code END,
		    last_failure_at = coalesce($7::timestamptz, last_failure_at)
		WHERE d.sink_id = $1 AND d.id = $2
		RETURNING `+deadLetterColumns, sink.Bytes(), dl.Bytes(), tag, class, status, code, at)
	out, err := scanDeadLetter(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, appsinks.ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("recording a redelivery: %w", err)
	}
	return out, nil
}

// DiscardDeadLetter deletes sink's dead letter dl; one that is gone already
// is left gone.
func (s *Store) DiscardDeadLetter(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM sink_dead_letters WHERE sink_id = $1 AND id = $2`, sink.Bytes(), dl.Bytes()); err != nil {
		return fmt.Errorf("discarding a dead letter: %w", err)
	}
	return nil
}

// Resume lifts sink's stop and clears its streams' runs of dead letters,
// making them due at now at the latest; it reports when the sink had been
// stopped, nil when it had not and nothing was done. A stream's backoff still
// running is not cut short, only a wait longer than now.
func (s *Store) Resume(ctx context.Context, sink sinks.ID, now time.Time) (*time.Time, error) {
	var was *time.Time
	err := s.db(ctx).QueryRow(ctx, `SELECT stopped_at FROM sinks WHERE id = $1 FOR UPDATE`, sink.Bytes()).Scan(&was)
	if err != nil {
		return nil, fmt.Errorf("reading when a sink stopped: %w", err)
	}
	if was == nil {
		return nil, nil
	}
	if _, err = s.db(ctx).Exec(ctx, `UPDATE sinks SET stopped_at = NULL WHERE id = $1`, sink.Bytes()); err != nil {
		return nil, fmt.Errorf("resuming a sink: %w", err)
	}
	if _, err = s.db(ctx).Exec(ctx, `
		UPDATE sink_streams SET consecutive_dead_letters = 0, next_attempt_at = LEAST(next_attempt_at, $2)
		WHERE sink_id = $1`, sink.Bytes(), now); err != nil {
		return nil, fmt.Errorf("making a sink's streams due: %w", err)
	}
	return utc(was), nil
}

// PendingDeadLetters returns how many dead letters wait, of every sink that
// has any or none.
func (s *Store) PendingDeadLetters(ctx context.Context) (map[sinks.ID]int, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT s.id, count(d.id) FROM sinks s LEFT JOIN sink_dead_letters d ON d.sink_id = s.id AND d.status = 'pending'
		GROUP BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("counting pending dead letters: %w", err)
	}
	defer rows.Close()
	out := map[sinks.ID]int{}
	for rows.Next() {
		var sk [16]byte
		var n int
		if err = rows.Scan(&sk, &n); err != nil {
			return nil, fmt.Errorf("reading a count of dead letters: %w", err)
		}
		out[id.FromBytes[sinks.Sink](sk)] = n
	}
	return out, rows.Err()
}

// withDeadLetters sets how many dead letters wait of each of list's sinks.
func (s *Store) withDeadLetters(ctx context.Context, list []sinks.Sink) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([][16]byte, len(list))
	for i, v := range list {
		ids[i] = v.ID.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT sink_id, count(*) FROM sink_dead_letters WHERE status = 'pending' AND sink_id = ANY($1::uuid[]) GROUP BY sink_id`, ids)
	if err != nil {
		return fmt.Errorf("counting dead letters: %w", err)
	}
	defer rows.Close()
	counts := map[sinks.ID]int{}
	for rows.Next() {
		var sk [16]byte
		var n int
		if err = rows.Scan(&sk, &n); err != nil {
			return fmt.Errorf("reading a count of dead letters: %w", err)
		}
		counts[id.FromBytes[sinks.Sink](sk)] = n
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range list {
		list[i].DeadLetters = counts[list[i].ID]
	}
	return nil
}
