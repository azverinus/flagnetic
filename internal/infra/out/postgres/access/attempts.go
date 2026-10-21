package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// reserveAttempt counts an attempt as a failure of its source and, unless
// the source is throttled or the email locked, of its email, in one
// statement: two attempts at once both count, so neither passes a check the
// other's failure would have failed. The failure that reaches the lockout
// threshold locks the email at once, for the window the domain's schedule
// gives its count. The email's row is locked and its lock read before it is
// counted, in the same statement, so the lock it answers is the one its
// count was judged against: a statement waiting on another sees that one's
// outcome. The source's row is taken before the email's, as every other
// write to both takes them, so two never wait on each other: prior joins
// source, so it locks the email's row only once the source's is written,
// whatever order the statement's parts run in, and email reads prior, so it
// counts only once the row is locked.
//
// $1 email, $2 now, $3 the lockout schedule in seconds, $4 source, $5 when a
// window opened that has ended, $6 the source failure limit.
const reserveAttempt = `
	WITH source AS (
		INSERT INTO sign_in_source_failures AS s (source, window_start, failures) VALUES ($4, $2, 1)
		ON CONFLICT (source) DO UPDATE SET
			window_start = CASE WHEN s.window_start <= $5 THEN $2 ELSE s.window_start END,
			failures     = CASE WHEN s.window_start <= $5 THEN 1 ELSE s.failures + 1 END
		RETURNING window_start, failures
	), prior AS (
		SELECT f.locked_until FROM sign_in_email_failures f, source WHERE f.email = $1 FOR UPDATE OF f
	), email AS (
		INSERT INTO sign_in_email_failures AS f (email, failures, locked_until, last_failure_at)
		SELECT $1, 1, $2 + make_interval(secs => NULLIF(($3::bigint[])[1], 0)), $2
		FROM source LEFT JOIN prior ON true WHERE source.failures <= $6
		ON CONFLICT (email) DO UPDATE SET
			failures        = f.failures + 1,
			last_failure_at = $2,
			locked_until    = $2 + make_interval(secs =>
				NULLIF(($3::bigint[])[LEAST(f.failures + 1, cardinality($3::bigint[]))], 0))
		WHERE f.locked_until IS NULL OR f.locked_until <= $2
		RETURNING failures, locked_until
	)
	SELECT source.window_start, source.failures, email.failures, email.locked_until,
	       (SELECT locked_until FROM prior)
	FROM source LEFT JOIN email ON true`

// ReserveAttempt counts a as a failure before its password is verified, and
// returns what it found: an email locked by earlier failures is not counted
// again, and reports when it unlocks.
func (s *Store) ReserveAttempt(ctx context.Context, a access.Attempt) (access.Reservation, error) {
	schedule := access.LockoutSchedule()
	seconds := make([]int64, len(schedule))
	for i, w := range schedule {
		seconds[i] = int64(w / time.Second)
	}
	r := access.Reservation{Attempt: a}
	var (
		failures              *int
		locksUntil, lockedTil *time.Time
	)
	err := s.db(ctx).QueryRow(ctx, reserveAttempt, a.Email, a.At, seconds, a.Source, a.At.Add(-access.SourceWindow),
		access.SourceFailureLimit).Scan(&r.WindowStart, &r.SourceFailures, &failures, &locksUntil, &lockedTil)
	if err != nil {
		return access.Reservation{}, fmt.Errorf("reserving a sign-in attempt: %w", err)
	}
	if failures != nil {
		r.Failures = *failures
		if locksUntil != nil {
			r.LocksUntil = *locksUntil
		}
	} else if lockedTil != nil {
		r.LockedUntil = *lockedTil
	}
	return r, nil
}

// ClearAttempt takes back the failure r reserved once its password proved
// right: the source's window, when it is still the one counted in, holds one
// less, and the email's consecutive failures end.
func (s *Store) ClearAttempt(ctx context.Context, r access.Reservation) error {
	if err := s.releaseSource(ctx, r); err != nil {
		return err
	}
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM sign_in_email_failures WHERE email = $1`, r.Email)
	return err
}

// releaseSource takes r's failure back from the source's window it counted
// in, unless another has started since.
func (s *Store) releaseSource(ctx context.Context, r access.Reservation) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sign_in_source_failures SET failures = failures - 1
		WHERE source = $1 AND window_start = $2 AND failures > 0`, r.Source, r.WindowStart)
	return err
}

// ReleaseAttempt takes back the failure r reserved when its attempt guessed
// nothing: its password could not be verified, or proved right but the
// sign-in failed after, or it was refused before any was checked. It runs in
// the unit of work ctx carries, so the source and the email are taken back
// together. When no failure was counted on the email since r, the email is
// as it was before r: a lock r started is lifted, and a lock before it had
// ended, since r was counted. An email holding fewer failures than r counted
// was cleared since, and r's failure went with it, so nothing is taken back
// twice.
func (s *Store) ReleaseAttempt(ctx context.Context, r access.Reservation) error {
	if err := s.releaseSource(ctx, r); err != nil {
		return err
	}
	if r.Failures == 0 {
		return nil
	}
	var failures int
	err := s.db(ctx).QueryRow(ctx, `SELECT failures FROM sign_in_email_failures WHERE email = $1 FOR UPDATE`, r.Email).Scan(&failures)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("locking an email's failures: %w", err)
	case failures < r.Failures:
		return nil
	case failures == 1:
		_, err = s.db(ctx).Exec(ctx, `DELETE FROM sign_in_email_failures WHERE email = $1`, r.Email)
	case failures == r.Failures:
		_, err = s.db(ctx).Exec(ctx, `UPDATE sign_in_email_failures SET failures = failures - 1, locked_until = NULL WHERE email = $1`, r.Email)
	default:
		_, err = s.db(ctx).Exec(ctx, `UPDATE sign_in_email_failures SET failures = failures - 1 WHERE email = $1`, r.Email)
	}
	return err
}
