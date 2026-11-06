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
	// UnrevokedSCIMTokens returns every token not revoked, with its sealed
	// hash: the few an instance holds, all of which authentication opens.
	UnrevokedSCIMTokens(ctx context.Context) ([]SealedSCIMToken, error)
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
		token, value, err := identity.NewSCIMToken(id.New[identity.SCIMToken](h.d.IDs), provider.ID, cmd.ExpiresAt, now, h.d.Random)
		if err != nil {
			return err
		}
		sealed, err := h.d.Sealer.Seal(secret.PurposeSCIMToken, token.Hash[:])
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
	// Reserve counts one failure of the source ip comes from at at, before
	// what is presented is checked. A throttled source is refused
	// rate_limited, its failure taken back. Otherwise it returns the function
	// that takes the failure back, for an attempt that guessed nothing.
	Reserve(ctx context.Context, ip netip.Addr, at time.Time) (release func(context.Context) error, err error)
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

// Handle returns the caller presenting value from ip. Every value but a
// token that matches counts as a failure of ip's source before it is
// compared, and one that is refused as unknown, malformed, revoked, expired
// or of a disabled provider alike is ErrInvalidSCIMToken; a throttled source
// is refused rate_limited. A value that matches a live token proves its
// presenter knows it, so a token that expired or lost its provider is
// refused without counting. A token accepted has its use recorded, at most
// once a minute.
func (h *SCIMAuthenticator) Handle(ctx context.Context, value string, ip netip.Addr) (SCIMCaller, error) {
	now := h.d.now()
	release, err := h.throttle.Reserve(ctx, ip, now)
	if err != nil {
		return SCIMCaller{}, err
	}
	hash, err := identity.HashSCIMTokenValue(value)
	if err != nil {
		return SCIMCaller{}, err
	}
	tokens, err := h.d.Store.UnrevokedSCIMTokens(ctx)
	if err != nil {
		return SCIMCaller{}, errors.Join(err, release(context.WithoutCancel(ctx)))
	}
	// Every token is opened and compared, whichever matches first, so the
	// time taken does not tell how many tokens precede the match.
	var match *SealedSCIMToken
	for i := range tokens {
		plain, openErr := h.opener.Open(secret.PurposeSCIMToken, tokens[i].SealedHash)
		if openErr != nil || len(plain) != len(hash) {
			continue
		}
		var stored identity.SCIMTokenHash
		copy(stored[:], plain)
		if stored.Equal(hash) && match == nil {
			match = &tokens[i]
		}
	}
	if match == nil {
		return SCIMCaller{}, identity.ErrInvalidSCIMToken
	}
	if err = release(context.WithoutCancel(ctx)); err != nil {
		return SCIMCaller{}, err
	}
	if !match.Usable(now, match.ProviderEnabled) {
		return SCIMCaller{}, identity.ErrInvalidSCIMToken
	}
	if match.UseToRecord(now) {
		// A use that cannot be written changes nothing the caller depends on.
		if err = h.d.Store.RecordSCIMTokenUse(ctx, match.ID, now, now.Add(-identity.SCIMTokenUseInterval)); err != nil {
			h.logger.WarnContext(ctx, "recording the use of a SCIM token", "error", err.Error())
		}
	}
	return SCIMCaller{Token: match.ID, Hint: match.Hint, Provider: match.Provider, ProviderSlug: match.ProviderSlug}, nil
}
