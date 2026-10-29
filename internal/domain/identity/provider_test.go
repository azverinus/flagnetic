package identity_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

func validInput() identity.ProviderInput {
	return identity.ProviderInput{
		Slug: "corp", Kind: identity.KindOIDC, DisplayName: "Corporate SSO", Enabled: true,
		Domains: []string{"example.com"},
		OIDC: identity.OIDCInput{
			Issuer: "https://idp.example.com", ClientID: "flagnetic", ClientSecret: secret.New("s3cret-value"),
			RequireVerifiedEmail: true,
		},
	}
}

// problemOf returns the code of err and the pointers of its violations.
func problemOf(t *testing.T, err error) (string, []string) {
	t.Helper()
	var e *apperrors.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v carries no kind", err)
	}
	pointers := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		pointers[i] = v.Pointer
	}
	return e.Code, pointers
}

func TestAWellFormedProviderIsNormalizedAndTakesItsDefaults(t *testing.T) {
	in := validInput()
	in.DisplayName = "  Corporate SSO "
	in.Domains = []string{" Example.COM ", "example.com", "sub.Example.org."}
	got, err := identity.CheckProviderInput(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Corporate SSO" || !slices.Equal(got.Domains, []string{"example.com", "sub.example.org"}) {
		t.Errorf("checked = %+v", got)
	}
	if !slices.Equal(got.OIDC.Scopes, []string{"openid", "email", "profile"}) {
		t.Errorf("scopes = %v", got.OIDC.Scopes)
	}
	if got.OIDC.Claims != (identity.Claims{Email: "email", Name: "name", Groups: "groups"}) {
		t.Errorf("claims = %+v", got.OIDC.Claims)
	}
}

func TestAMalformedProviderNamesEveryFieldAtOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		change   func(*identity.ProviderInput)
		pointers []string
	}{
		"a slug starting with a digit": {func(in *identity.ProviderInput) { in.Slug = "1corp" }, []string{"/slug"}},
		"an uppercase slug":            {func(in *identity.ProviderInput) { in.Slug = "Corp" }, []string{"/slug"}},
		"a slug of 65 characters":      {func(in *identity.ProviderInput) { in.Slug = "a" + strings.Repeat("b", 64) }, []string{"/slug"}},
		"another kind":                 {func(in *identity.ProviderInput) { in.Kind = "ldap" }, []string{"/kind"}},
		"a blank display name":         {func(in *identity.ProviderInput) { in.DisplayName = " " }, []string{"/display_name"}},
		"no domain":                    {func(in *identity.ProviderInput) { in.Domains = nil }, []string{"/domains"}},
		"an email in place of a domain": {
			func(in *identity.ProviderInput) { in.Domains = []string{"example.com", "a@example.com"} }, []string{"/domains/1"},
		},
		"a domain of one label":       {func(in *identity.ProviderInput) { in.Domains = []string{"localhost"} }, []string{"/domains/0"}},
		"a relative issuer":           {func(in *identity.ProviderInput) { in.OIDC.Issuer = "idp.example.com" }, []string{"/issuer"}},
		"an issuer with a query":      {func(in *identity.ProviderInput) { in.OIDC.Issuer = "https://idp.example.com/?a=b" }, []string{"/issuer"}},
		"an issuer with credentials":  {func(in *identity.ProviderInput) { in.OIDC.Issuer = "https://u:p@idp.example.com" }, []string{"/issuer"}},
		"an issuer of another scheme": {func(in *identity.ProviderInput) { in.OIDC.Issuer = "ftp://idp.example.com" }, []string{"/issuer"}},
		"a blank client ID":           {func(in *identity.ProviderInput) { in.OIDC.ClientID = "" }, []string{"/client_id"}},
		"no client secret":            {func(in *identity.ProviderInput) { in.OIDC.ClientSecret = secret.String{} }, []string{"/client_secret"}},
		"a scope with a space":        {func(in *identity.ProviderInput) { in.OIDC.Scopes = []string{"openid", "a b"} }, []string{"/scopes/1"}},
		"a claim name over 256 bytes": {func(in *identity.ProviderInput) { in.OIDC.Claims.Groups = strings.Repeat("g", 257) }, []string{"/claims/groups"}},
		"several at once": {
			func(in *identity.ProviderInput) { in.Slug, in.DisplayName, in.OIDC.ClientID = "", "", "" },
			[]string{"/slug", "/display_name", "/client_id"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			tc.change(&in)
			_, err := identity.CheckProviderInput(in)
			code, pointers := problemOf(t, err)
			if code != "invalid_auth_provider" || !slices.Equal(pointers, tc.pointers) {
				t.Errorf("refused %s at %v, want invalid_auth_provider at %v", code, pointers, tc.pointers)
			}
		})
	}
}

func TestAnIssuerMustBeHTTPSExceptOnALoopbackAddress(t *testing.T) {
	for issuer, insecure := range map[string]bool{
		"https://idp.example.com":      false,
		"https://10.0.0.5/realms/a":    false,
		"http://idp.example.com":       true,
		"http://10.0.0.5:8080":         true,
		"http://localhost:5556/dex":    false,
		"http://127.0.0.1:8080":        false,
		"http://127.1.2.3":             false,
		"http://[::1]:8080":            false,
		"http://localhost.example.com": true,
		"http://0.0.0.0:8080":          true,
	} {
		in := validInput()
		in.OIDC.Issuer = issuer
		_, err := identity.CheckProviderInput(in)
		if !insecure {
			if err != nil {
				t.Errorf("%s: %v", issuer, err)
			}
			continue
		}
		if code, _ := problemOf(t, err); code != "insecure_url" {
			t.Errorf("%s: refused as %q, want insecure_url", issuer, code)
		}
	}
}

func TestAMalformedFieldIsRefusedBeforeAnInsecureIssuer(t *testing.T) {
	in := validInput()
	in.OIDC.Issuer = "http://idp.example.com"
	in.Slug = "Bad"
	if code, _ := problemOf(t, errOf(identity.CheckProviderInput(in))); code != "invalid_auth_provider" {
		t.Errorf("refused as %q", code)
	}
}

func errOf[T any](_ T, err error) error { return err }

func newProvider(t *testing.T) identity.Provider {
	t.Helper()
	in, err := identity.CheckProviderInput(validInput())
	if err != nil {
		t.Fatal(err)
	}
	return identity.New(identity.ProviderID{}, in, "sealed", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}

func TestAChangeLeavesWhatItDoesNotNameAndNormalizesWhatItDoes(t *testing.T) {
	p := newProvider(t)
	name, domains, groups := "  Renamed ", []string{"Other.com"}, "roles"
	got, err := p.CheckChange(identity.ProviderChange{
		DisplayName: &name, Domains: &domains, Claims: &identity.ClaimsChange{Groups: &groups},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Renamed" || !slices.Equal(got.Domains, []string{"other.com"}) {
		t.Errorf("changed = %+v", got)
	}
	if got.OIDC.Claims != (identity.Claims{Email: "email", Name: "name", Groups: "roles"}) {
		t.Errorf("claims = %+v", got.OIDC.Claims)
	}
	if got.OIDC.Issuer != p.OIDC.Issuer || got.OIDC.SealedClientSecret != "sealed" || got.Slug != "corp" {
		t.Errorf("changed what it did not name: %+v", got.OIDC)
	}
	if p.DisplayName != "Corporate SSO" || !slices.Equal(p.Domains, []string{"example.com"}) {
		t.Errorf("the provider changed in place: %+v", p)
	}
}

func TestAChangeThatBreaksARuleIsRefusedLikeACreation(t *testing.T) {
	p := newProvider(t)
	empty, insecure := []string{}, "http://idp.example.com"
	if code, pointers := problemOf(t, errOf(p.CheckChange(identity.ProviderChange{Domains: &empty}))); code != "invalid_auth_provider" ||
		!slices.Equal(pointers, []string{"/domains"}) {
		t.Errorf("no domains: %s %v", code, pointers)
	}
	if code, _ := problemOf(t, errOf(p.CheckChange(identity.ProviderChange{Issuer: &insecure}))); code != "insecure_url" {
		t.Errorf("an http issuer: %s", code)
	}
	blank := secret.New(" ")
	if code, pointers := problemOf(t, errOf(p.CheckChange(identity.ProviderChange{ClientSecret: &blank}))); code != "invalid_auth_provider" ||
		!slices.Equal(pointers, []string{"/client_secret"}) {
		t.Errorf("a blank secret: %s %v", code, pointers)
	}
}

func TestSameIgnoresTheSealedSecretAndNothingElse(t *testing.T) {
	p := newProvider(t)
	q := p
	q.OIDC = &identity.OIDC{}
	*q.OIDC = *p.OIDC
	q.OIDC.SealedClientSecret = "other"
	if !p.Same(q) {
		t.Error("a different sealed secret made the providers differ")
	}
	q.OIDC.ClientID = "other"
	if p.Same(q) {
		t.Error("a different client ID did not")
	}
}

func TestEnablingAndDisablingMoveTheVersionOnlyWhenTheyChangeTheProvider(t *testing.T) {
	p := newProvider(t)
	now := p.CreatedAt.Add(time.Hour)
	if same, changed := p.Enable(now); changed || same.Version != 1 {
		t.Errorf("enabling an enabled provider: %+v %v", same, changed)
	}
	off, changed := p.Disable(now)
	if !changed || off.Enabled || off.Version != 2 || !off.UpdatedAt.Equal(now) {
		t.Errorf("disabled = %+v %v", off, changed)
	}
	if again, again2 := off.Disable(now); again2 || again.Version != 2 {
		t.Errorf("disabling a disabled provider: %+v %v", again, again2)
	}
	on, changed := off.Enable(now.Add(time.Hour))
	if !changed || !on.Enabled || on.Version != 3 {
		t.Errorf("enabled = %+v %v", on, changed)
	}
}
