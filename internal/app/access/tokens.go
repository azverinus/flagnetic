package access

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// PersonalTokenStore keeps members' personal access tokens.
type PersonalTokenStore interface {
	// MemberByID returns the member with the identifier; ErrNotFound when
	// none has it.
	MemberByID(ctx context.Context, member access.MemberID) (access.Member, error)
	// PersonalTokens returns every personal access token of member, revoked
	// and expired ones included, the newest first.
	PersonalTokens(ctx context.Context, member access.MemberID) ([]access.APIToken, error)
	CreateToken(ctx context.Context, t access.APIToken) error
	// RevokePersonalToken revokes the personal access token of member with
	// the identifier at at and returns it as revoked; ErrNotFound unless
	// member holds it unrevoked.
	RevokePersonalToken(ctx context.Context, member access.MemberID, token access.APITokenID, at time.Time) (access.APIToken, error)
}

// ErrTokenNotFound answers a token that is not an unrevoked one of the
// member addressed: another member's, one revoked already, or none at all.
var ErrTokenNotFound = apperrors.NotFound("not_found", "no unrevoked token of the member has this identifier")

// tokenHolder returns the member whose tokens are addressed: member, or the
// caller's own when it is zero.
func tokenHolder(ctx context.Context, store PersonalTokenStore, caller access.Caller, member access.MemberID) (access.Member, error) {
	if member.IsZero() {
		if caller.Member == nil {
			return access.Member{}, access.ErrSessionRequired
		}
		return *caller.Member, nil
	}
	m, err := store.MemberByID(ctx, member)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, ErrMemberNotFound
	}
	return m, err
}

// ListPersonalTokens answers a member's personal access tokens.
type ListPersonalTokens struct{ store PersonalTokenStore }

// NewListPersonalTokens returns the query.
func NewListPersonalTokens(store PersonalTokenStore) *ListPersonalTokens {
	return &ListPersonalTokens{store: store}
}

// Handle returns the personal access tokens of member, the caller's own
// when member is zero, the newest first; an unknown member is
// ErrMemberNotFound.
func (h *ListPersonalTokens) Handle(ctx context.Context, caller access.Caller, member access.MemberID) ([]access.APIToken, error) {
	holder, err := tokenHolder(ctx, h.store, caller, member)
	if err != nil {
		return nil, err
	}
	return h.store.PersonalTokens(ctx, holder.ID)
}

// CreatePersonalTokenCommand is a member asking for a personal access token.
type CreatePersonalTokenCommand struct {
	Caller    access.Caller
	Name      string
	Scopes    []access.Scope
	ExpiresAt *time.Time
	Request   governance.Request
}

// CreatedToken is a token just created, with its value, shown this once.
type CreatedToken struct {
	Token access.APIToken
	Value secret.String
}

// CreatePersonalToken creates a personal access token for the member signed
// in.
type CreatePersonalToken struct {
	tx       Transactor
	store    PersonalTokenStore
	settings AuthSettingsReader
	audit    AuditLog
	clock    clock.Clock
	ids      *id.Source
	random   io.Reader
}

// NewCreatePersonalToken returns the handler; random draws token values.
func NewCreatePersonalToken(tx Transactor, store PersonalTokenStore, settings AuthSettingsReader, audit AuditLog, c clock.Clock,
	ids *id.Source, random io.Reader,
) *CreatePersonalToken {
	return &CreatePersonalToken{tx: tx, store: store, settings: settings, audit: audit, clock: c, ids: ids, random: random}
}

// Handle creates the token and records api_token.created with its hint.
// Only a session creates one, so a leaked token can neither multiply itself
// nor outlive its revocation; its scopes are those the session's member
// holds, and its expiry is within what the auth settings allow.
func (h *CreatePersonalToken) Handle(ctx context.Context, cmd CreatePersonalTokenCommand) (CreatedToken, error) {
	if cmd.Caller.Session == nil || cmd.Caller.Member == nil {
		return CreatedToken{}, access.ErrSessionRequired
	}
	holder := *cmd.Caller.Member
	now := h.clock.Now()
	token, value, err := access.NewPersonalToken(id.New[access.APIToken](h.ids), access.NewPersonalTokenInput{
		Member: holder.ID, Name: cmd.Name, Scopes: cmd.Scopes, ExpiresAt: cmd.ExpiresAt, Now: now,
	}, h.random)
	if err != nil {
		return CreatedToken{}, err
	}
	if err = access.CheckScopesHeld(cmd.Caller, token.Scopes); err != nil {
		return CreatedToken{}, err
	}
	settings, err := h.settings.AuthSettings(ctx)
	if err != nil {
		return CreatedToken{}, err
	}
	if err = access.CheckTokenExpiry(token.ExpiresAt, now, settings); err != nil {
		return CreatedToken{}, err
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, tokenCreated(token, holder))
	if err != nil {
		return CreatedToken{}, err
	}
	if err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.CreateToken(ctx, token); err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	}); err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{Token: token, Value: value.Value}, nil
}

// RevokePersonalTokenCommand names the token to revoke: one of Member's, or
// of the caller's own when Member is zero.
type RevokePersonalTokenCommand struct {
	Caller  access.Caller
	Member  access.MemberID
	Token   access.APITokenID
	Request governance.Request
}

// RevokePersonalToken revokes a personal access token.
type RevokePersonalToken struct {
	tx    Transactor
	store PersonalTokenStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewRevokePersonalToken returns the handler.
func NewRevokePersonalToken(tx Transactor, store PersonalTokenStore, audit AuditLog, c clock.Clock, ids *id.Source,
) *RevokePersonalToken {
	return &RevokePersonalToken{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle revokes the token and records api_token.revoked. A member's own
// tokens are revoked only from a session, whichever route names them, so a
// leaked token cannot hide its siblings, an instance admin's included; a
// token the member does not hold unrevoked is ErrTokenNotFound.
func (h *RevokePersonalToken) Handle(ctx context.Context, cmd RevokePersonalTokenCommand) error {
	own := cmd.Member.IsZero() || (cmd.Caller.Member != nil && cmd.Caller.Member.ID == cmd.Member)
	if own && cmd.Caller.Session == nil {
		return access.ErrSessionRequired
	}
	return h.tx.Within(ctx, func(ctx context.Context) error {
		holder, err := tokenHolder(ctx, h.store, cmd.Caller, cmd.Member)
		if err != nil {
			return err
		}
		now := h.clock.Now()
		token, err := h.store.RevokePersonalToken(ctx, holder.ID, cmd.Token, now)
		if errors.Is(err, ErrNotFound) {
			return ErrTokenNotFound
		}
		if err != nil {
			return err
		}
		return recordAll(ctx, h.audit, h.ids, now, cmd.Request, tokenRevoked(workspace.ProjectID{}, token, tokenDetail(token, holder)))
	})
}

// TokenUseStore records when and from where API tokens were last used.
type TokenUseStore interface {
	// RecordTokenUse records that token was presented at at from ip, unless
	// a use less than a minute before at is recorded already, so replicas
	// writing the same token's use coalesce.
	RecordTokenUse(ctx context.Context, token access.APITokenID, at time.Time, ip netip.Addr) error
}

// TokenUses gathers the uses of API tokens a replica sees and writes them
// in rounds, so authenticating a request never waits on a write and a token
// presented many times a minute costs one.
type TokenUses struct {
	store TokenUseStore

	mu      sync.Mutex
	pending map[access.APITokenID]tokenUse
}

type tokenUse struct {
	at time.Time
	ip netip.Addr
}

// NewTokenUses returns the replica's gatherer of token uses.
func NewTokenUses(store TokenUseStore) *TokenUses {
	return &TokenUses{store: store, pending: map[access.APITokenID]tokenUse{}}
}

// Note keeps t's use at at from ip for the next round, when it is one worth
// recording; the latest use of a token seen before the round is the one
// written.
func (u *TokenUses) Note(t access.APIToken, at time.Time, ip netip.Addr) {
	if !t.UseToRecord(at) {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if prev, ok := u.pending[t.ID]; !ok || at.After(prev.at) {
		u.pending[t.ID] = tokenUse{at: at, ip: ip}
	}
}

// Flush writes the uses noted since the last round. A use that cannot be
// written is dropped rather than retried: the next use of its token is
// noted again, and nothing depends on the record but a member's view of
// their tokens. It returns the first failure, for the round's log.
func (u *TokenUses) Flush(ctx context.Context) error {
	u.mu.Lock()
	pending := u.pending
	u.pending = map[access.APITokenID]tokenUse{}
	u.mu.Unlock()
	var first error
	for token, use := range pending {
		if err := u.store.RecordTokenUse(ctx, token, use.at, use.ip); err != nil && first == nil {
			first = err
		}
	}
	return first
}
