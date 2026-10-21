package access

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AuthSettingsReader reads the auth settings in force.
type AuthSettingsReader interface {
	AuthSettings(ctx context.Context) (access.AuthSettings, error)
}

// AuthSettingsStore keeps the auth settings.
type AuthSettingsStore interface {
	// LockAuthSettings returns the settings and locks them until the
	// transaction ends.
	LockAuthSettings(ctx context.Context) (access.AuthSettings, error)
	// SaveAuthSettings stores the settings and announces the change to
	// every replica once the transaction commits.
	SaveAuthSettings(ctx context.Context, s access.AuthSettings) error
}

// AuthSettingsCache is a replica's copy of the settings.
type AuthSettingsCache interface {
	// Forget drops the copy, so the replica's next read sees a change it
	// just made.
	Forget()
}

// GetAuthSettings answers the auth settings in force.
type GetAuthSettings struct{ settings AuthSettingsReader }

// NewGetAuthSettings returns the query.
func NewGetAuthSettings(settings AuthSettingsReader) *GetAuthSettings {
	return &GetAuthSettings{settings: settings}
}

// Handle returns the settings.
func (h *GetAuthSettings) Handle(ctx context.Context) (access.AuthSettings, error) {
	return h.settings.AuthSettings(ctx)
}

// UpdateAuthSettings replaces the auth settings.
type UpdateAuthSettings struct {
	tx    Transactor
	store AuthSettingsStore
	cache AuthSettingsCache
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewUpdateAuthSettings returns the handler.
func NewUpdateAuthSettings(tx Transactor, store AuthSettingsStore, cache AuthSettingsCache, audit AuditLog, c clock.Clock,
	ids *id.Source,
) *UpdateAuthSettings {
	return &UpdateAuthSettings{tx: tx, store: store, cache: cache, audit: audit, clock: c, ids: ids}
}

// Handle checks the settings, stores them and records auth_settings.updated
// with the settings before and after; settings equal to those in force
// change nothing and record nothing. Every replica applies a change once
// the notification reaches it; this one at once.
func (h *UpdateAuthSettings) Handle(ctx context.Context, s access.AuthSettings, req governance.Request) (access.AuthSettings, error) {
	if err := s.Check(); err != nil {
		return access.AuthSettings{}, err
	}
	changed := false
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.LockAuthSettings(ctx)
		if err != nil || before == s {
			return err
		}
		changed = true
		entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), h.clock.Now(), req, governance.Record{
			Action: "auth_settings.updated",
			Entity: governance.Entity{Kind: "auth_settings", ID: "instance", Key: "instance"},
			Before: authSettingsRecord(before),
			After:  authSettingsRecord(s),
		})
		if err != nil {
			return err
		}
		if err := h.store.SaveAuthSettings(ctx, s); err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	})
	if err != nil {
		return access.AuthSettings{}, err
	}
	if changed {
		h.cache.Forget()
	}
	return s, nil
}

// authSettingsRecord is s as audit entries show it, in the API's terms.
func authSettingsRecord(s access.AuthSettings) map[string]any {
	return map[string]any{
		"session_idle_timeout_seconds":     int64(s.SessionIdleTimeout / time.Second),
		"session_absolute_timeout_seconds": int64(s.SessionAbsoluteTimeout / time.Second),
		"token_max_lifetime_seconds":       int64(s.TokenMaxLifetime / time.Second),
		"token_expiry_required":            s.TokenExpiryRequired,
		"password_min_length":              s.PasswordMinLength,
	}
}
