package access

import (
	"context"
	"fmt"
	"sync"
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
// every request judging a session reads. It trusts its copy only while its
// listener on AuthSettingsChannel is up: a change announced while it is
// down may be missed, so until it is listening again every read goes to
// PostgreSQL.
type AuthSettingsCache struct {
	store *Store

	mu        sync.Mutex
	listening bool
	// generation counts what may have made a read stale: a notification, or
	// the listener going up or down. A read is kept only when no such event
	// happened while it was under way.
	generation uint64
	cached     *access.AuthSettings
}

// NewAuthSettingsCache returns a cache of the settings on pool, which reads
// through until its listener reports itself up.
func NewAuthSettingsCache(pool *pgxpool.Pool) *AuthSettingsCache {
	return &AuthSettingsCache{store: NewStore(pool)}
}

// AuthSettings returns the auth settings, from the copy while it can be
// trusted.
func (c *AuthSettingsCache) AuthSettings(ctx context.Context) (access.AuthSettings, error) {
	c.mu.Lock()
	if c.cached != nil {
		v := *c.cached
		c.mu.Unlock()
		return v, nil
	}
	generation := c.generation
	c.mu.Unlock()
	v, err := c.store.AuthSettings(ctx)
	if err != nil {
		return access.AuthSettings{}, err
	}
	c.mu.Lock()
	if c.listening && c.generation == generation {
		c.cached = &v
	}
	c.mu.Unlock()
	return v, nil
}

// Forget drops the copy, as a change made by this replica does once it is
// committed, so the replica's next read sees it without waiting for the
// notification.
func (c *AuthSettingsCache) Forget() { c.invalidate(nil) }

// Listening is told when the listener starts or stops listening.
func (c *AuthSettingsCache) Listening(up bool) { c.invalidate(&up) }

// Received is told of each change announced.
func (c *AuthSettingsCache) Received(string) { c.invalidate(nil) }

func (c *AuthSettingsCache) invalidate(listening *bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if listening != nil {
		c.listening = *listening
	}
	c.generation++
	c.cached = nil
}
