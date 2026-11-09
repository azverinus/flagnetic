package access

import (
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ErrLastAuthMethod refuses removing the way in a member would be left
// without: a member always keeps one they can sign in with.
var ErrLastAuthMethod = apperrors.Conflict("last_auth_method",
	"this is the member's last usable way to sign in; set another way in first")

// IdentityMethod is an identity a member holds, as far as it is a way in.
type IdentityMethod struct {
	// ID identifies the identity, in the form its context gives it.
	ID string
	// Provider is the slug of the auth provider the identity is at.
	Provider string
	// ProviderEnabled is whether the auth provider the identity is at is
	// enabled: an identity at a disabled provider signs nobody in.
	ProviderEnabled bool
}

// AuthMethods are the ways a member holds to sign in.
type AuthMethods struct {
	Status MemberStatus
	// PasswordSetAt is when the member set the password they hold; nil when
	// they hold none.
	PasswordSetAt *time.Time
	// Identities are every identity the member holds, whether or not its
	// provider is enabled.
	Identities []IdentityMethod
	// PasswordRefused is whether the auth settings refuse the member's email
	// a password: local login is disabled, or their domain is enforced.
	PasswordRefused bool
	// EnforcedProvider is the slug of the auth provider the member's email
	// domain is enforced to, "" when it is not: an identity at another
	// provider signs them in nowhere.
	EnforcedProvider string
}

// UnderSettings returns m with what the settings s say of the member's email
// applied: whether a password is refused, and the provider it is enforced to.
func (m AuthMethods) UnderSettings(s AuthSettings, email string) AuthMethods {
	m.PasswordRefused = s.PasswordRefusal(email) != nil
	m.EnforcedProvider = ""
	if d, ok := s.EnforcedFor(email); ok {
		m.EnforcedProvider = d.Provider
	}
	return m
}

// PasswordSet reports whether the member holds a password.
func (m AuthMethods) PasswordSet() bool { return m.PasswordSetAt != nil }

// passwordUsable reports whether the password is a way in: the member holds
// one and local login accepts it for their email.
func (m AuthMethods) passwordUsable() bool { return m.PasswordSet() && !m.PasswordRefused }

// identityUsable reports whether i is a way in: its provider is enabled, and
// it is the provider the member's domain is enforced to, if any.
func (m AuthMethods) identityUsable(i IdentityMethod) bool {
	return i.ProviderEnabled && (m.EnforcedProvider == "" || i.Provider == m.EnforcedProvider)
}

// KeepsWayIn reports whether the member keeps a usable way in when the
// password is removed (withoutPassword) and the identity named by
// withoutIdentity ("" for none) is unlinked. A usable way in is a password
// local login accepts for the member's email, or an identity at an enabled
// provider that enforcement does not refuse them. A password local login or
// enforcement refuses today still counts as what the member would lose:
// `flagnetic admin unlock` makes it a way in again, so removing it while no
// usable way in remains could strand the member past the break-glass. A
// deactivated member signs in nowhere, so they need none.
func (m AuthMethods) KeepsWayIn(withoutPassword bool, withoutIdentity string) bool {
	if m.Status == MemberDeactivated {
		return true
	}
	return m.usable(withoutPassword, withoutIdentity)
}

// HoldsUsableWayIn reports whether the member holds a usable way in,
// whatever their status: what reactivating them asks, since a deactivated
// member signs in nowhere until then.
func (m AuthMethods) HoldsUsableWayIn() bool { return m.usable(false, "") }

// usable reports whether a usable way in remains once the password
// (withoutPassword) and the identity withoutIdentity are left out.
func (m AuthMethods) usable(withoutPassword bool, withoutIdentity string) bool {
	if !withoutPassword && m.passwordUsable() {
		return true
	}
	for _, i := range m.Identities {
		if i.ID != withoutIdentity && m.identityUsable(i) {
			return true
		}
	}
	return false
}
