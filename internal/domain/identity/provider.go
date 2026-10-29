// Package identity holds the rules of the integrations that reach outside
// the instance to know who a person is: the auth providers an instance
// admin configures, and, as they arrive, the identities, sign-in attempts and
// directory provisioning built on them.
package identity

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ProviderID identifies an auth provider.
type ProviderID = id.Of[Provider]

// Kind is the protocol an auth provider speaks.
type Kind string

// The kinds of auth provider.
const (
	// KindOIDC is an OpenID Connect provider, discovered from its issuer.
	KindOIDC Kind = "oidc"
)

// Claims name the claims of an OIDC provider's tokens that carry what the
// instance reads of a person.
type Claims struct {
	Email  string
	Name   string
	Groups string
}

// The claim names a provider reads when it names none.
var defaultClaims = Claims{Email: "email", Name: "name", Groups: "groups"}

// defaultScopes are what an OIDC provider is asked for when it names none.
var defaultScopes = []string{"openid", "email", "profile"}

// OIDC is what an auth provider of kind OIDC is configured with.
type OIDC struct {
	// Issuer is the provider's issuer URL, from which its discovery document
	// is fetched.
	Issuer   string
	ClientID string
	// SealedClientSecret is the client secret, sealed under the instance's
	// secret key; its value is never read back through the API.
	SealedClientSecret   string
	Scopes               []string
	Claims               Claims
	RequireVerifiedEmail bool
}

// Provider is an auth provider: one identity provider members can sign in
// through, vouching for the email domains it names.
type Provider struct {
	ID ProviderID
	// Slug names the provider in sign-in buttons and in its redirect URI. It
	// never changes.
	Slug        string
	Kind        Kind
	DisplayName string
	Enabled     bool
	// Domains are the email domains the provider vouches for, lowercase.
	Domains       []string
	LinkByEmail   bool
	AutoProvision bool
	// OIDC is set for a provider of kind OIDC.
	OIDC      *OIDC
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// ProviderInput is what an instance admin chooses of a provider when it is
// created.
type ProviderInput struct {
	Slug          string
	Kind          Kind
	DisplayName   string
	Enabled       bool
	Domains       []string
	LinkByEmail   bool
	AutoProvision bool
	// OIDC is what a provider of kind OIDC is configured with.
	OIDC OIDCInput
}

// OIDCInput is the OIDC part of a provider's creation.
type OIDCInput struct {
	Issuer       string
	ClientID     string
	ClientSecret secret.String
	// Scopes and the claim names left empty take their defaults.
	Scopes               []string
	Claims               Claims
	RequireVerifiedEmail bool
}

// ClaimsChange names the claims an edit renames; a nil one is left as it is.
type ClaimsChange struct {
	Email  *string
	Name   *string
	Groups *string
}

// ProviderChange is an edit of a provider; a nil field is left as it is. The
// slug and the kind never change, and enabling is a decision of its own.
type ProviderChange struct {
	DisplayName   *string
	Domains       *[]string
	LinkByEmail   *bool
	AutoProvision *bool

	Issuer   *string
	ClientID *string
	// ClientSecret replaces the stored secret; nil keeps it.
	ClientSecret         *secret.String
	Scopes               *[]string
	Claims               *ClaimsChange
	RequireVerifiedEmail *bool
}

const (
	maxDisplayNameLength = 200
	maxClientIDLength    = 512
	maxClientSecretBytes = 4096
	maxClaimNameLength   = 256
	maxScopes            = 50
	maxScopeLength       = 256
	maxDomains           = 100
	maxDomainLength      = 253
)

var (
	slugPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	// A domain is dot-separated labels of letters, digits and hyphens, none
	// starting or ending with a hyphen, with at least two labels.
	domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

	errInsecureURL = apperrors.Invalid("insecure_url",
		"the issuer must be https unless it is on a loopback address",
		apperrors.Violation{Pointer: "/issuer", Code: "insecure_url", Detail: "the issuer must be https"})
)

// errUnsupportedKind refuses a kind of provider the instance does not offer.
var errUnsupportedKind = apperrors.Violation{
	Pointer: "/kind", Code: "unsupported_kind", Detail: "the kind must be oidc", Params: map[string]any{"allowed": []string{string(KindOIDC)}},
}

type violations []apperrors.Violation

func (v *violations) add(pointer, code, detail string, params map[string]any) {
	*v = append(*v, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
}

func (v violations) err() error {
	if len(v) == 0 {
		return nil
	}
	return apperrors.Invalid("invalid_auth_provider", "the auth provider breaks a rule; the violations name each field", v...)
}

// CheckProviderInput returns in normalized, or the refusal of the first rule
// it breaks, in this order: a malformed field (invalid_auth_provider, naming
// every one), an issuer that is not https and not on a loopback address
// (insecure_url).
func CheckProviderInput(in ProviderInput) (ProviderInput, error) {
	var v violations
	in.Slug = strings.TrimSpace(in.Slug)
	if !slugPattern.MatchString(in.Slug) {
		v.add("/slug", "invalid_slug", "a slug starts with a lowercase letter and has 1 to 64 lowercase letters, digits, hyphens and underscores", nil)
	}
	if in.Kind != KindOIDC {
		v = append(v, errUnsupportedKind)
	}
	in.DisplayName = checkDisplayName(&v, in.DisplayName)
	in.Domains = checkDomains(&v, in.Domains)
	if in.Kind == KindOIDC {
		in.OIDC.Issuer = checkIssuer(&v, in.OIDC.Issuer)
		in.OIDC.ClientID = checkClientID(&v, in.OIDC.ClientID)
		checkClientSecret(&v, in.OIDC.ClientSecret)
		in.OIDC.Scopes = checkScopes(&v, in.OIDC.Scopes)
		in.OIDC.Claims = checkClaims(&v, withDefaultClaims(in.OIDC.Claims))
	}
	if err := v.err(); err != nil {
		return in, err
	}
	if in.Kind == KindOIDC {
		return in, checkIssuerScheme(in.OIDC.Issuer)
	}
	return in, nil
}

// CheckChange returns the refusal of the first rule change breaks applied to
// p, in the order CheckProviderInput judges a creation, and the provider with
// the change applied and normalized.
func (p Provider) CheckChange(change ProviderChange) (Provider, error) {
	var v violations
	q := p.clone()
	if change.DisplayName != nil {
		q.DisplayName = *change.DisplayName
	}
	q.DisplayName = checkDisplayName(&v, q.DisplayName)
	if change.Domains != nil {
		q.Domains = *change.Domains
	}
	q.Domains = checkDomains(&v, q.Domains)
	if change.LinkByEmail != nil {
		q.LinkByEmail = *change.LinkByEmail
	}
	if change.AutoProvision != nil {
		q.AutoProvision = *change.AutoProvision
	}
	if q.OIDC != nil {
		o := q.OIDC
		if change.Issuer != nil {
			o.Issuer = *change.Issuer
		}
		o.Issuer = checkIssuer(&v, o.Issuer)
		if change.ClientID != nil {
			o.ClientID = *change.ClientID
		}
		o.ClientID = checkClientID(&v, o.ClientID)
		if change.ClientSecret != nil {
			checkClientSecret(&v, *change.ClientSecret)
		}
		if change.Scopes != nil {
			o.Scopes = *change.Scopes
		}
		o.Scopes = checkScopes(&v, o.Scopes)
		if c := change.Claims; c != nil {
			if c.Email != nil {
				o.Claims.Email = *c.Email
			}
			if c.Name != nil {
				o.Claims.Name = *c.Name
			}
			if c.Groups != nil {
				o.Claims.Groups = *c.Groups
			}
		}
		o.Claims = checkClaims(&v, o.Claims)
		if change.RequireVerifiedEmail != nil {
			o.RequireVerifiedEmail = *change.RequireVerifiedEmail
		}
	}
	if err := v.err(); err != nil {
		return p, err
	}
	if q.OIDC != nil {
		if err := checkIssuerScheme(q.OIDC.Issuer); err != nil {
			return p, err
		}
	}
	return q, nil
}

// Same reports whether q configures the provider p does, apart from p's
// sealed client secret, which a change replaces or leaves.
func (p Provider) Same(q Provider) bool {
	if p.DisplayName != q.DisplayName || p.LinkByEmail != q.LinkByEmail || p.AutoProvision != q.AutoProvision ||
		!slices.Equal(p.Domains, q.Domains) || (p.OIDC == nil) != (q.OIDC == nil) {
		return false
	}
	if p.OIDC == nil {
		return true
	}
	a, b := p.OIDC, q.OIDC
	return a.Issuer == b.Issuer && a.ClientID == b.ClientID && slices.Equal(a.Scopes, b.Scopes) && a.Claims == b.Claims &&
		a.RequireVerifiedEmail == b.RequireVerifiedEmail
}

func (p Provider) clone() Provider {
	p.Domains = slices.Clone(p.Domains)
	if p.OIDC != nil {
		o := *p.OIDC
		o.Scopes = slices.Clone(o.Scopes)
		p.OIDC = &o
	}
	return p
}

// New returns the provider in names, as its first version made at now with
// its client secret sealed as sealed. in must have passed CheckProviderInput.
func New(providerID ProviderID, in ProviderInput, sealed string, now time.Time) Provider {
	p := Provider{
		ID: providerID, Slug: in.Slug, Kind: in.Kind, DisplayName: in.DisplayName, Enabled: in.Enabled,
		Domains: in.Domains, LinkByEmail: in.LinkByEmail, AutoProvision: in.AutoProvision,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if in.Kind == KindOIDC {
		p.OIDC = &OIDC{
			Issuer: in.OIDC.Issuer, ClientID: in.OIDC.ClientID, SealedClientSecret: sealed, Scopes: in.OIDC.Scopes,
			Claims: in.OIDC.Claims, RequireVerifiedEmail: in.OIDC.RequireVerifiedEmail,
		}
	}
	return p
}

// Enable returns p enabled at now, and whether that changed it.
func (p Provider) Enable(now time.Time) (Provider, bool) {
	if p.Enabled {
		return p, false
	}
	p.Enabled, p.UpdatedAt, p.Version = true, now, p.Version+1
	return p, true
}

// Disable returns p disabled at now, and whether that changed it.
func (p Provider) Disable(now time.Time) (Provider, bool) {
	if !p.Enabled {
		return p, false
	}
	p.Enabled, p.UpdatedAt, p.Version = false, now, p.Version+1
	return p, true
}

func checkDisplayName(v *violations, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxDisplayNameLength {
		v.add("/display_name", "invalid_display_name", "a display name has 1 to 200 characters and is not blank",
			map[string]any{"min": 1, "max": maxDisplayNameLength})
	}
	return name
}

// checkDomains returns domains trimmed, lowercased, without a trailing dot
// and without repeats, in the order given.
func checkDomains(v *violations, domains []string) []string {
	if len(domains) == 0 {
		v.add("/domains", "domains_required", "a provider vouches for at least one email domain", nil)
		return domains
	}
	if len(domains) > maxDomains {
		v.add("/domains", "too_many_domains", "a provider names at most 100 domains", map[string]any{"max": maxDomains})
		return domains
	}
	out := make([]string, 0, len(domains))
	for i, d := range domains {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if len(d) > maxDomainLength || !domainPattern.MatchString(d) {
			v.add("/domains/"+strconv.Itoa(i), "invalid_domain", "a domain is a host name such as example.com, without an at sign or a scheme", nil)
			continue
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// checkIssuer returns issuer trimmed, noting each way it is not the URL of a
// host: relative, without a host, naming a user, a query or a fragment.
func checkIssuer(v *violations, issuer string) string {
	issuer = strings.TrimSpace(issuer)
	u, err := url.Parse(issuer)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		v.add("/issuer", "invalid_issuer", "an issuer is the http or https URL of the identity provider, without credentials, a query or a fragment", nil)
	}
	return issuer
}

// checkIssuerScheme refuses an issuer that is http where the address is not a
// loopback one: its discovery document, which names where tokens are signed
// and sent, could be altered in transit. Private networks are allowed, since
// only an instance admin configures a provider.
func checkIssuerScheme(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "http" || IsLoopbackHost(u.Hostname()) {
		return nil
	}
	return errInsecureURL
}

// IsLoopbackHost reports whether host is localhost or a loopback address.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

func checkClientID(v *violations, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || len(clientID) > maxClientIDLength {
		v.add("/client_id", "invalid_client_id", "a client ID has 1 to 512 bytes and is not blank", map[string]any{"min": 1, "max": maxClientIDLength})
	}
	return clientID
}

// CheckClientSecret refuses a client secret that is blank or over 4096 bytes.
func CheckClientSecret(s secret.String) error {
	var v violations
	checkClientSecret(&v, s)
	return v.err()
}

func checkClientSecret(v *violations, s secret.String) {
	if value := s.Reveal(); strings.TrimSpace(value) == "" || len(value) > maxClientSecretBytes {
		v.add("/client_secret", "invalid_client_secret", "a client secret has 1 to 4096 bytes and is not blank",
			map[string]any{"min": 1, "max": maxClientSecretBytes})
	}
}

// checkScopes returns scopes without repeats, the defaults when none are
// named.
func checkScopes(v *violations, scopes []string) []string {
	if len(scopes) == 0 {
		return slices.Clone(defaultScopes)
	}
	if len(scopes) > maxScopes {
		v.add("/scopes", "too_many_scopes", "a provider asks for at most 50 scopes", map[string]any{"max": maxScopes})
		return scopes
	}
	out := make([]string, 0, len(scopes))
	for i, s := range scopes {
		if s == "" || len(s) > maxScopeLength || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			v.add("/scopes/"+strconv.Itoa(i), "invalid_scope", "a scope is a token of up to 256 bytes without whitespace", nil)
			continue
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func withDefaultClaims(c Claims) Claims {
	if c.Email == "" {
		c.Email = defaultClaims.Email
	}
	if c.Name == "" {
		c.Name = defaultClaims.Name
	}
	if c.Groups == "" {
		c.Groups = defaultClaims.Groups
	}
	return c
}

func checkClaims(v *violations, c Claims) Claims {
	for _, name := range []struct {
		pointer string
		value   *string
	}{
		{"/claims/email", &c.Email}, {"/claims/name", &c.Name}, {"/claims/groups", &c.Groups},
	} {
		*name.value = strings.TrimSpace(*name.value)
		if *name.value == "" || len(*name.value) > maxClaimNameLength {
			v.add(name.pointer, "invalid_claim", "a claim name has 1 to 256 bytes and is not blank", map[string]any{"min": 1, "max": maxClaimNameLength})
		}
	}
	return c
}
