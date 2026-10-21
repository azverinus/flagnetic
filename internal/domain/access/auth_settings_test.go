package access_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestAFreshInstanceHasTheDefaultAuthSettings(t *testing.T) {
	want := access.AuthSettings{
		SessionIdleTimeout: 24 * time.Hour, SessionAbsoluteTimeout: 30 * 24 * time.Hour,
		TokenMaxLifetime: 365 * 24 * time.Hour, TokenExpiryRequired: false, PasswordMinLength: 12,
	}
	if access.DefaultAuthSettings != want {
		t.Errorf("defaults = %+v, want %+v", access.DefaultAuthSettings, want)
	}
	if got := access.DefaultAuthSettings.SessionTimeouts(); got != access.DefaultSessionTimeouts {
		t.Errorf("session timeouts = %+v, want the defaults", got)
	}
	if err := access.DefaultAuthSettings.Check(); err != nil {
		t.Errorf("the defaults are refused: %v", err)
	}
}

// violations returns the pointer and code of each violation err reports,
// as "pointer code", once err is invalid_auth_settings.
func violations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_auth_settings" {
		t.Fatalf("error = %v, want invalid_auth_settings", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func TestAuthSettingsAreBoundedAndConsistent(t *testing.T) {
	const tenYears = 10 * 365 * 24 * time.Hour
	valid := access.DefaultAuthSettings
	for name, tc := range map[string]struct {
		change func(*access.AuthSettings)
		want   []string
	}{
		"idle beyond absolute": {func(s *access.AuthSettings) {
			s.SessionIdleTimeout, s.SessionAbsoluteTimeout = 48*time.Hour, 24*time.Hour
		},
			[]string{"/session_idle_timeout_seconds idle_beyond_absolute"}},
		"idle below a minute": {func(s *access.AuthSettings) { s.SessionIdleTimeout = 59 * time.Second },
			[]string{"/session_idle_timeout_seconds out_of_range"}},
		"absolute beyond ten years": {func(s *access.AuthSettings) { s.SessionAbsoluteTimeout = tenYears + time.Second },
			[]string{"/session_absolute_timeout_seconds out_of_range"}},
		"token lifetime below a minute": {func(s *access.AuthSettings) { s.TokenMaxLifetime = 0 },
			[]string{"/token_max_lifetime_seconds out_of_range"}},
		"token lifetime beyond ten years": {func(s *access.AuthSettings) { s.TokenMaxLifetime = tenYears + time.Second },
			[]string{"/token_max_lifetime_seconds out_of_range"}},
		"a fraction of a second": {func(s *access.AuthSettings) { s.TokenMaxLifetime = time.Hour + time.Millisecond },
			[]string{"/token_max_lifetime_seconds out_of_range"}},
		"a password minimum below the floor": {func(s *access.AuthSettings) { s.PasswordMinLength = 11 },
			[]string{"/password_min_length out_of_range"}},
		"a password minimum above the ceiling": {func(s *access.AuthSettings) { s.PasswordMinLength = 129 },
			[]string{"/password_min_length out_of_range"}},
		"several at once": {func(s *access.AuthSettings) { s.SessionAbsoluteTimeout, s.PasswordMinLength = 0, 3 },
			[]string{"/session_absolute_timeout_seconds out_of_range", "/password_min_length out_of_range"}},
	} {
		s := valid
		tc.change(&s)
		if got := violations(t, s.Check()); !slices.Equal(got, tc.want) {
			t.Errorf("%s: violations %v, want %v", name, got, tc.want)
		}
	}
	for name, s := range map[string]access.AuthSettings{
		"the smallest": {SessionIdleTimeout: time.Minute, SessionAbsoluteTimeout: time.Minute, TokenMaxLifetime: time.Minute, PasswordMinLength: 12},
		"the largest":  {SessionIdleTimeout: tenYears, SessionAbsoluteTimeout: tenYears, TokenMaxLifetime: tenYears, TokenExpiryRequired: true, PasswordMinLength: 128},
	} {
		if err := s.Check(); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
}
