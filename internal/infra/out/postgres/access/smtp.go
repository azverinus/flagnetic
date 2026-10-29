package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
)

// SMTPSettingsChannel is the notification channel a change of the SMTP
// settings is announced on, so every replica drops what it cached.
const SMTPSettingsChannel = "smtp_settings"

const smtpColumns = `host, port, security, username, password_sealed, sender, updated_at`

// SMTPSettings returns the SMTP settings as stored, ErrSMTPNotConfigured
// when there are none.
func (s *Store) SMTPSettings(ctx context.Context) (access.SMTPSettings, error) {
	return s.readSMTP(ctx, `SELECT `+smtpColumns+` FROM smtp_settings`)
}

// LockSMTPSettings returns the SMTP settings and locks them until the
// transaction ends, so two changes made at once each see the other's
// outcome; ErrSMTPNotConfigured when there are none. Two first
// configurations at once are ordered by the row's primary key.
func (s *Store) LockSMTPSettings(ctx context.Context) (access.SMTPSettings, error) {
	return s.readSMTP(ctx, `SELECT `+smtpColumns+` FROM smtp_settings FOR UPDATE`)
}

func (s *Store) readSMTP(ctx context.Context, query string) (access.SMTPSettings, error) {
	var (
		out      access.SMTPSettings
		security string
	)
	err := s.db(ctx).QueryRow(ctx, query).Scan(&out.Host, &out.Port, &security, &out.Username, &out.PasswordSealed,
		&out.From, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.SMTPSettings{}, appaccess.ErrSMTPNotConfigured
	}
	if err != nil {
		return access.SMTPSettings{}, fmt.Errorf("reading the SMTP settings: %w", err)
	}
	out.Security = access.SMTPSecurity(security)
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

// SaveSMTPSettings stores settings, replacing any, and announces the change
// on SMTPSettingsChannel, which PostgreSQL delivers once the transaction
// commits and never if it rolls back.
func (s *Store) SaveSMTPSettings(ctx context.Context, settings access.SMTPSettings) error {
	_, err := s.db(ctx).Exec(ctx, `
		WITH saved AS (
			INSERT INTO smtp_settings (host, port, security, username, password_sealed, sender, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET host = excluded.host, port = excluded.port, security = excluded.security,
				username = excluded.username, password_sealed = excluded.password_sealed, sender = excluded.sender,
				updated_at = excluded.updated_at
			RETURNING 1
		)
		SELECT pg_notify($8, '') FROM saved`,
		settings.Host, settings.Port, string(settings.Security), settings.Username, settings.PasswordSealed,
		settings.From, settings.UpdatedAt, SMTPSettingsChannel)
	if err != nil {
		return fmt.Errorf("saving the SMTP settings: %w", err)
	}
	return nil
}

// DeleteSMTPSettings removes the SMTP settings and announces it on
// SMTPSettingsChannel.
func (s *Store) DeleteSMTPSettings(ctx context.Context) error {
	_, err := s.db(ctx).Exec(ctx, `
		WITH removed AS (DELETE FROM smtp_settings RETURNING 1)
		SELECT pg_notify($1, '') FROM removed`, SMTPSettingsChannel)
	if err != nil {
		return fmt.Errorf("removing the SMTP settings: %w", err)
	}
	return nil
}

// SMTPSettingsCache holds a replica's copy of the SMTP settings, trusted
// only while its listener on SMTPSettingsChannel is up. A replica with no
// settings holds that too.
type SMTPSettingsCache struct {
	*listenedCache[smtpRead]
}

// smtpRead is what the cache holds: the settings, or that there are none.
type smtpRead struct {
	settings   access.SMTPSettings
	configured bool
}

// NewSMTPSettingsCache returns a cache of the SMTP settings on pool, which
// reads through until its listener reports itself up.
func NewSMTPSettingsCache(pool *pgxpool.Pool) *SMTPSettingsCache {
	store := NewStore(pool)
	return &SMTPSettingsCache{newListenedCache(func(ctx context.Context) (smtpRead, error) {
		settings, err := store.SMTPSettings(ctx)
		if errors.Is(err, appaccess.ErrSMTPNotConfigured) {
			return smtpRead{}, nil
		}
		return smtpRead{settings: settings, configured: err == nil}, err
	})}
}

// SMTPSettings returns the SMTP settings, from the copy while it can be
// trusted, ErrSMTPNotConfigured when there are none.
func (c *SMTPSettingsCache) SMTPSettings(ctx context.Context) (access.SMTPSettings, error) {
	r, err := c.get(ctx)
	if err != nil {
		return access.SMTPSettings{}, err
	}
	if !r.configured {
		return access.SMTPSettings{}, appaccess.ErrSMTPNotConfigured
	}
	return r.settings, nil
}
