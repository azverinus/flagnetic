package access

import (
	"context"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// reserveAttempt counts an attempt as a failure of its source and, unless
// the source is throttled or the email locked, of its email, in one
// statement: two attempts at once both count, so neither passes a check the
// other's failure would have failed. The failure that reaches the lockout
// threshold locks the email at once, for the window the domain's schedule
// gives its count. The email's row is locked and its lock read first, in the
// same statement, so the lock it answers is the one its count was judged
// against: a statement waiting on another sees that one's outcome.
// Statements take the source's row before the email's, as every other write
// to both does, so two never wait on each other.
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
		SELECT locked_until FROM sign_in_email_failures WHERE email = $1 FOR UPDATE
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

// releaseEmail takes back the failure r counted against its email. When no
// failure was counted since, the email is as it was before r: a lock r
// started is lifted, and a lock before it had ended, since r was counted. An
// email holding fewer failures than r counted was cleared since, and r's
// failure went with it, so nothing is taken back twice.
const releaseEmail = `
	WITH released AS (
		UPDATE sign_in_email_failures SET
			failures     = failures - 1,
			locked_until = CASE WHEN failures = $2 THEN NULL ELSE locked_until END
		WHERE email = $1 AND failures >= $2 AND failures > 1
		RETURNING email
	)
	DELETE FROM sign_in_email_failures WHERE email = $1 AND failures = 1 AND $2 = 1`

// ReleaseAttempt takes back the failure r reserved when its password could
// not be verified at all, such as when no hashing slot freed in time, or
// proved right but the sign-in failed after: that is no wrong guess.
func (s *Store) ReleaseAttempt(ctx context.Context, r access.Reservation) error {
	if err := s.releaseSource(ctx, r); err != nil {
		return err
	}
	if r.Failures == 0 {
		return nil
	}
	_, err := s.db(ctx).Exec(ctx, releaseEmail, r.Email, r.Failures)
	return err
}
