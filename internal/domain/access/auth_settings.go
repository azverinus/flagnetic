package access

import (
	"strconv"
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
}

// DefaultAuthSettings are the settings of a fresh instance.
var DefaultAuthSettings = AuthSettings{
	SessionIdleTimeout:     DefaultSessionTimeouts.Idle,
	SessionAbsoluteTimeout: DefaultSessionTimeouts.Absolute,
	TokenMaxLifetime:       365 * 24 * time.Hour,
	PasswordMinLength:      MinPasswordLength,
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
// never lets weaker passwords in nor refuses every one.
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
