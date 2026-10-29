package access

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ErrSMTPNotConfigured is the instance having no SMTP server to read or
// remove.
var ErrSMTPNotConfigured = apperrors.NotFound("not_found", "no SMTP server is configured")

var errBaseURLRequired = apperrors.Invalid("base_url_required",
	"FLAGNETIC_BASE_URL must be set before SMTP is configured: every mailed link is built from it")

// SMTPReader reads the SMTP settings in force, ErrSMTPNotConfigured when
// there are none.
type SMTPReader interface {
	SMTPSettings(ctx context.Context) (access.SMTPSettings, error)
}

// SMTPStore keeps the SMTP settings.
type SMTPStore interface {
	// LockSMTPSettings returns the settings and locks them until the
	// transaction ends; ErrSMTPNotConfigured when there are none.
	LockSMTPSettings(ctx context.Context) (access.SMTPSettings, error)
	// SaveSMTPSettings stores the settings, replacing any, and announces the
	// change to every replica once the transaction commits.
	SaveSMTPSettings(ctx context.Context, s access.SMTPSettings) error
	// DeleteSMTPSettings removes the settings and announces it likewise.
	DeleteSMTPSettings(ctx context.Context) error
}

// SMTPCache is a replica's copy of the SMTP settings.
type SMTPCache interface {
	// Forget drops the copy, so the replica's next read sees a change it
	// just made.
	Forget()
}

// Sealer seals a value under the instance's secret key.
type Sealer interface {
	Seal(purpose secret.Purpose, plaintext []byte) (string, error)
}

// Opener opens a value sealed under the instance's secret key.
type Opener interface {
	Open(purpose secret.Purpose, sealed string) ([]byte, error)
}

// GetSMTP answers the SMTP settings in force.
type GetSMTP struct{ settings SMTPReader }

// NewGetSMTP returns the query.
func NewGetSMTP(settings SMTPReader) *GetSMTP { return &GetSMTP{settings: settings} }

// Handle returns the settings, ErrSMTPNotConfigured when there are none.
func (h *GetSMTP) Handle(ctx context.Context) (access.SMTPSettings, error) {
	return h.settings.SMTPSettings(ctx)
}

// ConfigureSMTPDeps are what ConfigureSMTP needs.
type ConfigureSMTPDeps struct {
	Tx    Transactor
	Store SMTPStore
	Cache SMTPCache
	// Sealer seals the password.
	Sealer Sealer
	Audit  AuditLog
	Clock  clock.Clock
	IDs    *id.Source
	// BaseURLSet is whether the instance knows its public address
	// (FLAGNETIC_BASE_URL): without it no mailed link could be followed.
	BaseURLSet bool
}

// ConfigureSMTP sets the SMTP server the instance mails through.
type ConfigureSMTP struct{ d ConfigureSMTPDeps }

// NewConfigureSMTP returns the handler.
func NewConfigureSMTP(d ConfigureSMTPDeps) *ConfigureSMTP { return &ConfigureSMTP{d: d} }

// Handle checks the change, seals a new password, replaces the settings and
// records smtp.updated with the settings before and after, the password
// only as password_set. An omitted password stays as stored, unless the
// change drops the username, which drops the password with it. A change
// leaving the settings as they are stores and records nothing. It refuses
// base_url_required before anything else while the instance has no public
// address.
func (h *ConfigureSMTP) Handle(ctx context.Context, change access.SMTPChange, req governance.Request) (access.SMTPSettings, error) {
	if !h.d.BaseURLSet {
		return access.SMTPSettings{}, errBaseURLRequired
	}
	var out access.SMTPSettings
	changed := false
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.d.Store.LockSMTPSettings(ctx)
		configured := err == nil
		if err != nil && !errors.Is(err, ErrSMTPNotConfigured) {
			return err
		}
		checked, err := change.Check(configured && before.PasswordSet())
		if err != nil {
			return err
		}
		next := access.SMTPSettings{
			Host: checked.Host, Port: checked.Port, Security: checked.Security, Username: checked.Username,
			From: checked.From, UpdatedAt: before.UpdatedAt,
		}
		switch {
		case checked.Username == "":
		case checked.Password == nil:
			next.PasswordSealed = before.PasswordSealed
		default:
			// Sealing draws a fresh nonce, so a supplied password always
			// differs from the stored one.
			if next.PasswordSealed, err = h.d.Sealer.Seal(secret.PurposeSMTPPassword, []byte(*checked.Password)); err != nil {
				return err
			}
		}
		if configured && next == before {
			out = before
			return nil
		}
		changed = true
		next.UpdatedAt = h.d.Clock.Now()
		var beforeRecord any
		if configured {
			beforeRecord = smtpRecord(before)
		}
		entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), next.UpdatedAt, req, governance.Record{
			Action: "smtp.updated", Entity: smtpEntity, Before: beforeRecord, After: smtpRecord(next),
		})
		if err != nil {
			return err
		}
		if err := h.d.Store.SaveSMTPSettings(ctx, next); err != nil {
			return err
		}
		out = next
		return h.d.Audit.Record(ctx, entry)
	})
	if err != nil {
		return access.SMTPSettings{}, err
	}
	if changed {
		h.d.Cache.Forget()
	}
	return out, nil
}

// RemoveSMTP removes the SMTP server: the instance stops mailing.
type RemoveSMTP struct {
	tx    Transactor
	store SMTPStore
	cache SMTPCache
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewRemoveSMTP returns the handler.
func NewRemoveSMTP(tx Transactor, store SMTPStore, cache SMTPCache, audit AuditLog, c clock.Clock, ids *id.Source) *RemoveSMTP {
	return &RemoveSMTP{tx: tx, store: store, cache: cache, audit: audit, clock: c, ids: ids}
}

// Handle removes the settings and records smtp.deleted with them as they
// were, ErrSMTPNotConfigured when there are none.
func (h *RemoveSMTP) Handle(ctx context.Context, req governance.Request) error {
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.LockSMTPSettings(ctx)
		if err != nil {
			return err
		}
		entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), h.clock.Now(), req, governance.Record{
			Action: "smtp.deleted", Entity: smtpEntity, Before: smtpRecord(before),
		})
		if err != nil {
			return err
		}
		if err := h.store.DeleteSMTPSettings(ctx); err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	})
	if err != nil {
		return err
	}
	h.cache.Forget()
	return nil
}

var smtpEntity = governance.Entity{Kind: "smtp", ID: "instance", Key: "instance"}

// smtpRecord is s as audit entries show it, in the API's terms: the
// password only as whether one is stored.
func smtpRecord(s access.SMTPSettings) map[string]any {
	return map[string]any{
		"host": s.Host, "port": s.Port, "security": string(s.Security), "username": s.Username,
		"password_set": s.PasswordSet(), "from": s.From,
	}
}
