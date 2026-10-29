// Package identity holds the use cases of the identity context: the auth
// providers an instance admin configures and, as they arrive, the sign-ins,
// identities and directory provisioning built on them.
package identity

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ErrNotFound is what a store answers for a provider it does not hold.
var ErrNotFound = errors.New("identity: not found")

var (
	errProviderNotFound = apperrors.NotFound("not_found", "the instance has no auth provider with this slug")

	// ErrBaseURLRequired refuses configuring a provider on an instance that
	// does not know its own public address: the provider's redirect URI, which
	// the identity provider is told, is built from it.
	ErrBaseURLRequired = apperrors.Invalid("base_url_required",
		"an auth provider's URLs are built from the instance's public URL; set FLAGNETIC_BASE_URL")
)

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// Sealer seals a value under the instance's secret key.
type Sealer interface {
	Seal(purpose secret.Purpose, plaintext []byte) (string, error)
}

// ProviderStore keeps auth providers.
type ProviderStore interface {
	// Providers returns every provider, ordered by display name.
	Providers(ctx context.Context) ([]identity.Provider, error)
	// EnabledProviders returns the enabled providers, ordered by display name.
	EnabledProviders(ctx context.Context) ([]identity.Provider, error)
	// Provider returns the provider with slug, or ErrNotFound.
	Provider(ctx context.Context, slug string) (identity.Provider, error)
	// LockProvider is Provider, locking the provider's row until the unit of
	// work ctx carries ends.
	LockProvider(ctx context.Context, slug string) (identity.Provider, error)
	// CreateProvider stores p, answering a taken slug as a conflict.
	CreateProvider(ctx context.Context, p identity.Provider) error
	// UpdateProvider stores p in place of the provider with its identifier:
	// everything an edit changes, and the sealed client secret.
	UpdateProvider(ctx context.Context, p identity.Provider) error
	// SetProviderEnabled stores p's enabled flag, update time and version.
	SetProviderEnabled(ctx context.Context, p identity.Provider) error
	DeleteProvider(ctx context.Context, provider identity.ProviderID) error
}

// ProviderReference is something that depends on an auth provider, such as
// the identities linked through it. Each context that holds one adds its own
// to the deletion check.
type ProviderReference interface {
	// ProviderUses names what refers to provider, such as "identities";
	// nothing when nothing does. It reads in the unit of work ctx carries.
	ProviderUses(ctx context.Context, provider identity.ProviderID) ([]string, error)
}

// ProviderDeps are what the auth provider use cases run on.
type ProviderDeps struct {
	Tx    Transactor
	Store ProviderStore
	Audit AuditLog
	Clock clock.Clock
	IDs   *id.Source
	// Sealer seals client secrets.
	Sealer Sealer
	// BaseURL is the instance's public origin, nil when not configured.
	BaseURL *url.URL
	// References are what keeps a provider from being deleted.
	References []ProviderReference
}

func (d ProviderDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

// requireBaseURL refuses a write to a provider while the instance does not
// know its public address.
func (d ProviderDeps) requireBaseURL() error {
	if d.BaseURL == nil {
		return ErrBaseURLRequired
	}
	return nil
}

func providerOf(ctx context.Context, store ProviderStore, slug string) (identity.Provider, error) {
	p, err := store.Provider(ctx, slug)
	if errors.Is(err, ErrNotFound) {
		return p, errProviderNotFound
	}
	return p, err
}

func lockProvider(ctx context.Context, store ProviderStore, slug string) (identity.Provider, error) {
	p, err := store.LockProvider(ctx, slug)
	if errors.Is(err, ErrNotFound) {
		return p, errProviderNotFound
	}
	return p, err
}

// redacted stands for a provider's client secret in its audit values, so an
// auditor sees there is one and never reads it.
const redacted = "[redacted]"

// providerValues returns what the audit log records of p.
func providerValues(p identity.Provider) map[string]any {
	v := map[string]any{
		"id": p.ID.String(), "slug": p.Slug, "kind": string(p.Kind), "display_name": p.DisplayName, "enabled": p.Enabled,
		"domains": p.Domains, "link_by_email": p.LinkByEmail, "auto_provision": p.AutoProvision,
	}
	if o := p.OIDC; o != nil {
		v["issuer"], v["client_id"], v["client_secret"] = o.Issuer, o.ClientID, redacted
		v["scopes"], v["require_verified_email"] = o.Scopes, o.RequireVerifiedEmail
		v["claims"] = map[string]any{"email": o.Claims.Email, "name": o.Claims.Name, "groups": o.Claims.Groups}
	}
	return v
}

// record writes the audit entry of action on a provider, made at now in req.
func (d ProviderDeps) record(ctx context.Context, now time.Time, req governance.Request, action string,
	before, after *identity.Provider,
) error {
	p := after
	if p == nil {
		p = before
	}
	r := governance.Record{Action: action, Entity: governance.Entity{Kind: "auth_provider", ID: p.ID.String(), Key: p.Slug}}
	if before != nil {
		r.Before = providerValues(*before)
	}
	if after != nil {
		r.After = providerValues(*after)
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}

// ListProviders answers the instance's auth providers.
type ListProviders struct{ store ProviderStore }

// NewListProviders returns the query.
func NewListProviders(store ProviderStore) *ListProviders { return &ListProviders{store: store} }

// Handle returns every provider, ordered by display name.
func (h *ListProviders) Handle(ctx context.Context) ([]identity.Provider, error) {
	return h.store.Providers(ctx)
}

// GetProvider answers one auth provider.
type GetProvider struct{ store ProviderStore }

// NewGetProvider returns the query.
func NewGetProvider(store ProviderStore) *GetProvider { return &GetProvider{store: store} }

// Handle returns the provider with slug, or not_found.
func (h *GetProvider) Handle(ctx context.Context, slug string) (identity.Provider, error) {
	return providerOf(ctx, h.store, slug)
}

// ListEnabledProviders answers the providers the sign-in page offers.
type ListEnabledProviders struct{ store ProviderStore }

// NewListEnabledProviders returns the query.
func NewListEnabledProviders(store ProviderStore) *ListEnabledProviders {
	return &ListEnabledProviders{store: store}
}

// Handle returns the enabled providers, ordered by display name.
func (h *ListEnabledProviders) Handle(ctx context.Context) ([]identity.Provider, error) {
	return h.store.EnabledProviders(ctx)
}

// CreateProviderCommand asks for an auth provider.
type CreateProviderCommand struct {
	Input   identity.ProviderInput
	Request governance.Request
}

// CreateProvider creates an auth provider.
type CreateProvider struct{ d ProviderDeps }

// NewCreateProvider returns the handler.
func NewCreateProvider(d ProviderDeps) *CreateProvider { return &CreateProvider{d: d} }

// Handle checks the provider, seals its client secret, stores it and records
// auth_provider.created without the secret.
func (h *CreateProvider) Handle(ctx context.Context, cmd CreateProviderCommand) (identity.Provider, error) {
	if err := h.d.requireBaseURL(); err != nil {
		return identity.Provider{}, err
	}
	in, err := identity.CheckProviderInput(cmd.Input)
	if err != nil {
		return identity.Provider{}, err
	}
	sealed, err := h.d.Sealer.Seal(secret.PurposeOIDCClientSecret, []byte(in.OIDC.ClientSecret.Reveal()))
	if err != nil {
		return identity.Provider{}, err
	}
	now := h.d.now()
	p := identity.New(id.New[identity.Provider](h.d.IDs), in, sealed, now)
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if txErr := h.d.Store.CreateProvider(ctx, p); txErr != nil {
			return txErr
		}
		return h.d.record(ctx, now, cmd.Request, "auth_provider.created", nil, &p)
	})
	return p, err
}

// UpdateProviderCommand is an edit of the provider Slug, based on its version
// Expected.
type UpdateProviderCommand struct {
	Slug     string
	Change   identity.ProviderChange
	Expected precondition.Version
	Request  governance.Request
}

// UpdateProvider edits an auth provider.
type UpdateProvider struct{ d ProviderDeps }

// NewUpdateProvider returns the handler.
func NewUpdateProvider(d ProviderDeps) *UpdateProvider { return &UpdateProvider{d: d} }

// Handle applies the change to the provider read under its lock, sealing a
// new client secret, and records auth_provider.updated; an edit that changes
// nothing stores and records nothing. Enabling and disabling are operations
// of their own.
func (h *UpdateProvider) Handle(ctx context.Context, cmd UpdateProviderCommand) (identity.Provider, error) {
	if err := h.d.requireBaseURL(); err != nil {
		return identity.Provider{}, err
	}
	var sealed string
	if cmd.Change.ClientSecret != nil {
		if err := identity.CheckClientSecret(*cmd.Change.ClientSecret); err != nil {
			return identity.Provider{}, err
		}
		var err error
		if sealed, err = h.d.Sealer.Seal(secret.PurposeOIDCClientSecret, []byte(cmd.Change.ClientSecret.Reveal())); err != nil {
			return identity.Provider{}, err
		}
	}
	now := h.d.now()
	var out identity.Provider
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockProvider(ctx, h.d.Store, cmd.Slug)
		if err != nil {
			return err
		}
		out = before
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		after, err := before.CheckChange(cmd.Change)
		if err != nil {
			return err
		}
		if sealed == "" && after.Same(before) {
			return nil
		}
		if sealed != "" {
			after.OIDC.SealedClientSecret = sealed
		}
		after.UpdatedAt, after.Version = now, before.Version+1
		if err = h.d.Store.UpdateProvider(ctx, after); err != nil {
			return err
		}
		out = after
		return h.d.record(ctx, now, cmd.Request, "auth_provider.updated", &before, &after)
	})
	return out, err
}

// ProviderCommand names the provider Slug, acted on in Request.
type ProviderCommand struct {
	Slug    string
	Request governance.Request
}

// EnableProvider enables an auth provider.
type EnableProvider struct{ d ProviderDeps }

// NewEnableProvider returns the handler.
func NewEnableProvider(d ProviderDeps) *EnableProvider { return &EnableProvider{d: d} }

// Handle enables the provider and records auth_provider.enabled; an enabled
// provider is returned as it is and nothing is recorded.
func (h *EnableProvider) Handle(ctx context.Context, cmd ProviderCommand) (identity.Provider, error) {
	if err := h.d.requireBaseURL(); err != nil {
		return identity.Provider{}, err
	}
	return setEnabled(ctx, h.d, cmd, true)
}

// DisableProvider disables an auth provider.
type DisableProvider struct{ d ProviderDeps }

// NewDisableProvider returns the handler.
func NewDisableProvider(d ProviderDeps) *DisableProvider { return &DisableProvider{d: d} }

// Handle disables the provider, which stops its sign-ins and leaves its
// members, identities and grants as they are, and records
// auth_provider.disabled; a disabled provider is returned as it is and
// nothing is recorded.
func (h *DisableProvider) Handle(ctx context.Context, cmd ProviderCommand) (identity.Provider, error) {
	return setEnabled(ctx, h.d, cmd, false)
}

func setEnabled(ctx context.Context, d ProviderDeps, cmd ProviderCommand, enabled bool) (identity.Provider, error) {
	now := d.now()
	var out identity.Provider
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockProvider(ctx, d.Store, cmd.Slug)
		if err != nil {
			return err
		}
		after, changed := before.Disable(now)
		action := "auth_provider.disabled"
		if enabled {
			after, changed = before.Enable(now)
			action = "auth_provider.enabled"
		}
		if out = after; !changed {
			return nil
		}
		if err = d.Store.SetProviderEnabled(ctx, after); err != nil {
			return err
		}
		return d.record(ctx, now, cmd.Request, action, &before, &after)
	})
	return out, err
}

// DeleteProvider deletes an auth provider.
type DeleteProvider struct{ d ProviderDeps }

// NewDeleteProvider returns the handler.
func NewDeleteProvider(d ProviderDeps) *DeleteProvider { return &DeleteProvider{d: d} }

// Handle deletes the provider and records auth_provider.deleted, unless
// something refers to it: then it is refused provider_in_use, naming what
// does in used_by. A provider in use is disabled instead.
func (h *DeleteProvider) Handle(ctx context.Context, cmd ProviderCommand) error {
	now := h.d.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockProvider(ctx, h.d.Store, cmd.Slug)
		if err != nil {
			return err
		}
		var uses []string
		for _, ref := range h.d.References {
			u, usesErr := ref.ProviderUses(ctx, before.ID)
			if usesErr != nil {
				return usesErr
			}
			uses = append(uses, u...)
		}
		if len(uses) > 0 {
			return apperrors.Conflict("provider_in_use",
				"something depends on the auth provider; disable it instead of deleting it").WithExtension("used_by", uses)
		}
		if err = h.d.Store.DeleteProvider(ctx, before.ID); err != nil {
			return err
		}
		return h.d.record(ctx, now, cmd.Request, "auth_provider.deleted", &before, nil)
	})
}
