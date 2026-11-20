package identity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// SCIMTokenRecord is a SCIM token with the slug of the provider it is bound
// to.
type SCIMTokenRecord struct {
	identity.SCIMToken
	ProviderSlug string
}

// scimTokenBinding is what a SCIM token's sealed hash is bound to: the row,
// the provider the token acts for and when it expires, so one copied into
// another token's row does not open there, and a token whose provider or
// expiry was changed in the database no longer opens.
func scimTokenBinding(t identity.SCIMToken) secret.Binding {
	expires := "never"
	if t.ExpiresAt != nil {
		expires = t.ExpiresAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	}
	return secret.Row("scim_tokens", t.ID.String()+"/"+t.Provider.String()+"/"+expires)
}

// legacySCIMTokenBinding is what a hash sealed before the provider and the
// expiry were bound is bound to: the row alone. Such a hash still opens, and
// is sealed again on its first use.
func legacySCIMTokenBinding(t identity.SCIMTokenID) secret.Binding {
	return secret.Row("scim_tokens", t.String())
}

// SealedSCIMToken is a live SCIM token as authentication reads it: its hash
// still sealed, and whether its provider is enabled.
type SealedSCIMToken struct {
	SCIMTokenRecord
	SealedHash      string
	ProviderEnabled bool
}

// SCIMTokenStore keeps SCIM tokens.
type SCIMTokenStore interface {
	// CreateSCIMToken stores t with its hash sealed.
	CreateSCIMToken(ctx context.Context, t identity.SCIMToken, sealedHash string) error
	// SCIMTokens returns every SCIM token, the newest first.
	SCIMTokens(ctx context.Context) ([]SCIMTokenRecord, error)
	// RevokeSCIMToken revokes the token at at and returns it; ErrNotFound
	// unless it exists unrevoked.
	RevokeSCIMToken(ctx context.Context, token identity.SCIMTokenID, at time.Time) (SCIMTokenRecord, error)
	// AuthenticableSCIMTokens returns every token not revoked, and those
	// revoked at or after revokedSince, with its sealed
	// hash: the few an instance holds, all of which authentication opens.
	AuthenticableSCIMTokens(ctx context.Context, revokedSince time.Time) ([]SealedSCIMToken, error)
	// ResealSCIMToken replaces the sealed hash of the token.
	ResealSCIMToken(ctx context.Context, token identity.SCIMTokenID, sealedHash string) error
	// RecordSCIMTokenUse records that token was presented at at, unless a use
	// not before notBefore is recorded already, so replicas coalesce.
	RecordSCIMTokenUse(ctx context.Context, token identity.SCIMTokenID, at, notBefore time.Time) error
}

// SCIMTokenDeps are what the SCIM token use cases run on.
type SCIMTokenDeps struct {
	Tx        Transactor
	Providers ProviderStore
	Store     SCIMTokenStore
	Audit     AuditLog
	Clock     clock.Clock
	IDs       *id.Source
	Random    io.Reader
	Sealer    Sealer
}

func (d SCIMTokenDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

func scimTokenEntity(r SCIMTokenRecord) governance.Entity {
	return governance.Entity{Kind: "scim_token", ID: r.ID.String(), Key: r.Hint}
}

// scimTokenValues is what the audit log keeps of a SCIM token: its hint and
// provider, never its value.
func scimTokenValues(r SCIMTokenRecord) map[string]any {
	v := map[string]any{"provider": r.ProviderSlug, "hint": r.Hint, "expires_at": nil}
	if r.ExpiresAt != nil {
		v["expires_at"] = r.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return v
}

func (d SCIMTokenDeps) record(ctx context.Context, now time.Time, req governance.Request, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}

// ListSCIMTokens answers the instance's SCIM tokens.
type ListSCIMTokens struct{ store SCIMTokenStore }

// NewListSCIMTokens returns the query.
func NewListSCIMTokens(store SCIMTokenStore) *ListSCIMTokens { return &ListSCIMTokens{store: store} }

// Handle returns every SCIM token, the newest first, revoked ones included.
func (h *ListSCIMTokens) Handle(ctx context.Context) ([]SCIMTokenRecord, error) {
	return h.store.SCIMTokens(ctx)
}

// CreateSCIMToken creates a SCIM token.
type CreateSCIMToken struct{ d SCIMTokenDeps }

// NewCreateSCIMToken returns the handler.
func NewCreateSCIMToken(d SCIMTokenDeps) *CreateSCIMToken { return &CreateSCIMToken{d: d} }

// CreateSCIMTokenCommand asks for a SCIM token bound to the provider with
// Provider as its slug.
type CreateSCIMTokenCommand struct {
	Provider  string
	ExpiresAt *time.Time
	Request   governance.Request
}

// CreatedSCIMToken is a SCIM token just created, with its value, shown this
// once.
type CreatedSCIMToken struct {
	Token SCIMTokenRecord
	Value secret.String
}

// Handle creates the token and records scim_token.created with its hint and
// provider. A provider that does not exist, or an expiry that is not in the
// future, is refused invalid_scim_token, naming the field.
func (h *CreateSCIMToken) Handle(ctx context.Context, cmd CreateSCIMTokenCommand) (CreatedSCIMToken, error) {
	now := h.d.now()
	var out CreatedSCIMToken
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		provider, err := h.d.Providers.LockProvider(ctx, cmd.Provider)
		if errors.Is(err, ErrNotFound) {
			return apperrors.Invalid("invalid_scim_token", "the SCIM token is not valid", apperrors.Violation{
				Pointer: "/provider", Code: "unknown_provider", Detail: "the instance has no auth provider with this slug",
			})
		}
		if err != nil {
			return err
		}
		// The database keeps microseconds, and the seal binds what it keeps.
		var expiresAt *time.Time
		if cmd.ExpiresAt != nil {
			expiresAt = new(cmd.ExpiresAt.UTC().Truncate(time.Microsecond))
		}
		token, value, err := identity.NewSCIMToken(id.New[identity.SCIMToken](h.d.IDs), provider.ID, expiresAt, now, h.d.Random)
		if err != nil {
			return err
		}
		sealed, err := h.d.Sealer.Seal(secret.PurposeSCIMToken, token.Hash[:], scimTokenBinding(token))
		if err != nil {
			return err
		}
		if err = h.d.Store.CreateSCIMToken(ctx, token, sealed); err != nil {
			return err
		}
		rec := SCIMTokenRecord{SCIMToken: token, ProviderSlug: provider.Slug}
		out = CreatedSCIMToken{Token: rec, Value: value.Value}
		return h.d.record(ctx, now, cmd.Request, governance.Record{
			Action: "scim_token.created", Entity: scimTokenEntity(rec), After: scimTokenValues(rec),
		})
	})
	return out, err
}

// ErrSCIMTokenNotFound answers a SCIM token that is not an unrevoked one.
var ErrSCIMTokenNotFound = apperrors.NotFound("not_found", "no unrevoked SCIM token has this identifier")

// RevokeSCIMToken revokes a SCIM token.
type RevokeSCIMToken struct{ d SCIMTokenDeps }

// NewRevokeSCIMToken returns the handler.
func NewRevokeSCIMToken(d SCIMTokenDeps) *RevokeSCIMToken { return &RevokeSCIMToken{d: d} }

// Handle revokes the token and records scim_token.revoked; a token already
// revoked, or unknown, is ErrSCIMTokenNotFound.
func (h *RevokeSCIMToken) Handle(ctx context.Context, token identity.SCIMTokenID, req governance.Request) error {
	now := h.d.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		rec, err := h.d.Store.RevokeSCIMToken(ctx, token, now)
		if errors.Is(err, ErrNotFound) {
			return ErrSCIMTokenNotFound
		}
		if err != nil {
			return err
		}
		after := scimTokenValues(rec)
		after["revoked_at"] = now.Format(time.RFC3339)
		return h.d.record(ctx, now, req, governance.Record{
			Action: "scim_token.revoked", Entity: scimTokenEntity(rec),
			Before: map[string]any{"revoked_at": nil}, After: after,
		})
	})
}

// SourceThrottle counts a source's failed attempts, shared with sign-in,
// invitation acceptance and password reset, so that guessing at one cannot
// be traded for guessing at another.
type SourceThrottle interface {
	// Reserve counts one failure of the source ip comes from at at, for a
	// value that matched nothing. A throttled source is refused rate_limited,
	// its failure taken back.
	Reserve(ctx context.Context, ip netip.Addr, at time.Time) error
}

// SCIMAuthenticator resolves a bearer value presented to /scim/v2.
type SCIMAuthenticator struct {
	d        SCIMTokenDeps
	throttle SourceThrottle
	opener   Opener
	logger   *slog.Logger
}

// NewAuthenticateSCIM returns the authenticator.
func NewAuthenticateSCIM(d SCIMTokenDeps, throttle SourceThrottle, opener Opener, logger *slog.Logger) *SCIMAuthenticator {
	return &SCIMAuthenticator{d: d, throttle: throttle, opener: opener, logger: logger}
}

// SCIMCaller is the SCIM token that was accepted and its provider.
type SCIMCaller struct {
	Token    identity.SCIMTokenID
	Hint     string
	Provider identity.ProviderID
	// ProviderSlug names the provider in audit entries.
	ProviderSlug string
}

// presentableRevokedFor is how long a revoked token is still recognised when
// presented, so a connector left on the token a rotation replaced is refused
// without counting against the address the connector with the new one
// shares.
const presentableRevokedFor = 7 * 24 * time.Hour

// Handle returns the caller presenting value from ip. The value is compared
// with every token before anything is counted: one that matches a live
// token is accepted whatever the source's failures, since it proves its
// presenter knows it; one that matches a token that is revoked, expired or
// of a disabled provider is refused ErrInvalidSCIMToken without counting,
// since its presenter knew a token once. Every other value, unknown or
// malformed, counts as a failure of ip's source and is refused
// ErrInvalidSCIMToken, or rate_limited once the source is throttled. A
// token accepted has its use recorded, at most once a minute.
func (h *SCIMAuthenticator) Handle(ctx context.Context, value string, ip netip.Addr) (SCIMCaller, error) {
	now := h.d.now()
	hash, err := identity.HashSCIMTokenValue(value)
	if err != nil {
		return SCIMCaller{}, h.fail(ctx, ip, now, err)
	}
	tokens, err := h.d.Store.AuthenticableSCIMTokens(ctx, now.Add(-presentableRevokedFor))
	if err != nil {
		return SCIMCaller{}, err
	}
	// Every token is opened and compared, whichever matches first, so the
	// time taken does not tell how many tokens precede the match.
	var (
		match         *SealedSCIMToken
		matchedLegacy bool
	)
	for i := range tokens {
		plain, openErr := h.opener.Open(secret.PurposeSCIMToken, tokens[i].SealedHash, scimTokenBinding(tokens[i].SCIMToken))
		legacy := false
		if openErr != nil {
			plain, openErr = h.opener.Open(secret.PurposeSCIMToken, tokens[i].SealedHash, legacySCIMTokenBinding(tokens[i].ID))
			legacy = openErr == nil
		}
		if openErr != nil || len(plain) != len(hash) {
			continue
		}
		var stored identity.SCIMTokenHash
		copy(stored[:], plain)
		if stored.Equal(hash) && match == nil {
			match, matchedLegacy = &tokens[i], legacy
		}
	}
	if match == nil {
		return SCIMCaller{}, h.fail(ctx, ip, now, identity.ErrInvalidSCIMToken)
	}
	if !match.Usable(now, match.ProviderEnabled) {
		return SCIMCaller{}, identity.ErrInvalidSCIMToken
	}
	if matchedLegacy {
		h.reseal(ctx, match)
	}
	if match.UseToRecord(now) {
		// A use that cannot be written changes nothing the caller depends on.
		if err = h.d.Store.RecordSCIMTokenUse(ctx, match.ID, now, now.Add(-identity.SCIMTokenUseInterval)); err != nil {
			h.logger.WarnContext(ctx, "recording the use of a SCIM token", "error", err.Error())
		}
	}
	return SCIMCaller{Token: match.ID, Hint: match.Hint, Provider: match.Provider, ProviderSlug: match.ProviderSlug}, nil
}

// reseal binds the hash of a token sealed before the provider and the expiry
// were bound to them. A hash that cannot be sealed again changes nothing the
// caller depends on: it opens as it did, and is tried again at the next use.
func (h *SCIMAuthenticator) reseal(ctx context.Context, t *SealedSCIMToken) {
	plain, err := h.opener.Open(secret.PurposeSCIMToken, t.SealedHash, legacySCIMTokenBinding(t.ID))
	if err == nil {
		var sealed string
		if sealed, err = h.d.Sealer.Seal(secret.PurposeSCIMToken, plain, scimTokenBinding(t.SCIMToken)); err == nil {
			err = h.d.Store.ResealSCIMToken(ctx, t.ID, sealed)
		}
	}
	if err != nil {
		h.logger.WarnContext(ctx, "binding the sealed hash of a SCIM token to its provider", "error", err.Error())
	}
}

// fail counts one failure of the source ip comes from and returns why the
// attempt is refused: rate_limited once the source is throttled, else
// refusal.
func (h *SCIMAuthenticator) fail(ctx context.Context, ip netip.Addr, now time.Time, refusal error) error {
	if err := h.throttle.Reserve(ctx, ip, now); err != nil {
		return err
	}
	return refusal
}
