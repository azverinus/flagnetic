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
// the source is throttled or the failures it is counted under locked, of
// its email, in one statement: two attempts at once both count, so neither
// passes a check the other's failure would have failed. An attempt from a
// source the email's member signed in from since the cutoff is counted under
// the email and that source (known_prior, known_email), any other under the
// email alone (prior, email); an email no member has knows no source, so it
// is counted like a known one's from anywhere else. The failure that
// reaches the lockout threshold locks what it is counted under at once, for
// the window the domain's schedule gives its count. The row is locked and
// its lock read before it is counted, in the same statement, so the lock it
// answers is the one its count was judged against: a statement waiting on
// another sees that one's outcome, when the row existed as it began
// (ReserveAttempt reads the lock otherwise). The source's row is taken before the
// email's, as every other write to both takes them, so two never wait on
// each other: prior joins source, so it locks the email's row only once the
// source's is written, whatever order the statement's parts run in, and
// email reads prior, so it counts only once the row is locked.
//
// $1 email, $2 now, $3 the lockout schedule in seconds, $4 source, $5 when a
// window opened that has ended, $6 the source failure limit, $7 the instant
// after which a sign-in from the source makes it known.
const reserveAttempt = `
	WITH source AS (
		INSERT INTO sign_in_source_failures AS s (source, window_start, failures) VALUES ($4, $2, 1)
		ON CONFLICT (source) DO UPDATE SET
			window_start = CASE WHEN s.window_start <= $5 THEN $2 ELSE s.window_start END,
			failures     = CASE WHEN s.window_start <= $5 THEN 1 ELSE s.failures + 1 END
		RETURNING window_start, failures
	), known AS (
		SELECT EXISTS (
			SELECT FROM members m JOIN sign_in_known_sources k ON k.member_id = m.id
			WHERE lower(m.email) = lower($1) AND k.source = $4 AND k.signed_in_at > $7
		) AS known
	), prior AS (
		SELECT f.locked_until FROM sign_in_email_failures f, source, known
		WHERE NOT known.known AND f.email = $1 FOR UPDATE OF f
	), email AS (
		INSERT INTO sign_in_email_failures AS f (email, failures, locked_until, last_failure_at)
		SELECT $1, 1, $2 + make_interval(secs => NULLIF(($3::bigint[])[1], 0)), $2
		FROM source CROSS JOIN known LEFT JOIN prior ON true WHERE source.failures <= $6 AND NOT known.known
		ON CONFLICT (email) DO UPDATE SET
			failures        = f.failures + 1,
			last_failure_at = $2,
			locked_until    = $2 + make_interval(secs =>
				NULLIF(($3::bigint[])[LEAST(f.failures + 1, cardinality($3::bigint[]))], 0))
		WHERE f.locked_until IS NULL OR f.locked_until <= $2
		RETURNING failures, locked_until
	), known_prior AS (
		SELECT f.locked_until FROM sign_in_known_source_failures f, source, known
		WHERE known.known AND f.email = $1 AND f.source = $4 FOR UPDATE OF f
	), known_email AS (
		INSERT INTO sign_in_known_source_failures AS f (email, source, failures, locked_until, last_failure_at)
		SELECT $1, $4, 1, $2 + make_interval(secs => NULLIF(($3::bigint[])[1], 0)), $2
		FROM source CROSS JOIN known LEFT JOIN known_prior ON true WHERE source.failures <= $6 AND known.known
		ON CONFLICT (email, source) DO UPDATE SET
			failures        = f.failures + 1,
			last_failure_at = $2,
			locked_until    = $2 + make_interval(secs =>
				NULLIF(($3::bigint[])[LEAST(f.failures + 1, cardinality($3::bigint[]))], 0))
		WHERE f.locked_until IS NULL OR f.locked_until <= $2
		RETURNING failures, locked_until
	)
	SELECT source.window_start, source.failures, known.known,
	       COALESCE(email.failures, known_email.failures), COALESCE(email.locked_until, known_email.locked_until),
	       COALESCE((SELECT locked_until FROM prior), (SELECT locked_until FROM known_prior))
	FROM source CROSS JOIN known LEFT JOIN email ON true LEFT JOIN known_email ON true`

// ReserveAttempt counts a as a failure before its password is verified, and
// returns what it found: failures locked by earlier ones are not counted
// again, and report when they unlock. It runs in the unit of work ctx
// carries.
//
// A statement sees only the rows committed before it began, so failures
// first counted by a statement it waited on, on the source's row, say, are
// invisible to its prior: the count finds them locked and skips them, but
// the statement cannot say until when. The skipped row is locked all the
// same until the unit of work ends, so a second statement, which sees it,
// reads the lock it was judged against.
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
		access.SourceFailureLimit, a.KnownAfter()).
		Scan(&r.WindowStart, &r.SourceFailures, &r.KnownSource, &failures, &locksUntil, &lockedTil)
	if err != nil {
		return access.Reservation{}, fmt.Errorf("reserving a sign-in attempt: %w", err)
	}
	switch {
	case failures != nil:
		r.Failures = *failures
		if locksUntil != nil {
			r.LocksUntil = *locksUntil
		}
	case lockedTil != nil:
		r.LockedUntil = *lockedTil
	case r.SourceFailures <= access.SourceFailureLimit:
		// Counted nowhere, yet not throttled: the failures it is counted
		// under were locked by a statement committed after this one began.
		f := failuresOf(r)
		err = s.db(ctx).QueryRow(ctx, `SELECT locked_until FROM `+f.table+` WHERE `+f.key, f.args...).Scan(&lockedTil)
		if err != nil {
			return access.Reservation{}, fmt.Errorf("reading the lock of an email's failures: %w", err)
		}
		if lockedTil != nil {
			r.LockedUntil = *lockedTil
		}
	}
	return r, nil
}

// failures is the row of consecutive failures a reservation was counted
// under: the email's own, or the email's from one source known to its
// member.
type failures struct {
	table, key string
	args       []any
}

// failuresOf returns the row r was counted under, keyed by key with args.
func failuresOf(r access.Reservation) failures {
	if r.KnownSource {
		return failures{table: "sign_in_known_source_failures", key: "email = $1 AND source = $2", args: []any{r.Email, r.Source}}
	}
	return failures{table: "sign_in_email_failures", key: "email = $1", args: []any{r.Email}}
}

// ClearAttempt takes back the failure r reserved once its password proved
// right: the source's window, when it is still the one counted in, holds one
// less, and the consecutive failures r was counted under end. A right
// password from a known source leaves the email's own failures, which every
// other source shares, as they are: the member signing in from home does
// not reset a guesser's count elsewhere.
func (s *Store) ClearAttempt(ctx context.Context, r access.Reservation) error {
	if err := s.releaseSource(ctx, r); err != nil {
		return err
	}
	f := failuresOf(r)
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM `+f.table+` WHERE `+f.key, f.args...)
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
// the unit of work ctx carries, so the source and the failures r was
// counted under are taken back together. When no failure was counted on
// them since r, they are as they were before r: a lock r started is lifted,
// and a lock before it had ended, since r was counted. Failures fewer than r
// counted were cleared since, and r's failure went with them, so nothing is
// taken back twice.
func (s *Store) ReleaseAttempt(ctx context.Context, r access.Reservation) error {
	if err := s.releaseSource(ctx, r); err != nil {
		return err
	}
	if r.Failures == 0 {
		return nil
	}
	f := failuresOf(r)
	var n int
	err := s.db(ctx).QueryRow(ctx, `SELECT failures FROM `+f.table+` WHERE `+f.key+` FOR UPDATE`, f.args...).Scan(&n)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("locking an email's failures: %w", err)
	case n < r.Failures:
		return nil
	case n == 1:
		_, err = s.db(ctx).Exec(ctx, `DELETE FROM `+f.table+` WHERE `+f.key, f.args...)
	case n == r.Failures:
		_, err = s.db(ctx).Exec(ctx, `UPDATE `+f.table+` SET failures = failures - 1, locked_until = NULL WHERE `+f.key, f.args...)
	default:
		_, err = s.db(ctx).Exec(ctx, `UPDATE `+f.table+` SET failures = failures - 1 WHERE `+f.key, f.args...)
	}
	return err
}

// RecordKnownSource records that member signed in from source at, which
// keeps the source known to them for the age the domain gives it.
func (s *Store) RecordKnownSource(ctx context.Context, member access.MemberID, source netip.Prefix, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO sign_in_known_sources AS k (member_id, source, signed_in_at) VALUES ($1, $2, $3)
		ON CONFLICT (member_id, source) DO UPDATE SET signed_in_at = GREATEST(k.signed_in_at, EXCLUDED.signed_in_at)`,
		member.Bytes(), source, at)
	if err != nil {
		return fmt.Errorf("recording a known source: %w", err)
	}
	return nil
}
