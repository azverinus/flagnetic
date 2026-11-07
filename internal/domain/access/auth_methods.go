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
}

// PasswordSet reports whether the member holds a password.
func (m AuthMethods) PasswordSet() bool { return m.PasswordSetAt != nil }

// passwordUsable reports whether the password is a way in. Local login
// accepts every email today, so it is when the member holds one.
func (m AuthMethods) passwordUsable() bool { return m.PasswordSet() }

// KeepsWayIn reports whether the member keeps a usable way in when the
// password is removed (withoutPassword) and the identity named by
// withoutIdentity ("" for none) is unlinked. A usable way in is a password
// local login accepts, or an identity at an enabled provider. A deactivated
// member signs in nowhere, so they need none.
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
		if i.ID != withoutIdentity && i.ProviderEnabled {
			return true
		}
	}
	return false
}
