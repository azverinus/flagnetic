package access

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// reserveAttempt counts an attempt as a failure of its source and, unless
// the source is throttled or the email locked, of its email, in one
// statement: two attempts at once both count, so neither passes a check the
// other's failure would have failed. The failure that reaches the lockout
// threshold locks the email at once, for the window its count gives.
//
// $1 email, $2 now, $3 the lockout windows in seconds, $4 source, $5 when a
// window opened that has ended, $6 the lockout threshold, $7 the source
// failure limit.
const reserveAttempt = `
	WITH source AS (
		INSERT INTO sign_in_source_failures AS s (source, window_start, failures) VALUES ($4, $2, 1)
		ON CONFLICT (source) DO UPDATE SET
			window_start = CASE WHEN s.window_start <= $5 THEN $2 ELSE s.window_start END,
			failures     = CASE WHEN s.window_start <= $5 THEN 1 ELSE s.failures + 1 END
		RETURNING window_start, failures
	), email AS (
		INSERT INTO sign_in_email_failures AS f (email, failures, locked_until, last_failure_at)
		SELECT $1, 1,
		       CASE WHEN 1 >= $6 THEN $2 + make_interval(secs => ($3::bigint[])[1]) END, $2
		FROM source WHERE source.failures <= $7
		ON CONFLICT (email) DO UPDATE SET
			failures        = f.failures + 1,
			last_failure_at = $2,
			locked_until    = CASE WHEN f.failures + 1 >= $6
				THEN $2 + make_interval(secs => ($3::bigint[])[LEAST(f.failures + 2 - $6, cardinality($3::bigint[]))])
				ELSE f.locked_until END
		WHERE f.locked_until IS NULL OR f.locked_until <= $2
		RETURNING failures
	)
	SELECT source.window_start, source.failures, (SELECT failures FROM email) FROM source`

// ReserveAttempt counts an attempt on email from source at now as a failure
// before its password is verified, and returns what it found: an email
// locked by earlier failures is not counted again, and reports when it
// unlocks.
func (s *Store) ReserveAttempt(ctx context.Context, email string, source netip.Prefix, now time.Time) (access.Reservation, error) {
	windows := access.LockoutWindows()
	seconds := make([]int64, len(windows))
	for i, w := range windows {
		seconds[i] = int64(w / time.Second)
	}
	var (
		r        access.Reservation
		failures *int
	)
	err := s.db(ctx).QueryRow(ctx, reserveAttempt, email, now, seconds, source, now.Add(-access.SourceWindow),
		access.LockoutThreshold, access.SourceFailureLimit).Scan(&r.WindowStart, &r.SourceFailures, &failures)
	if err != nil {
		return access.Reservation{}, fmt.Errorf("reserving a sign-in attempt: %w", err)
	}
	if failures != nil || r.SourceFailures > access.SourceFailureLimit {
		if failures != nil {
			r.Failures = *failures
		}
		return r, nil
	}
	// Not counted on a source within its limit: the email was locked.
	var lockedUntil *time.Time
	err = s.db(ctx).QueryRow(ctx, `SELECT failures, locked_until FROM sign_in_email_failures WHERE email = $1`, email).
		Scan(&r.Failures, &lockedUntil)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Cleared by a sign-in since: the lock is over.
		return r, nil
	case err != nil:
		return access.Reservation{}, fmt.Errorf("reading a lockout: %w", err)
	}
	if lockedUntil != nil {
		r.LockedUntil = *lockedUntil
	}
	return r, nil
}

// ClearAttempt takes back the failure an attempt reserved once its
// password proved right: the email's consecutive failures end, and the
// source's window, when it is still the one counted in, holds one less.
func (s *Store) ClearAttempt(ctx context.Context, email string, source netip.Prefix, windowStart time.Time) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM sign_in_email_failures WHERE email = $1`, email); err != nil {
		return err
	}
	return s.releaseSource(ctx, source, windowStart)
}

// releaseSource takes one failure back from the source's window that
// started at windowStart, unless another has started since.
func (s *Store) releaseSource(ctx context.Context, source netip.Prefix, windowStart time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE sign_in_source_failures SET failures = failures - 1
		WHERE source = $1 AND window_start = $2 AND failures > 0`, source, windowStart)
	return err
}

// ReleaseAttempt takes back the failure an attempt reserved when its
// password could not be verified at all, such as when no hashing slot freed
// in time: that is no wrong guess. A lock the reservation started is lifted
// with it.
func (s *Store) ReleaseAttempt(ctx context.Context, email string, source netip.Prefix, windowStart time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		WITH released AS (
			UPDATE sign_in_email_failures SET
				failures     = failures - 1,
				locked_until = CASE WHEN failures - 1 >= $2 THEN locked_until END
			WHERE email = $1 AND failures > 1
			RETURNING email
		)
		DELETE FROM sign_in_email_failures WHERE email = $1 AND failures = 1`, email, access.LockoutThreshold)
	if err != nil {
		return err
	}
	return s.releaseSource(ctx, source, windowStart)
}
