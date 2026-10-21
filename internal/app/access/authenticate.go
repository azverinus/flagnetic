package access

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// ErrNotFound is what a store answers for an entity it does not hold.
var ErrNotFound = errors.New("access: not found")

// TokenStore finds API tokens by the hash of their value.
type TokenStore interface {
	// TokenByHash returns the token and, for a personal access token, the
	// member holding it; ErrNotFound when no token has the hash.
	TokenByHash(ctx context.Context, hash access.TokenHash) (access.APIToken, *access.Member, error)
}

// AuthenticateToken resolves a bearer token's value into the caller
// presenting it.
type AuthenticateToken struct {
	store TokenStore
	clock clock.Clock
}

// NewAuthenticateToken returns the handler.
func NewAuthenticateToken(store TokenStore, c clock.Clock) *AuthenticateToken {
	return &AuthenticateToken{store: store, clock: c}
}

// Handle returns the caller presenting value, or the refusal a script tells
// apart: invalid_token for a value that is malformed or unknown alike,
// token_revoked, token_expired.
func (h *AuthenticateToken) Handle(ctx context.Context, value string) (access.Caller, error) {
	hash, err := access.ParseTokenValue(value)
	if err != nil {
		return access.Caller{}, err
	}
	token, holder, err := h.store.TokenByHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return access.Caller{}, access.ErrInvalidToken
	}
	if err != nil {
		return access.Caller{}, err
	}
	if err := token.Refusal(h.clock.Now(), holder); err != nil {
		return access.Caller{}, err
	}
	return access.TokenCaller(token, holder), nil
}
