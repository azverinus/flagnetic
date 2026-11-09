package access

import (
	"slices"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// AuthSettings are the instance's rules for sessions, API tokens and
// passwords, which an instance admin changes at runtime.
type AuthSettings struct {
	// SessionIdleTimeout ends a session that long after its last request.
	SessionIdleTimeout time.Duration
	// SessionAbsoluteTimeout ends a session that long after it opened,
	// however much it is used.
	SessionAbsoluteTimeout time.Duration
	// TokenMaxLifetime is the furthest ahead an API token may expire.
	TokenMaxLifetime time.Duration
	// TokenExpiryRequired refuses an API token without an expiry.
	TokenExpiryRequired bool
	// PasswordMinLength is the fewest code points a password has, never
	// below MinPasswordLength.
	PasswordMinLength int
	// SelfServicePasswordReset lets a member ask for a password reset by
	// mail. An instance admin can still issue a password reset whatever it says.
	SelfServicePasswordReset bool
	// LocalLoginEnabled lets members sign in with a password at all. Off,
	// every member signs in through an auth provider.
	LocalLoginEnabled bool
	// SSOEnforcedDomains are the email domains whose people sign in only
	// through one auth provider, each named once.
	SSOEnforcedDomains []EnforcedDomain
}

// EnforcedDomain sends the people of an email domain to one auth provider:
// a password is no way in for them, whether to sign in, to accept an
// invitation or to complete a password reset, and no other provider signs
// them in or links them.
type EnforcedDomain struct {
	// Domain is lowercase, such as example.com; its subdomains are not
	// included.
	Domain string
	// Provider is the slug of the auth provider.
	Provider string
}

// DefaultAuthSettings are the settings of a fresh instance.
var DefaultAuthSettings = AuthSettings{
	SessionIdleTimeout:     DefaultSessionTimeouts.Idle,
	SessionAbsoluteTimeout: DefaultSessionTimeouts.Absolute,
	TokenMaxLifetime:       365 * 24 * time.Hour,
	PasswordMinLength:      MinPasswordLength,
	// A member who forgot their password is helped without an instance
	// admin as soon as the instance can send mail.
	SelfServicePasswordReset: true,
	LocalLoginEnabled:        true,
}

// Errors refusing a password as a way in.
var (
	// ErrLocalLoginDisabled refuses every password while local login is
	// disabled.
	ErrLocalLoginDisabled = apperrors.Forbidden("local_login_disabled",
		"local login is disabled; sign in through an auth provider")
	// ErrIdentityRequired refuses disabling local login to a caller who holds
	// no identity at an enabled auth provider: they would lock themself out
	// along with everybody else.
	ErrIdentityRequired = apperrors.Invalid("identity_required",
		"local login can be disabled only by a member who holds an identity at an enabled auth provider")
	errSSORequired = apperrors.Forbidden("sso_required", "this email's domain signs in through an auth provider only")
)

// SSORequired is the refusal of a way in for an email whose domain is
// enforced to provider (a slug), naming the provider to sign in through.
func SSORequired(provider string) error {
	return errSSORequired.WithExtension("provider", provider)
}

// Equal reports whether s and t are the same settings, the enforced
// domains in the same order.
func (s AuthSettings) Equal(t AuthSettings) bool {
	return s.SessionIdleTimeout == t.SessionIdleTimeout && s.SessionAbsoluteTimeout == t.SessionAbsoluteTimeout &&
		s.TokenMaxLifetime == t.TokenMaxLifetime && s.TokenExpiryRequired == t.TokenExpiryRequired &&
		s.PasswordMinLength == t.PasswordMinLength && s.SelfServicePasswordReset == t.SelfServicePasswordReset &&
		s.LocalLoginEnabled == t.LocalLoginEnabled && slices.Equal(s.SSOEnforcedDomains, t.SSOEnforcedDomains)
}

// Normalized returns s with each enforced domain trimmed, lowercased and
// without a trailing dot, as the settings keep and compare them.
func (s AuthSettings) Normalized() AuthSettings {
	if s.SSOEnforcedDomains == nil {
		return s
	}
	domains := make([]EnforcedDomain, len(s.SSOEnforcedDomains))
	for i, d := range s.SSOEnforcedDomains {
		domains[i] = EnforcedDomain{Domain: normalizeDomain(d.Domain), Provider: d.Provider}
	}
	s.SSOEnforcedDomains = domains
	return s
}

func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// emailDomain returns the domain of email as enforced domains are compared
// with it: after its last @, lowercased; "" when it has none.
func emailDomain(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return ""
	}
	return normalizeDomain(email[at+1:])
}

// EnforcedFor returns the enforced domain email belongs to, judged from its
// domain alone, so the answer never tells whether a member holds email.
func (s AuthSettings) EnforcedFor(email string) (EnforcedDomain, bool) {
	domain := emailDomain(email)
	if domain == "" {
		return EnforcedDomain{}, false
	}
	for _, d := range s.SSOEnforcedDomains {
		if d.Domain == domain {
			return d, true
		}
	}
	return EnforcedDomain{}, false
}

// PasswordRefusal returns why a password is no way in for email: sso_required
// naming the provider when its domain is enforced, local_login_disabled while
// local login is off, nil when a password may sign it in. The enforcement
// wins, since it says where to go instead.
func (s AuthSettings) PasswordRefusal(email string) error {
	if d, ok := s.EnforcedFor(email); ok {
		return SSORequired(d.Provider)
	}
	if !s.LocalLoginEnabled {
		return ErrLocalLoginDisabled
	}
	return nil
}

// ProviderRefusal returns why the auth provider with the slug provider may
// not sign in or link email: sso_required naming the provider its domain is
// enforced to, when that is another one; nil otherwise.
func (s AuthSettings) ProviderRefusal(email, provider string) error {
	if d, ok := s.EnforcedFor(email); ok && d.Provider != provider {
		return SSORequired(d.Provider)
	}
	return nil
}

// EnforcedDomainsOf returns the enforced domains sent to provider, a slug.
func (s AuthSettings) EnforcedDomainsOf(provider string) []string {
	var out []string
	for _, d := range s.SSOEnforcedDomains {
		if d.Provider == provider {
			out = append(out, d.Domain)
		}
	}
	return out
}

// The bounds of every duration of the settings: a minute keeps an instance
// usable, ten years is beyond any session or token anyone means to keep.
const (
	minSettingDuration = time.Minute
	maxSettingDuration = 10 * 365 * 24 * time.Hour
)

// SessionTimeouts are the timeouts the settings give sessions.
func (s AuthSettings) SessionTimeouts() SessionTimeouts {
	return SessionTimeouts{Idle: s.SessionIdleTimeout, Absolute: s.SessionAbsoluteTimeout}
}

// Check refuses settings that break a rule, invalid_auth_settings naming
// each field: every duration a whole number of seconds from a minute to ten
// years, the idle timeout at most the absolute one, and the password
// minimum length from MinPasswordLength to MaxPasswordLength, so a policy
// never lets weaker passwords in nor refuses every one, and each enforced
// domain named once, with a provider. Whether the provider exists and
// vouches for the domain is the identity context's to say. Check judges the
// settings as Normalized returns them.
func (s AuthSettings) Check() error {
	var vs []apperrors.Violation
	for _, d := range []struct {
		pointer string
		value   time.Duration
	}{
		{"/session_idle_timeout_seconds", s.SessionIdleTimeout},
		{"/session_absolute_timeout_seconds", s.SessionAbsoluteTimeout},
		{"/token_max_lifetime_seconds", s.TokenMaxLifetime},
	} {
		if d.value < minSettingDuration || d.value > maxSettingDuration || d.value%time.Second != 0 {
			vs = append(vs, outOfRange(d.pointer, "a whole number of seconds",
				int64(minSettingDuration/time.Second), int64(maxSettingDuration/time.Second)))
		}
	}
	if len(vs) == 0 && s.SessionIdleTimeout > s.SessionAbsoluteTimeout {
		vs = append(vs, apperrors.Violation{
			Pointer: "/session_idle_timeout_seconds", Code: "idle_beyond_absolute",
			Detail: "the idle timeout must be at most the absolute timeout",
		})
	}
	if s.PasswordMinLength < MinPasswordLength || s.PasswordMinLength > MaxPasswordLength {
		vs = append(vs, outOfRange("/password_min_length", "a number of characters", MinPasswordLength, MaxPasswordLength))
	}
	vs = append(vs, checkEnforcedDomains(s.SSOEnforcedDomains)...)
	if len(vs) > 0 {
		return apperrors.Invalid("invalid_auth_settings", "the auth settings are not valid", vs...)
	}
	return nil
}

func outOfRange(pointer, what string, minimum, maximum int64) apperrors.Violation {
	return apperrors.Violation{
		Pointer: pointer, Code: "out_of_range",
		Detail: "must be " + what + " from " + strconv.FormatInt(minimum, 10) + " to " + strconv.FormatInt(maximum, 10),
		Params: map[string]any{"min": minimum, "max": maximum},
	}
}

func checkEnforcedDomains(domains []EnforcedDomain) []apperrors.Violation {
	var vs []apperrors.Violation
	seen := make(map[string]bool, len(domains))
	for i, d := range domains {
		at := "/sso_enforced_domains/" + strconv.Itoa(i)
		switch {
		case d.Domain == "" || strings.ContainsAny(d.Domain, "@ \t/"):
			vs = append(vs, apperrors.Violation{Pointer: at + "/domain", Code: "invalid_domain",
				Detail: "a domain is a host name such as example.com, without an at sign or a scheme"})
		case seen[d.Domain]:
			vs = append(vs, apperrors.Violation{Pointer: at + "/domain", Code: "duplicate_domain",
				Detail: "a domain is enforced to one provider, named once"})
		}
		seen[d.Domain] = true
		if d.Provider == "" {
			vs = append(vs, apperrors.Violation{Pointer: at + "/provider", Code: "required", Detail: "name the auth provider"})
		}
	}
	return vs
}
