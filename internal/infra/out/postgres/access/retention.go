package access

import (
	"context"
	"fmt"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// TryLockAuthRetention takes the purge lock until the transaction ends,
// without waiting; false when another replica holds it. The key names the
// schema too, so instances sharing a database in separate schemas purge
// independently.
func (s *Store) TryLockAuthRetention(ctx context.Context) (bool, error) {
	var held bool
	err := s.db(ctx).QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext('flagnetic.access.auth_retention@' || current_schema()))`).Scan(&held)
	return held, err
}

// Each purge selects a batch and deletes it with the condition checked
// again, so a row a concurrent statement changed since, such as a failure
// counted against an email at that moment, is left alone.

// PurgeSessions deletes at most limit sessions the cutoffs end.
func (s *Store) PurgeSessions(ctx context.Context, cut access.SessionCutoffs, limit int) (int, error) {
	const ended = `(revoked_at IS NOT NULL OR last_seen_at <= $1 OR created_at <= $2 OR expires_at <= $3)`
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM sessions WHERE `+ended+`
		AND id IN (SELECT id FROM sessions WHERE `+ended+` LIMIT $4)`,
		cut.LastSeenBy, cut.CreatedBy, cut.ExpiresBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging ended sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeEmailFailures deletes at most limit emails' failures that no lock
// holds and whose last failure is at or before the cutoff.
func (s *Store) PurgeEmailFailures(ctx context.Context, cut access.FailureCutoffs, limit int) (int, error) {
	const stale = `((locked_until IS NULL OR locked_until <= $1) AND last_failure_at <= $2)`
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM sign_in_email_failures WHERE `+stale+`
		AND email IN (SELECT email FROM sign_in_email_failures WHERE `+stale+` LIMIT $3)`,
		cut.Now, cut.LastEmailFailureBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging email failures: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeSourceFailures deletes at most limit sources' failures whose window
// opened at or before the cutoff.
func (s *Store) PurgeSourceFailures(ctx context.Context, cut access.FailureCutoffs, limit int) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM sign_in_source_failures WHERE window_start <= $1
		AND source IN (SELECT source FROM sign_in_source_failures WHERE window_start <= $1 LIMIT $2)`,
		cut.SourceWindowStartBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging source failures: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeKnownSourceFailures deletes at most limit failures of emails from
// known sources that no lock holds and whose last failure is at or before
// the cutoff, as PurgeEmailFailures does an email's own.
func (s *Store) PurgeKnownSourceFailures(ctx context.Context, cut access.FailureCutoffs, limit int) (int, error) {
	const stale = `((locked_until IS NULL OR locked_until <= $1) AND last_failure_at <= $2)`
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM sign_in_known_source_failures WHERE `+stale+`
		AND (email, source) IN (SELECT email, source FROM sign_in_known_source_failures WHERE `+stale+` LIMIT $3)`,
		cut.Now, cut.LastEmailFailureBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging known source failures: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeKnownSources deletes at most limit sources their members last signed
// in from at or before the cutoff: they are no longer known.
func (s *Store) PurgeKnownSources(ctx context.Context, cut access.FailureCutoffs, limit int) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM sign_in_known_sources WHERE signed_in_at <= $1
		AND (member_id, source) IN (SELECT member_id, source FROM sign_in_known_sources WHERE signed_in_at <= $1 LIMIT $2)`,
		cut.KnownSourceBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging known sources: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
