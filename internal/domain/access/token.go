package access

import (
	"crypto/sha256"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// APITokenID identifies an API token.
type APITokenID = id.Of[APIToken]

// TokenKind says whom an API token acts for.
type TokenKind string

// The kinds of API token.
const (
	// TokenPersonal acts for the member who holds it.
	TokenPersonal TokenKind = "personal"
	// TokenProject holds one role in one project and acts for no member.
	TokenProject TokenKind = "project"
)

// prefixes start a token's value, so a leaked value is recognizable for what
// it is and its kind is known before any lookup.
var prefixes = map[TokenKind]string{TokenPersonal: "fn_pat_", TokenProject: "fn_prj_"}

const (
	tokenBodyLength = 40
	hintBodyLength  = 4
	passwordLength  = 24
	maxTokenName    = 200
)

// TokenHash is the SHA-256 of a token's whole value, the only form in which
// the value is stored.
type TokenHash [sha256.Size]byte

// TokenValue is a newly drawn token value: shown once, then only its hint
// and hash remain.
type TokenValue struct {
	Value secret.String
	// Hint is the prefix and the first four characters, enough for a member
	// to tell their tokens apart and for the audit log to name one.
	Hint string
	Hash TokenHash
}

// NewTokenValue draws a token value of kind from random: the kind's prefix
// and 40 base62 characters, about 238 bits.
func NewTokenValue(kind TokenKind, random io.Reader) TokenValue {
	v := prefixes[kind] + base62(random, tokenBodyLength)
	return TokenValue{
		Value: secret.New(v),
		Hint:  v[:len(prefixes[kind])+hintBodyLength],
		Hash:  sha256.Sum256([]byte(v)),
	}
}

// GeneratePassword draws a password of 24 base62 characters, about 143
// bits, for an instance admin created without one.
func GeneratePassword(random io.Reader) secret.String {
	return secret.New(base62(random, passwordLength))
}

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// base62 draws n characters from random without bias: a byte is used only
// below 248, the largest multiple of 62 a byte holds, since reducing the
// bytes above it would favour the first characters.
func base62(random io.Reader, n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, 64)
	for len(out) < n {
		if _, err := io.ReadFull(random, buf); err != nil {
			// The system's randomness never fails; an exhausted test source
			// is a programming error.
			panic("access: reading randomness: " + err.Error())
		}
		for _, b := range buf {
			if b < 248 && len(out) < n {
				out = append(out, base62Alphabet[b%62])
			}
		}
	}
	return string(out)
}

// ErrInvalidToken refuses a bearer value that is no token, malformed or
// unknown alike, so a caller cannot tell which.
var ErrInvalidToken = apperrors.Unauthenticated("invalid_token", "the API token is not valid")

// ParseTokenValue returns the hash of a well-formed token value. Any other
// value is an invalid token, refused without a lookup.
func ParseTokenValue(value string) (TokenHash, error) {
	body := ""
	for _, p := range prefixes {
		if b, ok := strings.CutPrefix(value, p); ok {
			body = b
		}
	}
	if len(body) != tokenBodyLength || strings.ContainsFunc(body, func(r rune) bool { return !strings.ContainsRune(base62Alphabet, r) }) {
		return TokenHash{}, ErrInvalidToken
	}
	return sha256.Sum256([]byte(value)), nil
}

// APIToken is a credential presented to the management API as a bearer
// token.
type APIToken struct {
	ID   APITokenID
	Kind TokenKind
	// Member holds a personal access token; zero for a project token.
	Member MemberID
	// Project is the one project a project token reaches, and Role the one
	// role it holds there; zero for a personal access token.
	Project workspace.ProjectID
	Role    RoleName
	Name    string
	Hint    string
	Hash    TokenHash
	// Scopes always hold read beside any write scope.
	Scopes []Scope
	// ExpiresAt is nil for a token that does not expire.
	ExpiresAt *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// NewPersonalTokenInput is what a personal access token is made of.
type NewPersonalTokenInput struct {
	Member MemberID
	Name   string
	// Scopes default to read alone; read is added to any write scope.
	Scopes    []Scope
	ExpiresAt *time.Time
	Now       time.Time
}

// NewPersonalToken returns a personal access token and its value, drawn from
// random.
func NewPersonalToken(tokenID APITokenID, in NewPersonalTokenInput, random io.Reader) (APIToken, TokenValue, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxTokenName {
		return APIToken{}, TokenValue{}, apperrors.Invalid("invalid_token_request", "the token is not valid", apperrors.Violation{
			Pointer: "/name", Code: "invalid_name", Detail: "a token's name has 1 to 200 characters and is not blank",
		})
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return APIToken{}, TokenValue{}, err
	}
	v := NewTokenValue(TokenPersonal, random)
	return APIToken{
		ID: tokenID, Kind: TokenPersonal, Member: in.Member, Name: name,
		Hint: v.Hint, Hash: v.Hash, Scopes: scopes, ExpiresAt: in.ExpiresAt, CreatedAt: in.Now,
	}, v, nil
}

// normalizeScopes returns scopes in catalog order with read added, or read
// alone when none are asked for.
func normalizeScopes(asked []Scope) ([]Scope, error) {
	all := AllScopes()
	for i, s := range asked {
		if !slices.Contains(all, s) {
			return nil, apperrors.Invalid("invalid_token_request", "the token is not valid", apperrors.Violation{
				Pointer: "/scopes/" + strconv.Itoa(i), Code: "unknown_scope", Detail: "the scope " + string(s) + " does not exist",
			})
		}
	}
	out := []Scope{ScopeRead}
	for _, s := range all[1:] {
		if slices.Contains(asked, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// HasScope reports whether the token carries s.
func (t APIToken) HasScope(s Scope) bool { return slices.Contains(t.Scopes, s) }

// Refusal judges the token at now, held by holder (nil for a project token):
// a revoked token is refused as revoked whether or not it also expired, and
// so is a personal access token whose member is not active; then an expired
// one as expired.
func (t APIToken) Refusal(now time.Time, holder *Member) error {
	if t.RevokedAt != nil || (t.Kind == TokenPersonal && (holder == nil || holder.Status != MemberActive)) {
		return apperrors.Unauthenticated("token_revoked", "the API token has been revoked")
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return apperrors.Unauthenticated("token_expired", "the API token has expired")
	}
	return nil
}
