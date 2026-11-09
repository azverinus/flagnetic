package sso_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// isolated is an instance on a database of its own, with its instance
// admin's client and the pool its members are made on: the auth settings and
// the providers enabled are the instance's, so a test changing them touches
// no other.
type isolated struct {
	env
	pool *pgxpool.Pool
}

func startIsolated(t *testing.T) isolated {
	t.Helper()
	pool := testkit.FreshPool(t)
	e := start(t, testkit.WithPool(pool))
	e.admin = testkit.Client(t, e.srv, testkit.Bootstrap(t, pool))
	return isolated{env: e, pool: pool}
}

// settingsWith returns the auth settings c reads, changed by change.
func settingsWith(t *testing.T, c *management.ClientWithResponses, change func(*management.AuthSettings)) management.AuthSettings {
	t.Helper()
	res, err := c.GetAuthSettingsWithResponse(t.Context())
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /instance/auth-settings = %v: %v", res.StatusCode(), err)
	}
	s := *res.JSON200
	change(&s)
	return s
}

// putSettings replaces the auth settings through c, changed from those in
// force by change, and returns the answer.
func putSettings(t *testing.T, c *management.ClientWithResponses, change func(*management.AuthSettings)) *management.UpdateAuthSettingsResponse {
	t.Helper()
	res, err := c.UpdateAuthSettingsWithResponse(t.Context(), settingsWith(t, c, change))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// enforce sends domain to provider through c, failing unless it is.
func enforce(t *testing.T, c *management.ClientWithResponses, domain, provider string) management.AuthSettings {
	t.Helper()
	res := putSettings(t, c, func(s *management.AuthSettings) {
		s.SsoEnforcedDomains = []management.SsoEnforcedDomain{{Domain: domain, Provider: provider}}
	})
	if res.JSON200 == nil {
		t.Fatalf("enforcing %s to %s = %d: %s", domain, provider, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// passwordSignIn signs in with email and password from a fresh browser.
func passwordSignIn(t *testing.T, e env, email, password string) *management.SignInResponse {
	t.Helper()
	res, err := testkit.NewBrowser(t, e.srv.URL).SignInWithResponse(t.Context(), management.SignInRequest{Email: email, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// wantRefused checks a problem answered status with code and, for
// sso_required, the provider to sign in through.
func wantRefused(t *testing.T, what string, status int, body []byte, p *management.Problem, wantStatus int, code, provider string) {
	t.Helper()
	if status != wantStatus || p == nil || p.Code != code {
		t.Errorf("%s = %d %s, want %d %s", what, status, body, wantStatus, code)
		return
	}
	if provider != "" && (p.Provider == nil || *p.Provider != provider) {
		t.Errorf("%s names provider %v, want %s", what, p.Provider, provider)
	}
}

func wantSSORequired(t *testing.T, what string, res *management.SignInResponse, provider string) {
	t.Helper()
	wantRefused(t, what, res.StatusCode(), res.Body, res.ApplicationproblemJSON403, http.StatusForbidden, "sso_required", provider)
}

func authMethods(t *testing.T, e env, email string) management.AuthMethods {
	t.Helper()
	res, err := testkit.NewBrowser(t, e.srv.URL).GetAuthMethodsWithResponse(t.Context(), &management.GetAuthMethodsParams{Email: &email})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /auth/methods = %v: %v", res.StatusCode(), err)
	}
	return *res.JSON200
}

// noFallback leaves a provider to sign in only the identities linked to it.
func noFallback(domains ...string) func(*provider) {
	return func(p *provider) { p.domains, p.autoProvision, p.linkByEmail = domains, false, false }
}

func TestEnforcingADomainRefusesItsPasswordsWhileItsProviderStillSignsIn(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	pool := e.pool
	alice := testkit.NewMember(t, pool, testkit.WithEmail("alice@corp.example"))
	outsider := testkit.NewMember(t, pool, testkit.WithEmail("outsider@example.com"))
	aliceAt := testkit.NewBrowser(t, e.srv.URL)
	aliceAt.SignIn(t, alice.Email, alice.Password)
	e.link(t, idp, aliceAt, corp, "sub-alice", nil)

	got := enforce(t, e.admin, " CORP.Example ", corp)
	if want := []management.SsoEnforcedDomain{{Domain: "corp.example", Provider: corp}}; !slices.Equal(got.SsoEnforcedDomains, want) || !got.LocalLoginEnabled {
		t.Errorf("settings answered %+v, want the domain lowercased and local login on", got)
	}

	t.Run("a password is no way in for the domain, whoever holds the email", func(t *testing.T) {
		wantSSORequired(t, "the right password", passwordSignIn(t, e.env, alice.Email, alice.Password), corp)
		wantSSORequired(t, "a wrong password, the email in capitals", passwordSignIn(t, e.env, "ALICE@corp.EXAMPLE", "not the password"), corp)
		wantSSORequired(t, "an email nobody holds", passwordSignIn(t, e.env, "nobody@corp.example", "not the password"), corp)
	})
	t.Run("an email outside the domain still signs in with a password", func(t *testing.T) {
		if res := passwordSignIn(t, e.env, outsider.Email, outsider.Password); res.JSON200 == nil {
			t.Errorf("outside the domain = %d: %s", res.StatusCode(), res.Body)
		}
		if res := passwordSignIn(t, e.env, "someone@sub.corp.example", "not the password"); res.StatusCode() != http.StatusUnauthorized {
			t.Errorf("a subdomain = %d: %s; want 401, as subdomains are not enforced", res.StatusCode(), res.Body)
		}
	})
	t.Run("the enforced provider signs the member in", func(t *testing.T) {
		idp.NextClaims(map[string]any{"sub": "sub-alice", "email": alice.Email})
		b := testkit.NewBrowser(t, e.srv.URL)
		e.signIn(t, b, corp, "", e.opts())
		if me := signedIn(t, b); me == nil || me.Member == nil || me.Member.Email != alice.Email {
			t.Errorf("signed in as %+v, want %s", me, alice.Email)
		}
	})
	t.Run("an API token keeps working", func(t *testing.T) {
		token := testkit.PersonalToken(t, pool, alice.ID, "script", access.ScopeRead)
		if res, err := testkit.Client(t, e.srv, token).GetMeWithResponse(t.Context()); err != nil || res.JSON200 == nil {
			t.Errorf("GET /me with a token = %v: %v", res.StatusCode(), err)
		}
	})
	t.Run("the sign-in page learns where to go from the domain alone", func(t *testing.T) {
		for _, email := range []string{alice.Email, "Nobody@CORP.example"} {
			m := authMethods(t, e.env, email)
			if m.EnforcedProvider == nil || m.EnforcedProvider.Slug != corp || m.EnforcedProvider.Kind != management.AuthProviderKindOidc ||
				!m.LocalLoginEnabled || m.PasswordResetAvailable {
				t.Errorf("methods for %s = %+v, want %s enforced and no reset", email, m, corp)
			}
		}
		if m := authMethods(t, e.env, outsider.Email); m.EnforcedProvider != nil {
			t.Errorf("methods outside the domain name %+v", m.EnforcedProvider)
		}
	})
	t.Run("the enforced provider is not disabled, deleted or edited out of the domain", func(t *testing.T) {
		disabled, err := e.admin.DisableAuthProviderWithResponse(t.Context(), corp)
		if err != nil {
			t.Fatal(err)
		}
		wantInUse(t, "disabling", disabled.StatusCode(), disabled.Body, disabled.ApplicationproblemJSON409, "enforced domains")
		deleted, err := e.admin.DeleteAuthProviderWithResponse(t.Context(), corp)
		if err != nil {
			t.Fatal(err)
		}
		wantInUse(t, "deleting", deleted.StatusCode(), deleted.Body, deleted.ApplicationproblemJSON409, "enforced domains")
		edited, err := e.admin.UpdateAuthProviderWithResponse(t.Context(), corp, &management.UpdateAuthProviderParams{IfMatch: ptr(`"1"`)},
			management.AuthProviderPatch{Domains: &[]string{"example.com"}})
		if err != nil {
			t.Fatal(err)
		}
		wantInUse(t, "dropping the domain", edited.StatusCode(), edited.Body, edited.ApplicationproblemJSON409, "enforced domains")
		kept, err := e.admin.UpdateAuthProviderWithResponse(t.Context(), corp, &management.UpdateAuthProviderParams{IfMatch: ptr(`"1"`)},
			management.AuthProviderPatch{Domains: &[]string{"corp.example", "example.com"}})
		if err != nil || kept.JSON200 == nil {
			t.Errorf("adding a domain beside the enforced one = %v: %s", kept.StatusCode(), kept.Body)
		}
	})
	t.Run("the enforced provider's identity is not unlinked", func(t *testing.T) {
		identity := identityAt(t, methodsOf(t, aliceAt), corp)
		res, err := e.admin.UnlinkMemberIdentityWithResponse(t.Context(), alice.ID.String(), identity.Id.String())
		if err != nil {
			t.Fatal(err)
		}
		wantRefused(t, "unlinking", res.StatusCode(), res.Body, res.ApplicationproblemJSON409, http.StatusConflict, "provider_enforced", "")
	})
	t.Run("the change is audited with the settings before and after", func(t *testing.T) {
		action := "auth_settings.updated"
		res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action})
		if err != nil || res.JSON200 == nil || len(res.JSON200.Items) != 1 {
			t.Fatalf("auth_settings.updated entries = %v: %s, want 1", err, res.Body)
		}
		before, _ := res.JSON200.Items[0].Before.(map[string]any)
		after, _ := res.JSON200.Items[0].After.(map[string]any)
		domains, _ := after["sso_enforced_domains"].([]any)
		if len(domains) != 1 || domains[0].(map[string]any)["domain"] != "corp.example" || domains[0].(map[string]any)["provider"] != corp ||
			after["local_login_enabled"] != true {
			t.Errorf("after = %v, want the enforced domain and local login on", after)
		}
		if lifted, _ := before["sso_enforced_domains"].([]any); len(lifted) != 0 {
			t.Errorf("before = %v, want no enforced domain", before)
		}
	})
	t.Run("lifting the enforcement lets the password in and frees the provider", func(t *testing.T) {
		res := putSettings(t, e.admin, func(s *management.AuthSettings) { s.SsoEnforcedDomains = []management.SsoEnforcedDomain{} })
		if res.JSON200 == nil || len(res.JSON200.SsoEnforcedDomains) != 0 {
			t.Fatalf("lifting = %d: %s", res.StatusCode(), res.Body)
		}
		if res := passwordSignIn(t, e.env, alice.Email, alice.Password); res.JSON200 == nil {
			t.Errorf("the password after lifting = %d: %s", res.StatusCode(), res.Body)
		}
		if res, err := e.admin.DisableAuthProviderWithResponse(t.Context(), corp); err != nil || res.JSON200 == nil {
			t.Errorf("disabling the provider after lifting = %v: %s", res.StatusCode(), res.Body)
		}
	})
}

func wantInUse(t *testing.T, what string, status int, body []byte, p *management.Problem, use string) {
	t.Helper()
	wantRefused(t, what, status, body, p, http.StatusConflict, "provider_in_use", "")
	if p != nil && (p.UsedBy == nil || !slices.Contains(*p.UsedBy, use)) {
		t.Errorf("%s: used_by = %v, want %q among them", what, p.UsedBy, use)
	}
}

func TestAnotherProviderNeitherSignsInNorLinksAnEnforcedEmail(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	other := e.forged(t, idp, func(p *provider) { p.domains, p.autoProvision, p.linkByEmail = []string{"corp.example"}, true, true })
	member := testkit.NewMember(t, e.pool, testkit.WithEmail("member@corp.example"))
	memberAt := testkit.NewBrowser(t, e.srv.URL)
	memberAt.SignIn(t, member.Email, member.Password)
	e.link(t, idp, memberAt, corp, "sub-member-corp", nil)
	enforce(t, e.admin, "corp.example", corp)

	t.Run("a newcomer is not provisioned", func(t *testing.T) {
		idp.NextClaims(map[string]any{"sub": "sub-newcomer", "email": "newcomer@corp.example", "email_verified": true})
		b := testkit.NewBrowser(t, e.srv.URL)
		v := e.signIn(t, b, other, "", e.opts())
		wantError(t, v, b, "sso_required")
		if v.Query("provider") != corp {
			t.Errorf("the refusal names %q, want %s", v.Query("provider"), corp)
		}
		if n := e.memberCount(t, "newcomer@corp.example"); n != 0 {
			t.Errorf("another provider provisioned %d members in the enforced domain", n)
		}
	})
	t.Run("an existing member is not linked by email", func(t *testing.T) {
		idp.NextClaims(map[string]any{"sub": "sub-member-other", "email": member.Email, "email_verified": true})
		b := testkit.NewBrowser(t, e.srv.URL)
		wantError(t, e.signIn(t, b, other, "", e.opts()), b, "sso_required")
	})
	t.Run("a member signed in through their provider links no other", func(t *testing.T) {
		idp.NextClaims(map[string]any{"sub": "sub-member-corp", "email": member.Email})
		b := testkit.NewBrowser(t, e.srv.URL)
		e.signIn(t, b, corp, "", e.opts())
		if signedIn(t, b) == nil {
			t.Fatal("the member could not sign in through the enforced provider")
		}
		v := e.link(t, idp, b, other, "sub-member-linked", nil)
		wantSettings(t, v, "sso_required")
		if v.Query("provider") != corp {
			t.Errorf("the link refusal names %q, want %s", v.Query("provider"), corp)
		}
		if ids := methodsOf(t, b).Identities; len(ids) != 1 || ids[0].Provider != corp {
			t.Errorf("identities after the refused link = %+v, want the enforced provider's alone", ids)
		}
	})
}

func TestEnforcedDomainsAreValidated(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	off := e.forged(t, idp, noFallback("off.example"))
	if res, err := e.admin.DisableAuthProviderWithResponse(t.Context(), off); err != nil || res.JSON200 == nil {
		t.Fatalf("disabling = %v: %v", res.StatusCode(), err)
	}
	for name, tc := range map[string]struct {
		domains []management.SsoEnforcedDomain
		pointer string
		code    string
	}{
		"an unknown provider":   {[]management.SsoEnforcedDomain{{Domain: "corp.example", Provider: "nobody"}}, "/sso_enforced_domains/0/provider", "unknown_provider"},
		"a disabled provider":   {[]management.SsoEnforcedDomain{{Domain: "off.example", Provider: off}}, "/sso_enforced_domains/0/provider", "provider_disabled"},
		"a domain not its own":  {[]management.SsoEnforcedDomain{{Domain: "example.org", Provider: corp}}, "/sso_enforced_domains/0/domain", "not_provider_domain"},
		"a domain named twice":  {[]management.SsoEnforcedDomain{{Domain: "corp.example", Provider: corp}, {Domain: "Corp.Example", Provider: corp}}, "/sso_enforced_domains/1/domain", "duplicate_domain"},
		"an empty domain":       {[]management.SsoEnforcedDomain{{Domain: "", Provider: corp}}, "/sso_enforced_domains/0/domain", "invalid_domain"},
		"an email for a domain": {[]management.SsoEnforcedDomain{{Domain: "alice@corp.example", Provider: corp}}, "/sso_enforced_domains/0/domain", "invalid_domain"},
		"a domain with a space": {[]management.SsoEnforcedDomain{{Domain: "corp example", Provider: corp}}, "/sso_enforced_domains/0/domain", "invalid_domain"},
		"no provider named":     {[]management.SsoEnforcedDomain{{Domain: "corp.example"}}, "/sso_enforced_domains/0/provider", "required"},
	} {
		res := putSettings(t, e.admin, func(s *management.AuthSettings) { s.SsoEnforcedDomains = tc.domains })
		p := res.ApplicationproblemJSON422
		if p == nil || p.Code != "invalid_auth_settings" || p.Errors == nil ||
			!slices.ContainsFunc(*p.Errors, func(v management.Violation) bool { return v.Pointer == tc.pointer && v.Code == tc.code }) {
			t.Errorf("%s: PUT = %d %s, want 422 invalid_auth_settings with %s %s", name, res.StatusCode(), res.Body, tc.pointer, tc.code)
		}
	}
	if got := settingsWith(t, e.admin, func(*management.AuthSettings) {}); len(got.SsoEnforcedDomains) != 0 {
		t.Errorf("enforced domains after the refusals = %+v, want none", got.SsoEnforcedDomains)
	}
}

func TestAnInvitationToAnEnforcedDomainIsNotAcceptedWithAPassword(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	enforce(t, e.admin, "corp.example", corp)
	invite := func(email string) string {
		t.Helper()
		res, err := e.admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: email, DisplayName: "Carol"})
		if err != nil || res.JSON201 == nil || res.JSON201.Invitation == nil || res.JSON201.Invitation.Url == nil {
			t.Fatalf("inviting %s = %v: %v", email, res.StatusCode(), err)
		}
		link := *res.JSON201.Invitation.Url
		secret := link[strings.LastIndex(link, "/")+1:]
		testkit.RecordSecret(secret)
		return secret
	}
	anyone := testkit.NewBrowser(t, e.srv.URL)
	carol := invite("carol@corp.example")
	opened, err := anyone.GetInvitationWithResponse(t.Context(), carol)
	if err != nil || opened.JSON200 == nil {
		t.Fatalf("GET the invitation = %v: %v", opened.StatusCode(), err)
	}
	if opened.JSON200.PasswordAllowed || opened.JSON200.EnforcedProvider == nil || opened.JSON200.EnforcedProvider.Slug != corp {
		t.Errorf("the invitation answers %+v, want no password and %s", opened.JSON200, corp)
	}
	testkit.RecordSecret("a long enough password for carol")
	accepted, err := anyone.AcceptInvitationWithResponse(t.Context(), carol, management.InvitationAcceptance{Password: "a long enough password for carol"})
	if err != nil {
		t.Fatal(err)
	}
	wantRefused(t, "accepting with a password", accepted.StatusCode(), accepted.Body, accepted.ApplicationproblemJSON403, http.StatusForbidden, "sso_required", corp)
	again, err := anyone.GetInvitationWithResponse(t.Context(), carol)
	if err != nil || again.JSON200 == nil {
		t.Errorf("the invitation after the refusal = %v: %v; want it open still", again.StatusCode(), err)
	}

	outside, err := anyone.GetInvitationWithResponse(t.Context(), invite("dave@example.com"))
	if err != nil || outside.JSON200 == nil {
		t.Fatalf("GET the invitation = %v: %v", outside.StatusCode(), err)
	}
	if !outside.JSON200.PasswordAllowed || outside.JSON200.EnforcedProvider != nil {
		t.Errorf("an invitation outside the domain answers %+v, want a password allowed and no provider", outside.JSON200)
	}
}

func TestAPasswordResetIsNoWayInForAnEnforcedDomain(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	dave := testkit.NewMember(t, e.pool, testkit.WithEmail("dave@corp.example"))
	issued, err := e.admin.IssuePasswordResetWithResponse(t.Context(), dave.ID.String())
	if err != nil || issued.JSON201 == nil {
		t.Fatalf("issuing a reset = %v: %v", issued.StatusCode(), err)
	}
	secret := issued.JSON201.Url[strings.LastIndex(issued.JSON201.Url, "/")+1:]
	testkit.RecordSecret(secret)
	enforce(t, e.admin, "corp.example", corp)

	anyone := testkit.NewBrowser(t, e.srv.URL)
	testkit.RecordSecret("a brand new password for dave")
	res, err := anyone.CompletePasswordResetWithResponse(t.Context(), secret, management.PasswordResetCompletion{Password: "a brand new password for dave"})
	if err != nil {
		t.Fatal(err)
	}
	wantRefused(t, "completing the reset", res.StatusCode(), res.Body, res.ApplicationproblemJSON403, http.StatusForbidden, "sso_required", corp)
	if open, err := anyone.GetPasswordResetWithResponse(t.Context(), secret); err != nil || open.JSON200 == nil {
		t.Errorf("the reset after the refusal = %v: %v; want it open still", open.StatusCode(), err)
	}
}

func TestDisablingLocalLoginNeedsAnIdentityAndRefusesEveryPassword(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	pool := e.pool
	admin := testkit.NewMember(t, pool, testkit.AsInstanceAdmin(), testkit.WithEmail("keeper@example.com"))
	adminAt := testkit.NewBrowser(t, e.srv.URL)
	adminAt.SignIn(t, admin.Email, admin.Password)
	disable := func(s *management.AuthSettings) { s.LocalLoginEnabled = false }

	res := putSettings(t, adminAt.ClientWithResponses, disable)
	wantRefused(t, "without any provider", res.StatusCode(), res.Body, res.ApplicationproblemJSON422, http.StatusUnprocessableEntity, "identity_required", "")
	corp := e.forged(t, idp, noFallback("example.com"))
	res = putSettings(t, adminAt.ClientWithResponses, disable)
	wantRefused(t, "holding no identity", res.StatusCode(), res.Body, res.ApplicationproblemJSON422, http.StatusUnprocessableEntity, "identity_required", "")

	e.link(t, idp, adminAt, corp, "sub-admin", nil)
	invited, err := e.admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: "erin@example.com", DisplayName: "Erin"})
	if err != nil || invited.JSON201 == nil || invited.JSON201.Invitation == nil || invited.JSON201.Invitation.Url == nil {
		t.Fatalf("inviting = %v: %v", invited.StatusCode(), err)
	}
	url := *invited.JSON201.Invitation.Url
	invitation := url[strings.LastIndex(url, "/")+1:]
	testkit.RecordSecret(invitation)
	if res = putSettings(t, adminAt.ClientWithResponses, disable); res.JSON200 == nil || res.JSON200.LocalLoginEnabled {
		t.Fatalf("disabling local login holding an identity = %d: %s", res.StatusCode(), res.Body)
	}

	t.Run("every password sign-in is refused", func(t *testing.T) {
		for _, email := range []string{admin.Email, "nobody@example.com"} {
			res := passwordSignIn(t, e.env, email, admin.Password)
			wantRefused(t, email, res.StatusCode(), res.Body, res.ApplicationproblemJSON403, http.StatusForbidden, "local_login_disabled", "")
		}
		if m := authMethods(t, e.env, ""); m.LocalLoginEnabled || m.PasswordResetAvailable {
			t.Errorf("auth methods = %+v, want local login off", m)
		}
	})
	t.Run("an invitation is not accepted with a password", func(t *testing.T) {
		anyone := testkit.NewBrowser(t, e.srv.URL)
		if opened, err := anyone.GetInvitationWithResponse(t.Context(), invitation); err != nil || opened.JSON200 == nil || opened.JSON200.PasswordAllowed {
			t.Errorf("the invitation = %v: %s; want no password allowed", opened.StatusCode(), opened.Body)
		}
		testkit.RecordSecret("a long enough password for erin")
		res, err := anyone.AcceptInvitationWithResponse(t.Context(), invitation, management.InvitationAcceptance{Password: "a long enough password for erin"})
		if err != nil {
			t.Fatal(err)
		}
		wantRefused(t, "accepting", res.StatusCode(), res.Body, res.ApplicationproblemJSON403, http.StatusForbidden, "local_login_disabled", "")
	})
	t.Run("the only enabled provider is neither disabled nor deleted", func(t *testing.T) {
		disabled, err := e.admin.DisableAuthProviderWithResponse(t.Context(), corp)
		if err != nil {
			t.Fatal(err)
		}
		wantInUse(t, "disabling", disabled.StatusCode(), disabled.Body, disabled.ApplicationproblemJSON409, "only way in")
		deleted, err := e.admin.DeleteAuthProviderWithResponse(t.Context(), corp)
		if err != nil {
			t.Fatal(err)
		}
		wantInUse(t, "deleting", deleted.StatusCode(), deleted.Body, deleted.ApplicationproblemJSON409, "only way in")
	})
	t.Run("single sign-on still signs in", func(t *testing.T) {
		idp.NextClaims(map[string]any{"sub": "sub-admin", "email": "whatever@example.org"})
		b := testkit.NewBrowser(t, e.srv.URL)
		e.signIn(t, b, corp, "", e.opts())
		if me := signedIn(t, b); me == nil || me.Member == nil || me.Member.Email != admin.Email {
			t.Errorf("signed in as %+v, want %s", me, admin.Email)
		}
	})
	t.Run("a second enabled provider can be disabled", func(t *testing.T) {
		second := e.forged(t, idp, noFallback("example.org"))
		if res, err := e.admin.DisableAuthProviderWithResponse(t.Context(), second); err != nil || res.JSON200 == nil {
			t.Errorf("disabling a second provider = %v: %s", res.StatusCode(), res.Body)
		}
	})
}

func TestAnEnforcedDomainSignsInThroughASAMLProvider(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := startIsolated(t)
	corp := e.createSAML(t, idp, func(c *management.AuthProviderCreate) {
		c.Domains, c.AutoProvision, c.LinkByEmail = []string{"corp.example"}, ptr(false), ptr(true)
	}).Slug
	bob := testkit.NewMember(t, e.pool, testkit.WithEmail("bob@corp.example"))
	enforce(t, e.admin, "corp.example", corp)

	wantSSORequired(t, "a password", passwordSignIn(t, e.env, bob.Email, bob.Password), corp)
	b, v := e.samlSignIn(t, idp, corp, "/", samlPerson(bob.Email))
	if v.URL.Path != "/" {
		t.Fatalf("the SAML sign-in ended at %s", v.URL)
	}
	if me := signedIn(t, b); me == nil || me.Member == nil || me.Member.Email != bob.Email {
		t.Errorf("signed in as %+v, want %s", me, bob.Email)
	}
}

func TestDisablingLocalLoginNeedsAnIdentityTheSameChangeStillLetsIn(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	other := e.forged(t, idp, noFallback("corp.example"))
	keeper := testkit.NewMember(t, e.pool, testkit.AsInstanceAdmin(), testkit.WithEmail("keeper@corp.example"))
	keeperAt := testkit.NewBrowser(t, e.srv.URL)
	keeperAt.SignIn(t, keeper.Email, keeper.Password)
	e.link(t, idp, keeperAt, other, "sub-keeper-other", nil)
	closeAndEnforce := func(s *management.AuthSettings) {
		s.LocalLoginEnabled = false
		s.SsoEnforcedDomains = []management.SsoEnforcedDomain{{Domain: "corp.example", Provider: corp}}
	}

	// The identity at other would be refused the keeper the moment their
	// own domain is enforced to corp: it keeps no way through.
	res := putSettings(t, keeperAt.ClientWithResponses, closeAndEnforce)
	wantRefused(t, "holding another provider's identity", res.StatusCode(), res.Body, res.ApplicationproblemJSON422,
		http.StatusUnprocessableEntity, "identity_required", "")
	if got := settingsWith(t, e.admin, func(*management.AuthSettings) {}); !got.LocalLoginEnabled || len(got.SsoEnforcedDomains) != 0 {
		t.Errorf("settings after the refusal = %+v, want them unchanged", got)
	}

	e.link(t, idp, keeperAt, corp, "sub-keeper-corp", nil)
	if res = putSettings(t, keeperAt.ClientWithResponses, closeAndEnforce); res.JSON200 == nil || res.JSON200.LocalLoginEnabled {
		t.Errorf("holding the enforced provider's identity = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestEnforcingADomainWhileLocalLoginIsOffNeedsAnIdentityItStillLetsIn(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := startIsolated(t)
	corp := e.forged(t, idp, noFallback("corp.example"))
	other := e.forged(t, idp, noFallback("corp.example"))
	keeper := testkit.NewMember(t, e.pool, testkit.AsInstanceAdmin(), testkit.WithEmail("keeper@corp.example"))
	keeperAt := testkit.NewBrowser(t, e.srv.URL)
	keeperAt.SignIn(t, keeper.Email, keeper.Password)
	e.link(t, idp, keeperAt, other, "sub-keeper-other", nil)
	if res := putSettings(t, keeperAt.ClientWithResponses, func(s *management.AuthSettings) { s.LocalLoginEnabled = false }); res.JSON200 == nil {
		t.Fatalf("closing local login while holding an identity = %d: %s", res.StatusCode(), res.Body)
	}

	// Local login is off already: enforcing the keeper's own domain to corp
	// would leave them no way in, the same as closing local login would.
	enforceCorp := func(s *management.AuthSettings) {
		s.LocalLoginEnabled = false
		s.SsoEnforcedDomains = []management.SsoEnforcedDomain{{Domain: "corp.example", Provider: corp}}
	}
	res := putSettings(t, keeperAt.ClientWithResponses, enforceCorp)
	wantRefused(t, "enforcing their domain to a provider they hold no identity at", res.StatusCode(), res.Body, res.ApplicationproblemJSON422,
		http.StatusUnprocessableEntity, "identity_required", "")

	// A change that touches neither local login nor the enforced domains is
	// not judged by the caller's ways in.
	if res = putSettings(t, keeperAt.ClientWithResponses, func(s *management.AuthSettings) {
		s.LocalLoginEnabled = false
		s.PasswordMinLength = 14
	}); res.JSON200 == nil {
		t.Errorf("raising the password minimum while local login is off = %d: %s", res.StatusCode(), res.Body)
	}

	e.link(t, idp, keeperAt, corp, "sub-keeper-corp", nil)
	if res = putSettings(t, keeperAt.ClientWithResponses, enforceCorp); res.JSON200 == nil {
		t.Errorf("enforcing with the enforced provider's identity held = %d: %s", res.StatusCode(), res.Body)
	}
}
