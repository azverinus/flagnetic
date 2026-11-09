package access

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// AuthSettingsChannel is the notification channel a change of the auth
// settings is announced on, so every replica drops what it cached.
const AuthSettingsChannel = "auth_settings"

const authSettingsColumns = `session_idle_timeout_seconds, session_absolute_timeout_seconds, token_max_lifetime_seconds,
	token_expiry_required, password_min_length, self_service_password_reset, local_login_enabled,
	coalesce((SELECT array_agg(domain ORDER BY position) FROM sso_enforced_domains), '{}'),
	coalesce((SELECT array_agg(provider ORDER BY position) FROM sso_enforced_domains), '{}')`

// AuthSettings returns the instance's auth settings as stored.
func (s *Store) AuthSettings(ctx context.Context) (access.AuthSettings, error) {
	return s.readAuthSettings(ctx, `SELECT `+authSettingsColumns+` FROM auth_settings`)
}

// LockAuthSettings returns the auth settings and locks them until the
// transaction ends, so two changes made at once each see the other's
// outcome. The enforced domains are locked with the row: every change to
// them takes this lock first.
func (s *Store) LockAuthSettings(ctx context.Context) (access.AuthSettings, error) {
	return s.readAuthSettings(ctx, `SELECT `+authSettingsColumns+` FROM auth_settings FOR UPDATE`)
}

func (s *Store) readAuthSettings(ctx context.Context, query string) (access.AuthSettings, error) {
	var (
		idle, absolute, lifetime int64
		domains, providers       []string
		out                      access.AuthSettings
	)
	err := s.db(ctx).QueryRow(ctx, query).Scan(&idle, &absolute, &lifetime, &out.TokenExpiryRequired, &out.PasswordMinLength,
		&out.SelfServicePasswordReset, &out.LocalLoginEnabled, &domains, &providers)
	if err != nil {
		return access.AuthSettings{}, fmt.Errorf("reading the auth settings: %w", err)
	}
	for i, d := range domains {
		out.SSOEnforcedDomains = append(out.SSOEnforcedDomains, access.EnforcedDomain{Domain: d, Provider: providers[i]})
	}
	out.SessionIdleTimeout = time.Duration(idle) * time.Second
	out.SessionAbsoluteTimeout = time.Duration(absolute) * time.Second
	out.TokenMaxLifetime = time.Duration(lifetime) * time.Second
	return out, nil
}

// SaveAuthSettings stores settings, the enforced domains replacing those
// stored, and announces the change on AuthSettingsChannel, which PostgreSQL
// delivers once the transaction commits and never if it rolls back. It runs
// in the transaction ctx carries, which holds the settings locked.
func (s *Store) SaveAuthSettings(ctx context.Context, settings access.AuthSettings) error {
	domains := make([]string, len(settings.SSOEnforcedDomains))
	providers := make([]string, len(settings.SSOEnforcedDomains))
	for i, d := range settings.SSOEnforcedDomains {
		domains[i], providers[i] = d.Domain, d.Provider
	}
	db := s.db(ctx)
	if _, err := db.Exec(ctx, `DELETE FROM sso_enforced_domains`); err != nil {
		return fmt.Errorf("saving the enforced domains: %w", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO sso_enforced_domains (domain, provider, position)
		SELECT d, p, n - 1 FROM unnest($1::text[], $2::text[]) WITH ORDINALITY AS e (d, p, n)`, domains, providers); err != nil {
		return fmt.Errorf("saving the enforced domains: %w", err)
	}
	_, err := db.Exec(ctx, `
		WITH saved AS (
			UPDATE auth_settings SET session_idle_timeout_seconds = $1, session_absolute_timeout_seconds = $2,
				token_max_lifetime_seconds = $3, token_expiry_required = $4, password_min_length = $5,
				self_service_password_reset = $6, local_login_enabled = $7
			RETURNING 1
		)
		SELECT pg_notify($8, '') FROM saved`,
		int64(settings.SessionIdleTimeout/time.Second), int64(settings.SessionAbsoluteTimeout/time.Second),
		int64(settings.TokenMaxLifetime/time.Second), settings.TokenExpiryRequired, settings.PasswordMinLength, settings.SelfServicePasswordReset,
		settings.LocalLoginEnabled, AuthSettingsChannel)
	if err != nil {
		return fmt.Errorf("saving the auth settings: %w", err)
	}
	return nil
}

// ClearAllSignInFailures forgets every failed sign-in counted, per email, per
// email and source, and per source, and returns how many emails were locked
// out at the moment at, alone or from some source: an email locked both ways
// counts once, and a lockout already over counts not at all.
func (s *Store) ClearAllSignInFailures(ctx context.Context, at time.Time) (int, error) {
	var locked int
	err := s.db(ctx).QueryRow(ctx, `
		WITH emails AS (DELETE FROM sign_in_email_failures RETURNING email, locked_until),
		known AS (DELETE FROM sign_in_known_source_failures RETURNING email, locked_until),
		-- A data-modifying statement in WITH runs to completion whether or
		-- not the query reads it.
		sources AS (DELETE FROM sign_in_source_failures)
		SELECT count(DISTINCT email) FROM (
			SELECT email, locked_until FROM emails
			UNION ALL
			SELECT email, locked_until FROM known
		) locked
		WHERE locked_until > $1`, at).Scan(&locked)
	if err != nil {
		return 0, fmt.Errorf("clearing the failed sign-ins: %w", err)
	}
	return locked, nil
}

// AuthSettingsCache holds a replica's copy of the auth settings, which
// every request judging a session reads, trusted only while its listener on
// AuthSettingsChannel is up.
type AuthSettingsCache struct {
	*listenedCache[access.AuthSettings]
}

// NewAuthSettingsCache returns a cache of the settings on pool, which reads
// through until its listener reports itself up.
func NewAuthSettingsCache(pool *pgxpool.Pool) *AuthSettingsCache {
	store := NewStore(pool)
	return &AuthSettingsCache{newListenedCache(store.AuthSettings)}
}

// AuthSettings returns the auth settings, from the copy while it can be
// trusted.
func (c *AuthSettingsCache) AuthSettings(ctx context.Context) (access.AuthSettings, error) {
	return c.get(ctx)
}
