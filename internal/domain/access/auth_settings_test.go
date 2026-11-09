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
		SelfServicePasswordReset: true, LocalLoginEnabled: true,
	}
	if !access.DefaultAuthSettings.Equal(want) {
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

func TestEnforcedDomainsAreNamedOnceWithAProvider(t *testing.T) {
	for name, tc := range map[string]struct {
		domains []access.EnforcedDomain
		want    []string
	}{
		"an empty domain":       {[]access.EnforcedDomain{{Domain: " ", Provider: "corp"}}, []string{"/sso_enforced_domains/0/domain invalid_domain"}},
		"an email for a domain": {[]access.EnforcedDomain{{Domain: "ann@corp.example", Provider: "corp"}}, []string{"/sso_enforced_domains/0/domain invalid_domain"}},
		"a domain with a space": {[]access.EnforcedDomain{{Domain: "corp example", Provider: "corp"}}, []string{"/sso_enforced_domains/0/domain invalid_domain"}},
		"a domain named twice, whatever its case": {
			[]access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}, {Domain: "CORP.Example.", Provider: "other"}},
			[]string{"/sso_enforced_domains/1/domain duplicate_domain"},
		},
		"no provider": {[]access.EnforcedDomain{{Domain: "corp.example"}}, []string{"/sso_enforced_domains/0/provider required"}},
	} {
		s := access.DefaultAuthSettings
		s.SSOEnforcedDomains = tc.domains
		if got := violations(t, s.Normalized().Check()); !slices.Equal(got, tc.want) {
			t.Errorf("%s: violations %v, want %v", name, got, tc.want)
		}
	}
	s := access.DefaultAuthSettings
	s.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: " CORP.Example. ", Provider: "corp"}}
	got := s.Normalized()
	if err := got.Check(); err != nil {
		t.Fatalf("a domain refused: %v", err)
	}
	if want := []access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}}; !slices.Equal(got.SSOEnforcedDomains, want) {
		t.Errorf("normalized %v, want %v", got.SSOEnforcedDomains, want)
	}
	if s.SSOEnforcedDomains[0].Domain != " CORP.Example. " {
		t.Error("Normalized changed the settings it was called on")
	}
}

func TestAPasswordIsRefusedInAnEnforcedDomainAndWhileLocalLoginIsOff(t *testing.T) {
	s := access.DefaultAuthSettings
	s.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}}
	for email, want := range map[string]string{
		"ann@corp.example": "sso_required", "Nobody@CORP.example": "sso_required",
		"ann@sub.corp.example": "", "ann@example.com": "", "no-at-sign": "",
	} {
		assertRefusal(t, s.PasswordRefusal(email), want, "corp")
	}
	s.LocalLoginEnabled = false
	assertRefusal(t, s.PasswordRefusal("ann@example.com"), "local_login_disabled", "")
	assertRefusal(t, s.PasswordRefusal("ann@corp.example"), "sso_required", "corp")

	assertRefusal(t, s.ProviderRefusal("ann@corp.example", "other"), "sso_required", "corp")
	assertRefusal(t, s.ProviderRefusal("ann@corp.example", "corp"), "", "")
	assertRefusal(t, s.ProviderRefusal("ann@example.com", "other"), "", "")
	if got := s.EnforcedDomainsOf("corp"); !slices.Equal(got, []string{"corp.example"}) {
		t.Errorf("domains of corp = %v", got)
	}
	if got := s.EnforcedDomainsOf("other"); got != nil {
		t.Errorf("domains of other = %v", got)
	}
}

func assertRefusal(t *testing.T, err error, code, provider string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Errorf("refused %v, want none", err)
		}
		return
	}
	e, ok := apperrors.From(err)
	if !ok || e.Code != code {
		t.Fatalf("refusal = %v, want %s", err, code)
	}
	if code == "sso_required" && e.Extensions["provider"] != provider {
		t.Errorf("provider = %v, want %s", e.Extensions["provider"], provider)
	}
}

func TestSettingsAreEqualWithTheSameEnforcedDomains(t *testing.T) {
	a, b := access.DefaultAuthSettings, access.DefaultAuthSettings
	a.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}}
	if a.Equal(b) {
		t.Error("settings with and without an enforced domain are equal")
	}
	b.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}}
	if !a.Equal(b) {
		t.Error("the same settings are not equal")
	}
}
