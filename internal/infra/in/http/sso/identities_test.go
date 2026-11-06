package sso_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// memberAt is a member with a password, signed in in a browser.
type memberAt struct {
	testkit.Member
	b *testkit.Browser
}

func (e env) passwordMember(t *testing.T) memberAt {
	t.Helper()
	m := testkit.NewMember(t, testkit.Pool(t))
	b := testkit.NewBrowser(t, e.srv.URL)
	b.SignIn(t, m.Email, m.Password)
	return memberAt{Member: m, b: b}
}

// provisionedMember signs a person in through slug, which provisions a member
// without a password.
func (e env) provisionedMember(t *testing.T, idp *testkit.ForgingIDP, slug, subject string) (string, *testkit.Browser) {
	t.Helper()
	email := uniqueEmail()
	idp.NextClaims(map[string]any{"email": email, "sub": subject})
	b := testkit.NewBrowser(t, e.srv.URL)
	if v := e.signIn(t, b, slug, "", e.opts()); signedIn(t, b) == nil {
		t.Fatalf("provisioning a member through %s ended at %s", slug, v.URL)
	}
	return email, b
}

// link links an identity with subject at slug to the member signed in in b
// and follows the provider's answer to the end.
func (e env) link(t *testing.T, idp *testkit.ForgingIDP, b *testkit.Browser, slug, subject string, returnTo *string) testkit.Visit {
	t.Helper()
	idp.NextClaims(map[string]any{"email": uniqueEmail(), "email_verified": false, "sub": subject})
	res, err := b.LinkMyIdentityWithResponse(t.Context(), slug, management.IdentityLinkStart{ReturnTo: returnTo})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("POST /me/identities/%s/link = %v, %v: %s", slug, res.StatusCode(), err, res.Body)
	}
	return b.Visit(t, res.JSON200.StartUrl, e.opts())
}

func methodsOf(t *testing.T, b *testkit.Browser) management.MemberAuthMethods {
	t.Helper()
	res, err := b.ListMyIdentitiesWithResponse(t.Context())
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /me/identities = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	return *res.JSON200
}

func identityAt(t *testing.T, m management.MemberAuthMethods, slug string) management.LinkedIdentity {
	t.Helper()
	for _, i := range m.Identities {
		if i.Provider == slug {
			return i
		}
	}
	t.Fatalf("no identity at %s among %+v", slug, m.Identities)
	return management.LinkedIdentity{}
}

func wantSettings(t *testing.T, v testkit.Visit, errCode string) {
	t.Helper()
	if v.URL.Path != "/settings" || v.Query("error") != errCode {
		t.Errorf("the link ended at %s, want /settings with error=%q", v.URL, errCode)
	}
}

func TestTheAuthMethodsListThePasswordAndEveryIdentity(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	first, second := e.forged(t, idp, nil), e.forged(t, idp, nil)
	m := e.passwordMember(t)

	none := methodsOf(t, m.b)
	if !none.Password.Set || none.Password.SetAt == nil || len(none.Identities) != 0 {
		t.Fatalf("a member with a password and no identity: %+v", none)
	}
	e.link(t, idp, m.b, first, "first-subject", nil)
	e.link(t, idp, m.b, second, "second-subject", nil)

	got := methodsOf(t, m.b)
	if len(got.Identities) != 2 {
		t.Fatalf("identities = %+v, want two", got.Identities)
	}
	i := identityAt(t, got, first)
	if i.Subject != "first-subject" || i.Kind != management.AuthProviderKindOidc || !i.ProviderEnabled || i.LastSignInAt != nil {
		t.Errorf("the first identity = %+v", i)
	}

	// An instance admin sees the same; another member does not.
	res, err := e.admin.ListMemberIdentitiesWithResponse(t.Context(), m.ID.String())
	if err != nil || res.JSON200 == nil || len(res.JSON200.Identities) != 2 || !res.JSON200.Password.Set {
		t.Fatalf("GET /members/{m}/identities = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	other := e.passwordMember(t)
	denied, err := other.b.ListMemberIdentitiesWithResponse(t.Context(), m.ID.String())
	if err != nil || denied.StatusCode() != http.StatusForbidden {
		t.Errorf("another member reading the identities = %v, %v", denied.StatusCode(), err)
	}
	if res, err := e.admin.ListMemberIdentitiesWithResponse(t.Context(), "00000000-0000-4000-8000-000000000000"); err != nil || res.StatusCode() != http.StatusNotFound {
		t.Errorf("the identities of an unknown member = %v, %v", res.StatusCode(), err)
	}
}

func TestSigningInRecordsTheLastSignInOfTheIdentity(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	_, b := e.provisionedMember(t, idp, slug, "sso-subject")

	methods := methodsOf(t, b)
	if methods.Password.Set || methods.Password.SetAt != nil {
		t.Errorf("a member signed in through a provider holds a password: %+v", methods.Password)
	}
	if i := identityAt(t, methods, slug); i.LastSignInAt == nil {
		t.Error("the identity records no sign-in")
	}
}

func TestLinkingWhileSignedInAddsTheIdentityWhateverItsEmail(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision = false })
	m := e.passwordMember(t)
	session := m.b.Cookie(management.SessionCookie)

	v := e.link(t, idp, m.b, slug, "link-subject", ptr("/settings/profile"))

	if v.URL.Path != "/settings/profile" || v.Query("error") != "" {
		t.Fatalf("the link ended at %s", v.URL)
	}
	if got := m.b.Cookie(management.SessionCookie); got != session {
		t.Error("linking replaced the session")
	}
	if m.b.Cookie("flagnetic_sso") != "" {
		t.Error("the attempt cookie outlived the link")
	}
	if i := identityAt(t, methodsOf(t, m.b), slug); i.Subject != "link-subject" {
		t.Errorf("identity = %+v", i)
	}
	if got := e.audited(t, "identity.linked", "member_email", m.Email); len(got) != 1 || got[0]["provider"] != slug {
		t.Errorf("identity.linked = %v", got)
	}

	// The identity signs its member in from another browser.
	idp.NextClaims(map[string]any{"email": uniqueEmail(), "sub": "link-subject"})
	other := testkit.NewBrowser(t, e.srv.URL)
	e.signIn(t, other, slug, "", e.opts())
	if me := signedIn(t, other); me == nil || me.Member.Email != m.Email {
		t.Errorf("signing in through the linked identity = %+v", me)
	}
}

func TestLinkingToAnAnswerThatLeavesNoReturnToLandsOnTheSettings(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	m := e.passwordMember(t)

	for name, returnTo := range map[string]*string{"absent": nil, "off this instance": ptr("https://evil.example/x"), "backslash": ptr(`/\evil.example`)} {
		t.Run(name, func(t *testing.T) {
			v := e.link(t, idp, m.b, slug, "subject-"+strings.ReplaceAll(name, " ", "-"), returnTo)
			if v.URL.Path != "/settings" || v.URL.Host != strings.TrimPrefix(e.srv.URL, "http://") {
				t.Errorf("the link ended at %s", v.URL)
			}
		})
	}
}

func TestLinkingAnIdentityLinkedToAnotherMemberLinksNothing(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	holder, latecomer := e.passwordMember(t), e.passwordMember(t)
	e.link(t, idp, holder.b, slug, "taken-subject", nil)

	v := e.link(t, idp, latecomer.b, slug, "taken-subject", nil)

	wantSettings(t, v, "identity_linked")
	if n := len(methodsOf(t, latecomer.b).Identities); n != 0 {
		t.Errorf("the latecomer holds %d identities", n)
	}
	if n := len(methodsOf(t, holder.b).Identities); n != 1 {
		t.Errorf("the holder holds %d identities", n)
	}
	if n := len(e.audited(t, "identity.linked", "member_email", latecomer.Email)); n != 0 {
		t.Errorf("%d identity.linked entries for the latecomer", n)
	}
}

func TestLinkingAnIdentityTheMemberHoldsAlreadyLinksItOnce(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	m := e.passwordMember(t)

	e.link(t, idp, m.b, slug, "again-subject", nil)
	v := e.link(t, idp, m.b, slug, "again-subject", nil)

	if v.URL.Query().Get("error") != "" {
		t.Errorf("linking the same subject twice ended at %s", v.URL)
	}
	if n := len(methodsOf(t, m.b).Identities); n != 1 {
		t.Errorf("%d identities after linking one subject twice", n)
	}
	if n := len(e.audited(t, "identity.linked", "member_email", m.Email)); n != 1 {
		t.Errorf("%d identity.linked entries for one link", n)
	}
}

func TestALinkCompletesOnlyInTheBrowserAndSessionThatStartedIt(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)

	hold := func(t *testing.T, m memberAt) string {
		t.Helper()
		idp.NextClaims(map[string]any{"email": uniqueEmail(), "sub": "held-" + m.Email})
		res, err := m.b.LinkMyIdentityWithResponse(t.Context(), slug, management.IdentityLinkStart{})
		if err != nil || res.JSON200 == nil {
			t.Fatalf("starting a link = %v, %v", res.StatusCode(), err)
		}
		o := e.opts()
		o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
		return m.b.Visit(t, res.JSON200.StartUrl, o).URL.String()
	}
	done := func(t *testing.T, m memberAt) {
		t.Helper()
		if n := len(methodsOf(t, m.b).Identities); n != 0 {
			t.Errorf("%d identities linked", n)
		}
	}

	t.Run("another browser of the same member", func(t *testing.T) {
		m := e.passwordMember(t)
		completion := hold(t, m)
		other := testkit.NewBrowser(t, e.srv.URL)
		other.SignIn(t, m.Email, m.Password)

		wantSettings(t, other.Visit(t, completion, testkit.VisitOptions{}), "attempt_mismatch")
		done(t, m)
	})
	t.Run("a browser signed in as another member", func(t *testing.T) {
		m, intruder := e.passwordMember(t), e.passwordMember(t)
		completion := hold(t, m)

		wantSettings(t, intruder.b.Visit(t, completion, testkit.VisitOptions{}), "attempt_mismatch")
		done(t, m)
		done(t, intruder)
	})
	t.Run("the same browser signed in afresh", func(t *testing.T) {
		m := e.passwordMember(t)
		completion := hold(t, m)
		m.b.SignIn(t, m.Email, m.Password)

		// The attempt was bound to the session that started it, and is spent.
		if v := m.b.Visit(t, completion, testkit.VisitOptions{}); v.Query("error") == "" {
			t.Errorf("the link ended at %s", v.URL)
		}
		done(t, m)
	})
	t.Run("a browser signed out", func(t *testing.T) {
		m := e.passwordMember(t)
		completion := hold(t, m)
		if res, err := m.b.SignOutWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
			t.Fatalf("signing out = %v, %v", res.StatusCode(), err)
		}

		if v := m.b.Visit(t, completion, testkit.VisitOptions{}); v.Query("error") == "" {
			t.Errorf("the link ended at %s", v.URL)
		}
		if m.b.Cookie(management.SessionCookie) != "" {
			t.Error("completing a link opened a session")
		}
	})
}

func TestOnlyASessionStartsALinkAndOnlyAnEnabledProviderIsLinked(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	m := e.passwordMember(t)

	token := testkit.PersonalToken(t, testkit.Pool(t), m.ID, "link")
	viaToken := testkit.Client(t, e.srv, token)
	if res, err := viaToken.LinkMyIdentityWithResponse(t.Context(), slug, management.IdentityLinkStart{}); err != nil || res.StatusCode() != http.StatusForbidden {
		t.Errorf("a token starting a link = %v, %v", res.StatusCode(), err)
	}
	if res, err := m.b.LinkMyIdentityWithResponse(t.Context(), "nope", management.IdentityLinkStart{}); err != nil || res.StatusCode() != http.StatusNotFound {
		t.Errorf("linking an unknown provider = %v, %v", res.StatusCode(), err)
	}
	disable, err := e.admin.DisableAuthProviderWithResponse(t.Context(), slug)
	if err != nil || disable.StatusCode() != http.StatusOK {
		t.Fatalf("disabling the provider = %v, %v: %s", disable.StatusCode(), err, disable.Body)
	}
	if res, err := m.b.LinkMyIdentityWithResponse(t.Context(), slug, management.IdentityLinkStart{}); err != nil || res.StatusCode() != http.StatusNotFound {
		t.Errorf("linking a disabled provider = %v, %v", res.StatusCode(), err)
	}
	// The identity of a token reads the methods all the same.
	if res, err := viaToken.ListMyIdentitiesWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusOK {
		t.Errorf("a token reading the identities = %v, %v", res.StatusCode(), err)
	}
}

func TestAnIdentityIsUnlinkedWhileAnotherWayInRemains(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision = false })
	m := e.passwordMember(t)
	e.link(t, idp, m.b, slug, "unlink-subject", nil)
	linked := identityAt(t, methodsOf(t, m.b), slug)

	res, err := m.b.UnlinkMyIdentityWithResponse(t.Context(), linked.Id.String())

	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /me/identities/{identity} = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if n := len(methodsOf(t, m.b).Identities); n != 0 {
		t.Errorf("%d identities after unlinking", n)
	}
	if got := e.auditedBefore(t, "identity.unlinked", "member_email", m.Email); len(got) != 1 || got[0]["provider"] != slug {
		t.Errorf("identity.unlinked = %v", got)
	}
	// The identity no longer signs anyone in.
	idp.NextClaims(map[string]any{"email": uniqueEmail(), "sub": "unlink-subject"})
	again := testkit.NewBrowser(t, e.srv.URL)
	wantError(t, e.signIn(t, again, slug, "", e.opts()), again, "member_not_found")

	// Gone, or another member's, is not found.
	if res, err := m.b.UnlinkMyIdentityWithResponse(t.Context(), linked.Id.String()); err != nil || res.StatusCode() != http.StatusNotFound {
		t.Errorf("unlinking twice = %v, %v", res.StatusCode(), err)
	}
	if res, err := m.b.UnlinkMyIdentityWithResponse(t.Context(), "not-an-identifier"); err != nil || res.StatusCode() != http.StatusNotFound {
		t.Errorf("unlinking a malformed identifier = %v, %v", res.StatusCode(), err)
	}
}

func TestTheLastWayInIsNeverUnlinked(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	_, b := e.provisionedMember(t, idp, slug, "only-subject")
	only := identityAt(t, methodsOf(t, b), slug)

	res, err := b.UnlinkMyIdentityWithResponse(t.Context(), only.Id.String())

	if err != nil || res.StatusCode() != http.StatusConflict || !strings.Contains(string(res.Body), `"last_auth_method"`) {
		t.Fatalf("unlinking the only identity of a member without a password = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if n := len(methodsOf(t, b).Identities); n != 1 {
		t.Errorf("%d identities after the refusal", n)
	}
	if n := len(e.audited(t, "identity.unlinked", "provider", slug)); n != 0 {
		t.Errorf("%d identity.unlinked entries for a refused unlink", n)
	}
}

func TestAnIdentityAtADisabledProviderIsNoWayIn(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	disabled, backup := e.forged(t, idp, nil), e.forged(t, idp, nil)
	m := e.passwordMember(t)
	e.link(t, idp, m.b, disabled, "disabled-subject", nil)
	e.link(t, idp, m.b, backup, "backup-subject", nil)
	if res, err := m.b.RemoveMyPasswordWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing the password = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if res, err := e.admin.DisableAuthProviderWithResponse(t.Context(), disabled); err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("disabling the provider = %v, %v: %s", res.StatusCode(), err, res.Body)
	}

	methods := methodsOf(t, m.b)
	if identityAt(t, methods, disabled).ProviderEnabled {
		t.Error("an identity at a disabled provider is listed as enabled")
	}
	// The backup is the only way in left.
	res, err := m.b.UnlinkMyIdentityWithResponse(t.Context(), identityAt(t, methods, backup).Id.String())
	if err != nil || res.StatusCode() != http.StatusConflict {
		t.Errorf("unlinking the identity that is the only way in = %v, %v", res.StatusCode(), err)
	}
	// The one at the disabled provider was no way in, so it goes.
	res, err = m.b.UnlinkMyIdentityWithResponse(t.Context(), identityAt(t, methods, disabled).Id.String())
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Errorf("unlinking an identity at a disabled provider = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
}

func TestAPasswordIsRemovedWhileAnIdentityRemains(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	m := e.passwordMember(t)

	res, err := m.b.RemoveMyPasswordWithResponse(t.Context())
	if err != nil || res.StatusCode() != http.StatusConflict || !strings.Contains(string(res.Body), `"last_auth_method"`) {
		t.Fatalf("removing the only way in = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if !methodsOf(t, m.b).Password.Set {
		t.Fatal("the refused removal removed the password")
	}

	e.link(t, idp, m.b, slug, "keeper-subject", nil)
	res, err = m.b.RemoveMyPasswordWithResponse(t.Context())
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing the password beside an identity = %v, %v: %s", res.StatusCode(), err, res.Body)
	}

	if p := methodsOf(t, m.b).Password; p.Set || p.SetAt != nil {
		t.Errorf("password = %+v after removing it", p)
	}
	if got := e.audited(t, "password.removed", "email", m.Email); len(got) != 1 {
		t.Errorf("password.removed = %v", got)
	}
	fresh := testkit.NewBrowser(t, e.srv.URL)
	signIn, err := fresh.SignInWithResponse(t.Context(), management.SignInRequest{Email: m.Email, Password: m.Password})
	if err != nil || signIn.JSON200 != nil {
		t.Errorf("signing in with the removed password = %v, %v", signIn.StatusCode(), err)
	}
	// Removing what is not there changes nothing.
	if res, err := m.b.RemoveMyPasswordWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Errorf("removing the password twice = %v, %v", res.StatusCode(), err)
	}
	if got := e.audited(t, "password.removed", "email", m.Email); len(got) != 1 {
		t.Errorf("password.removed = %d entries after a second removal", len(got))
	}
}

func TestOnlyASessionRemovesAPassword(t *testing.T) {
	e := start(t)
	m := e.passwordMember(t)
	token := testkit.PersonalToken(t, testkit.Pool(t), m.ID, "remove")

	res, err := testkit.Client(t, e.srv, token).RemoveMyPasswordWithResponse(t.Context())

	if err != nil || res.StatusCode() != http.StatusForbidden {
		t.Errorf("a token removing the password = %v, %v", res.StatusCode(), err)
	}
}

func TestAnInstanceAdminUnlinksAMembersIdentityUnderTheSameRules(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	email, ssoOnly := e.provisionedMember(t, idp, slug, "sso-only-subject")
	ssoOnlyID := memberID(t, e, email)
	ssoIdentity := identityAt(t, methodsOf(t, ssoOnly), slug)
	withPassword := e.passwordMember(t)
	e.link(t, idp, withPassword.b, slug, "with-password-subject", nil)
	passwordIdentity := identityAt(t, methodsOf(t, withPassword.b), slug)

	// The last way in of a member stays, whoever unlinks it.
	res, err := e.admin.UnlinkMemberIdentityWithResponse(t.Context(), ssoOnlyID, ssoIdentity.Id.String())
	if err != nil || res.StatusCode() != http.StatusConflict {
		t.Errorf("unlinking an only way in = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	// An identity of another member is not found under this one.
	res, err = e.admin.UnlinkMemberIdentityWithResponse(t.Context(), ssoOnlyID, passwordIdentity.Id.String())
	if err != nil || res.StatusCode() != http.StatusNotFound {
		t.Errorf("unlinking an identity of another member = %v, %v", res.StatusCode(), err)
	}
	// Only an instance admin does it.
	res, err = withPassword.b.UnlinkMemberIdentityWithResponse(t.Context(), withPassword.ID.String(), passwordIdentity.Id.String())
	if err != nil || res.StatusCode() != http.StatusForbidden {
		t.Errorf("a member unlinking through the instance admin's address = %v, %v", res.StatusCode(), err)
	}
	res, err = e.admin.UnlinkMemberIdentityWithResponse(t.Context(), withPassword.ID.String(), passwordIdentity.Id.String())
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("unlinking with the password beside = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if got := e.auditedBefore(t, "identity.unlinked", "member_email", withPassword.Email); len(got) != 1 {
		t.Errorf("identity.unlinked = %v", got)
	}
	if signedIn(t, withPassword.b) == nil {
		t.Error("the member's session ended with the unlink")
	}

	// A deactivated member signs in nowhere, so their last identity may go.
	if res, err := e.admin.DeactivateMemberWithResponse(t.Context(), ssoOnlyID); err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	res, err = e.admin.UnlinkMemberIdentityWithResponse(t.Context(), ssoOnlyID, ssoIdentity.Id.String())
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Errorf("unlinking from a deactivated member = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
}

func TestAProviderWithIdentitiesIsDeletedOnceTheyAreUnlinked(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	m := e.passwordMember(t)
	e.link(t, idp, m.b, slug, "delete-subject", nil)

	res, err := e.admin.DeleteAuthProviderWithResponse(t.Context(), slug)
	if err != nil || res.StatusCode() != http.StatusConflict {
		t.Fatalf("deleting a provider with an identity = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	un, err := e.admin.UnlinkMemberIdentityWithResponse(t.Context(), m.ID.String(), identityAt(t, methodsOf(t, m.b), slug).Id.String())
	if err != nil || un.StatusCode() != http.StatusNoContent {
		t.Fatalf("unlinking = %v, %v", un.StatusCode(), err)
	}
	res, err = e.admin.DeleteAuthProviderWithResponse(t.Context(), slug)
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting the provider once unlinked = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
}

func TestReactivatingAMemberWhoHoldsAnIdentityMakesThemActive(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	email, _ := e.provisionedMember(t, idp, slug, "back-subject")
	id := memberID(t, e, email)
	if res, err := e.admin.DeactivateMemberWithResponse(t.Context(), id); err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating = %v, %v: %s", res.StatusCode(), err, res.Body)
	}

	res, err := e.admin.ReactivateMemberWithResponse(t.Context(), id)

	if err != nil || res.JSON200 == nil || res.JSON200.Member.Status != management.MemberStatusActive {
		t.Fatalf("reactivating a member with an identity and no password = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if res.JSON200.Invitation != nil {
		t.Error("reactivating an active member issued an invitation")
	}
}

func TestReactivatingAMemberWithNoWayInInvitesThemAgain(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	email, _ := e.provisionedMember(t, idp, slug, "gone-subject")
	id := memberID(t, e, email)
	deactivate, err := e.admin.DeactivateMemberWithResponse(t.Context(), id)
	if err != nil || deactivate.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating = %v, %v", deactivate.StatusCode(), err)
	}
	linked, err := e.admin.ListMemberIdentitiesWithResponse(t.Context(), id)
	if err != nil || linked.JSON200 == nil || len(linked.JSON200.Identities) != 1 {
		t.Fatalf("listing identities = %v, %v", linked.StatusCode(), err)
	}
	un, err := e.admin.UnlinkMemberIdentityWithResponse(t.Context(), id, linked.JSON200.Identities[0].Id.String())
	if err != nil || un.StatusCode() != http.StatusNoContent {
		t.Fatalf("unlinking = %v, %v", un.StatusCode(), err)
	}

	res, err := e.admin.ReactivateMemberWithResponse(t.Context(), id)

	if err != nil || res.JSON200 == nil || res.JSON200.Member.Status != management.MemberStatusInvited || res.JSON200.Invitation == nil {
		t.Errorf("reactivating a member with no way in = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
}

// auditedBefore returns what entries of action held in before, where the
// field key equals value.
func (e env) auditedBefore(t *testing.T, action, key, value string) []map[string]any {
	t.Helper()
	res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %v", res.StatusCode(), err)
	}
	var out []map[string]any
	for _, entry := range res.JSON200.Items {
		if before, _ := entry.Before.(map[string]any); before != nil && before[key] == value {
			out = append(out, before)
		}
	}
	return out
}

// memberID returns the identifier of the member holding email.
func memberID(t *testing.T, e env, email string) string {
	t.Helper()
	res, err := e.admin.ListMembersWithResponse(t.Context(), &management.ListMembersParams{Q: &email})
	if err != nil || res.JSON200 == nil || len(res.JSON200.Items) != 1 {
		t.Fatalf("finding %s: %v, %v", email, res.StatusCode(), err)
	}
	return res.JSON200.Items[0].Id.String()
}
