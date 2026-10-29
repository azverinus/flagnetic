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
	token_expiry_required, password_min_length`

// AuthSettings returns the instance's auth settings as stored.
func (s *Store) AuthSettings(ctx context.Context) (access.AuthSettings, error) {
	return s.readAuthSettings(ctx, `SELECT `+authSettingsColumns+` FROM auth_settings`)
}

// LockAuthSettings returns the auth settings and locks them until the
// transaction ends, so two changes made at once each see the other's
// outcome.
func (s *Store) LockAuthSettings(ctx context.Context) (access.AuthSettings, error) {
	return s.readAuthSettings(ctx, `SELECT `+authSettingsColumns+` FROM auth_settings FOR UPDATE`)
}

func (s *Store) readAuthSettings(ctx context.Context, query string) (access.AuthSettings, error) {
	var (
		idle, absolute, lifetime int64
		out                      access.AuthSettings
	)
	err := s.db(ctx).QueryRow(ctx, query).Scan(&idle, &absolute, &lifetime, &out.TokenExpiryRequired, &out.PasswordMinLength)
	if err != nil {
		return access.AuthSettings{}, fmt.Errorf("reading the auth settings: %w", err)
	}
	out.SessionIdleTimeout = time.Duration(idle) * time.Second
	out.SessionAbsoluteTimeout = time.Duration(absolute) * time.Second
	out.TokenMaxLifetime = time.Duration(lifetime) * time.Second
	return out, nil
}

// SaveAuthSettings stores settings and announces the change on
// AuthSettingsChannel, which PostgreSQL delivers once the transaction
// commits and never if it rolls back.
func (s *Store) SaveAuthSettings(ctx context.Context, settings access.AuthSettings) error {
	_, err := s.db(ctx).Exec(ctx, `
		WITH saved AS (
			UPDATE auth_settings SET session_idle_timeout_seconds = $1, session_absolute_timeout_seconds = $2,
				token_max_lifetime_seconds = $3, token_expiry_required = $4, password_min_length = $5
			RETURNING 1
		)
		SELECT pg_notify($6, '') FROM saved`,
		int64(settings.SessionIdleTimeout/time.Second), int64(settings.SessionAbsoluteTimeout/time.Second),
		int64(settings.TokenMaxLifetime/time.Second), settings.TokenExpiryRequired, settings.PasswordMinLength,
		AuthSettingsChannel)
	if err != nil {
		return fmt.Errorf("saving the auth settings: %w", err)
	}
	return nil
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
