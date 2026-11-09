package access

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
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

// EnforcementProvider is an auth provider as enforcing a domain to it asks:
// whether it is enabled and the domains it vouches for.
type EnforcementProvider struct {
	Enabled bool
	// Domains are the email domains the provider vouches for, lowercase.
	Domains []string
}

// EnforcementProviders is what the access context asks of the identity
// context about the auth providers enforced domains name.
type EnforcementProviders interface {
	// EnforcementProvider returns the provider with the slug; false when
	// none has it.
	EnforcementProvider(ctx context.Context, slug string) (EnforcementProvider, bool, error)
}

// UpdateAuthSettings replaces the auth settings.
type UpdateAuthSettings struct {
	d UpdateAuthSettingsDeps
}

// UpdateAuthSettingsDeps are what replacing the auth settings works with.
type UpdateAuthSettingsDeps struct {
	Tx    Transactor
	Store AuthSettingsStore
	Cache AuthSettingsCache
	// Providers tell whether an enforced domain's provider may have it.
	Providers EnforcementProviders
	// Identities tell whether the caller holds an identity, as disabling
	// local login asks.
	Identities IdentityDirectory
	Audit      AuditLog
	Clock      clock.Clock
	IDs        *id.Source
}

// NewUpdateAuthSettings returns the handler.
func NewUpdateAuthSettings(d UpdateAuthSettingsDeps) *UpdateAuthSettings {
	return &UpdateAuthSettings{d: d}
}

// UpdateAuthSettingsCommand replaces the settings with Settings, on behalf
// of Caller.
type UpdateAuthSettingsCommand struct {
	Settings access.AuthSettings
	Caller   access.Caller
	Request  governance.Request
}

// Handle checks the settings, stores them and records auth_settings.updated
// with the settings before and after; settings equal to those in force
// change nothing and record nothing. Each enforced domain must name an
// enabled auth provider that vouches for it (invalid_auth_settings
// otherwise). Disabling local login is refused identity_required unless the
// caller holds an identity at an enabled provider that the new settings'
// enforcement still lets sign them in, so the instance admin who closes the
// door keeps a way through it. Every replica applies a change
// once the notification reaches it; this one at once.
func (h *UpdateAuthSettings) Handle(ctx context.Context, cmd UpdateAuthSettingsCommand) (access.AuthSettings, error) {
	s := cmd.Settings.Normalized()
	if err := s.Check(); err != nil {
		return access.AuthSettings{}, err
	}
	changed := false
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// The settings are locked before any provider is read: disabling or
		// deleting a provider takes the same lock first, so neither can
		// slip in between the check and the commit.
		before, err := h.d.Store.LockAuthSettings(ctx)
		if err != nil || before.Equal(s) {
			return err
		}
		if err = h.checkProviders(ctx, s.SSOEnforcedDomains); err != nil {
			return err
		}
		if before.LocalLoginEnabled && !s.LocalLoginEnabled {
			if err = h.requireIdentity(ctx, cmd.Caller, s); err != nil {
				return err
			}
		}
		changed = true
		entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), h.d.Clock.Now(), cmd.Request, governance.Record{
			Action: "auth_settings.updated",
			Entity: authSettingsEntity,
			Before: authSettingsRecord(before),
			After:  authSettingsRecord(s),
		})
		if err != nil {
			return err
		}
		if err := h.d.Store.SaveAuthSettings(ctx, s); err != nil {
			return err
		}
		return h.d.Audit.Record(ctx, entry)
	})
	if err != nil {
		return access.AuthSettings{}, err
	}
	if changed {
		h.d.Cache.Forget()
	}
	return s, nil
}

var authSettingsEntity = governance.Entity{Kind: "auth_settings", ID: "instance", Key: "instance"}

// checkProviders refuses, invalid_auth_settings naming each, an enforced
// domain whose provider is unknown or disabled, or does not vouch for it.
func (h *UpdateAuthSettings) checkProviders(ctx context.Context, domains []access.EnforcedDomain) error {
	var vs []apperrors.Violation
	for i, d := range domains {
		at := "/sso_enforced_domains/" + strconv.Itoa(i)
		p, found, err := h.d.Providers.EnforcementProvider(ctx, d.Provider)
		switch {
		case err != nil:
			return err
		case !found:
			vs = append(vs, apperrors.Violation{Pointer: at + "/provider", Code: "unknown_provider", Detail: "no auth provider has this slug"})
		case !p.Enabled:
			vs = append(vs, apperrors.Violation{Pointer: at + "/provider", Code: "provider_disabled",
				Detail: "the auth provider is disabled; enable it first"})
		case !slices.Contains(p.Domains, d.Domain):
			vs = append(vs, apperrors.Violation{Pointer: at + "/domain", Code: "not_provider_domain",
				Detail: "the auth provider does not vouch for this domain; add it to the provider's domains first"})
		}
	}
	if len(vs) > 0 {
		return apperrors.Invalid("invalid_auth_settings", "the auth settings are not valid", vs...)
	}
	return nil
}

// requireIdentity refuses identity_required a caller who holds no identity
// that signs them in under the settings s: one at an enabled auth provider
// and, when s enforces the caller's email domain, at the provider it is
// enforced to. An identity at another provider would be refused them the
// moment s is in force.
func (h *UpdateAuthSettings) requireIdentity(ctx context.Context, caller access.Caller, s access.AuthSettings) error {
	if caller.Member == nil {
		return access.ErrIdentityRequired
	}
	held, err := h.d.Identities.MemberIdentities(ctx, caller.Member.ID)
	if err != nil {
		return err
	}
	if !(access.AuthMethods{Identities: held}).UnderSettings(s, caller.Member.Email).HoldsUsableWayIn() {
		return access.ErrIdentityRequired
	}
	return nil
}

// authSettingsRecord is s as audit entries show it, in the API's terms.
func authSettingsRecord(s access.AuthSettings) map[string]any {
	return map[string]any{
		"session_idle_timeout_seconds":     int64(s.SessionIdleTimeout / time.Second),
		"session_absolute_timeout_seconds": int64(s.SessionAbsoluteTimeout / time.Second),
		"token_max_lifetime_seconds":       int64(s.TokenMaxLifetime / time.Second),
		"token_expiry_required":            s.TokenExpiryRequired,
		"password_min_length":              s.PasswordMinLength,
		"self_service_password_reset":      s.SelfServicePasswordReset,
		"local_login_enabled":              s.LocalLoginEnabled,
		"sso_enforced_domains":             enforcedDomainsRecord(s.SSOEnforcedDomains),
	}
}

func enforcedDomainsRecord(domains []access.EnforcedDomain) []map[string]any {
	out := make([]map[string]any, 0, len(domains))
	for _, d := range domains {
		out = append(out, map[string]any{"domain": d.Domain, "provider": d.Provider})
	}
	return out
}
