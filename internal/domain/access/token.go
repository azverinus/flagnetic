package access

import (
	"crypto/sha256"
	"io"
	"net/netip"
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
	// CreatedBy is the member who created a project token; zero for a
	// personal access token, whose member is Member. The project token
	// outlives them: it belongs to its project.
	CreatedBy MemberID
	// LastUsedAt and LastUsedIP are when and from where the token was last
	// presented, as last recorded; nil and the zero address until then.
	LastUsedAt *time.Time
	LastUsedIP netip.Addr
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
	name, err := tokenName(in.Name)
	if err != nil {
		return APIToken{}, TokenValue{}, err
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

// tokenName returns a token's name trimmed, refusing one that is blank or
// longer than 200 characters.
func tokenName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxTokenName {
		return "", errInvalidTokenRequest(apperrors.Violation{
			Pointer: "/name", Code: "invalid_name", Detail: "a token's name has 1 to 200 characters and is not blank",
		})
	}
	return name, nil
}

// NewProjectTokenInput is what a project token is made of.
type NewProjectTokenInput struct {
	Project workspace.ProjectID
	// Role is the one built-in role the token holds in Project, fixed for
	// its life.
	Role RoleName
	Name string
	// Scopes default to read alone; read is added to any write scope.
	Scopes    []Scope
	ExpiresAt *time.Time
	CreatedBy MemberID
	Now       time.Time
}

// NewProjectToken returns a project token and its value, drawn from random.
// It holds one built-in role in its project and never instance:admin: it
// acts in its project alone.
func NewProjectToken(tokenID APITokenID, in NewProjectTokenInput, random io.Reader) (APIToken, TokenValue, error) {
	name, err := tokenName(in.Name)
	if err != nil {
		return APIToken{}, TokenValue{}, err
	}
	if _, ok := RoleNamed(in.Role); !ok {
		return APIToken{}, TokenValue{}, errInvalidTokenRequest(apperrors.Violation{
			Pointer: "/role", Code: "unknown_role", Detail: "the role is none of owner, maintainer, developer and viewer",
		})
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return APIToken{}, TokenValue{}, err
	}
	if i := slices.Index(in.Scopes, ScopeInstanceAdmin); i >= 0 {
		return APIToken{}, TokenValue{}, apperrors.Invalid("scope_not_allowed", "a project token never carries instance:admin",
			apperrors.Violation{
				Pointer: "/scopes/" + strconv.Itoa(i), Code: "scope_not_allowed",
				Detail: "a project token acts in its project alone, so it never carries instance:admin",
			})
	}
	v := NewTokenValue(TokenProject, random)
	return APIToken{
		ID: tokenID, Kind: TokenProject, Project: in.Project, Role: in.Role, Name: name, Hint: v.Hint, Hash: v.Hash,
		Scopes: scopes, ExpiresAt: in.ExpiresAt, CreatedBy: in.CreatedBy, CreatedAt: in.Now,
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

// errInvalidTokenRequest refuses a token asked for with a field that breaks
// a rule, naming it.
func errInvalidTokenRequest(v apperrors.Violation) error {
	return apperrors.Invalid("invalid_token_request", "the token is not valid", v)
}

// CheckTokenExpiry refuses the expiry of a token created at now unless it
// lies in the future and at most the settings' maximum lifetime ahead, or
// its absence when the settings require one, so no token outlives what the
// instance allows. A token without an expiry never expires.
func CheckTokenExpiry(expiresAt *time.Time, now time.Time, s AuthSettings) error {
	switch {
	case expiresAt == nil && s.TokenExpiryRequired:
		return errInvalidTokenRequest(apperrors.Violation{
			Pointer: "/expires_at", Code: "required", Detail: "the instance requires every token to expire",
		})
	case expiresAt == nil:
		return nil
	case !expiresAt.After(now):
		return errInvalidTokenRequest(apperrors.Violation{
			Pointer: "/expires_at", Code: "in_past", Detail: "a token's expiry lies in the future",
		})
	case expiresAt.After(now.Add(s.TokenMaxLifetime)):
		seconds := int64(s.TokenMaxLifetime / time.Second)
		return errInvalidTokenRequest(apperrors.Violation{
			Pointer: "/expires_at", Code: "beyond_max_lifetime",
			Detail: "a token expires at most " + strconv.FormatInt(seconds, 10) + " seconds after it is created",
			Params: map[string]any{"max_lifetime_seconds": seconds},
		})
	}
	return nil
}

// CheckScopesHeld refuses scopes c does not hold itself, scope_not_allowed
// naming the first, so a credential never hands out more than it holds: a
// session holds every scope, but instance:admin only for an instance admin;
// a token holds its own scopes.
func CheckScopesHeld(c Caller, scopes []Scope) error {
	for i, s := range scopes {
		held := c.hasScope(s) && (s != ScopeInstanceAdmin || (c.Member != nil && c.Member.InstanceAdmin))
		if !held {
			return apperrors.Invalid("scope_not_allowed", "the scope "+string(s)+" is not one the caller holds", apperrors.Violation{
				Pointer: "/scopes/" + strconv.Itoa(i), Code: "scope_not_allowed",
				Detail: "a token carries only scopes its creator holds; instance:admin is an instance admin's",
			})
		}
	}
	return nil
}

// TokenUseInterval is how often a token's use is recorded at most: often
// enough for a member to tell a token in use from one forgotten, rarely
// enough that a busy script does not write on every request.
const TokenUseInterval = time.Minute

// UseToRecord reports whether presenting the token at now is a use worth
// recording: its first, or a minute or more after the last one recorded.
func (t APIToken) UseToRecord(now time.Time) bool {
	return t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= TokenUseInterval
}
