package management_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/oapi-codegen/nullable"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// updateMe sends body as PATCH /me through b and returns the answer.
func updateMe(t *testing.T, b *testkit.Browser, body management.MePatch) *management.UpdateMeResponse {
	t.Helper()
	res, err := b.UpdateMeWithResponse(t.Context(), body)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// refusedWith fails unless problem carries code and, when pointer is not
// "", one violation at pointer with code violation.
func refusedWith(t *testing.T, name string, status int, body []byte, problem *management.Problem, code, pointer, violation string) {
	t.Helper()
	if problem == nil || problem.Code != code {
		t.Errorf("%s: %d %s, want %s", name, status, body, code)
		return
	}
	if pointer == "" {
		return
	}
	if problem.Errors == nil || len(*problem.Errors) != 1 || (*problem.Errors)[0].Pointer != pointer || (*problem.Errors)[0].Code != violation {
		t.Errorf("%s: violations %+v, want %s at %s", name, problem.Errors, violation, pointer)
	}
}

func TestAMemberRenamesThemselves(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)

	res := updateMe(t, b, management.MePatch{DisplayName: new("  Renamed  ")})
	if res.JSON200 == nil || res.JSON200.Member == nil || res.JSON200.Member.DisplayName != "Renamed" ||
		res.JSON200.Credential.Method != management.CredentialMethodSession {
		t.Fatalf("PATCH /me = %d %s, want 200 with the new name", res.StatusCode(), res.Body)
	}
	if got := mustMe(t, b); got.Member.DisplayName != "Renamed" || got.Member.Email != m.Email {
		t.Errorf("GET /me after = %+v, want the new name and the same email", got.Member)
	}
	if got := memberOf(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), m.ID); got.DisplayName != "Renamed" {
		t.Errorf("the instance admin reads %q, want the new name", got.DisplayName)
	}
	entries := auditOf(t, srv, "member.updated", m.ID.String())
	if len(entries) != 1 || entries[0].Actor.MemberId == nil || entries[0].Actor.MemberId.String() != m.ID.String() ||
		entries[0].Actor.Kind != management.ActorKindMember {
		t.Fatalf("member.updated entries = %+v, want one by the member", entries)
	}
	if before, after := entries[0].Before.(map[string]any), entries[0].After.(map[string]any); before["display_name"] != "Test Member" ||
		after["display_name"] != "Renamed" || len(after) != 1 {
		t.Errorf("entry before %v after %v, want only the name changed", before, after)
	}

	if res := updateMe(t, b, management.MePatch{DisplayName: new("Renamed")}); res.JSON200 == nil {
		t.Fatalf("an unchanged name = %d %s, want 200", res.StatusCode(), res.Body)
	}
	if n := len(auditOf(t, srv, "member.updated", m.ID.String())); n != 1 {
		t.Errorf("%d member.updated entries after an edit changing nothing, want still 1", n)
	}

	for name, tc := range map[string]struct {
		body               management.MePatch
		pointer, violation string
	}{
		"blank name": {management.MePatch{DisplayName: new(" ")}, "/display_name", "invalid_name"},
		"long name":  {management.MePatch{DisplayName: new(strings.Repeat("n", 201))}, "/display_name", "too_long"},
	} {
		res := updateMe(t, b, tc.body)
		refusedWith(t, name, res.StatusCode(), res.Body, res.ApplicationproblemJSON422, "invalid_member", tc.pointer, tc.violation)
	}
	if got := mustMe(t, b); got.Member.DisplayName != "Renamed" {
		t.Errorf("name after the refusals = %q, want Renamed", got.Member.DisplayName)
	}
}

func TestAMemberChoosesTheConsoleLanguageOrFollowsTheBrowser(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)
	if got := mustMe(t, b); got.Member.Locale != nil {
		t.Fatalf("a new member's locale = %q, want null: they follow the browser", *got.Member.Locale)
	}

	res := updateMe(t, b, management.MePatch{Locale: nullable.NewNullableWithValue("ru")})
	if res.JSON200 == nil || res.JSON200.Member.Locale == nil || *res.JSON200.Member.Locale != "ru" {
		t.Fatalf("choosing ru = %d %s", res.StatusCode(), res.Body)
	}
	// Renaming leaves the language alone.
	if renamed := updateMe(t, b, management.MePatch{DisplayName: new("Ada")}); renamed.JSON200 == nil ||
		renamed.JSON200.Member.Locale == nil || *renamed.JSON200.Member.Locale != "ru" {
		t.Fatalf("renaming = %d %s, want the language kept", renamed.StatusCode(), renamed.Body)
	}
	if got := mustMe(t, signedIn(t, srv, m)); got.Member.Locale == nil || *got.Member.Locale != "ru" {
		t.Errorf("another device reads %v, want ru", got.Member.Locale)
	}

	res = updateMe(t, b, management.MePatch{Locale: nullable.NewNullNullable[string]()})
	if res.JSON200 == nil || res.JSON200.Member.Locale != nil {
		t.Fatalf("following the browser = %d %s, want locale null", res.StatusCode(), res.Body)
	}
	entries := auditOf(t, srv, "member.updated", m.ID.String())
	if len(entries) != 3 {
		t.Fatalf("%d member.updated entries, want 3", len(entries))
	}
	if before, after := entries[0].Before.(map[string]any), entries[0].After.(map[string]any); before["locale"] != "ru" ||
		after["locale"] != nil || len(after) != 1 {
		t.Errorf("newest entry before %v after %v, want the locale from ru to null", before, after)
	}

	for _, locale := range []string{"de", "ru-RU", "EN"} {
		res := updateMe(t, b, management.MePatch{Locale: nullable.NewNullableWithValue(locale)})
		refusedWith(t, locale, res.StatusCode(), res.Body, res.ApplicationproblemJSON422, "invalid_member", "/locale", "unknown_locale")
	}
}

func TestAMemberEditsOnlyTheirOwnNameAndOnlyFromASession(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	token := testkit.PersonalToken(t, testkit.Pool(t), m.ID, "script", access.ScopeRead, access.ScopeProjectAdmin)

	res, err := testkit.Client(t, srv, token).UpdateMeWithResponse(t.Context(), management.MePatch{DisplayName: new("By a token")})
	if err != nil {
		t.Fatal(err)
	}
	refusedWith(t, "a token", res.StatusCode(), res.Body, res.ApplicationproblemJSON403, "session_required", "", "")

	// The email and the instance admin switch are not the member's to
	// change: the body has no such fields, and sending them changes
	// nothing.
	b := signedIn(t, srv, m)
	sent, body := b.Do(t, http.MethodPatch, management.BaseURL+"/me", "application/json",
		`{"display_name":"Ada","email":"someone@example.com","instance_admin":true}`)
	if sent.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /me with other fields = %d %s", sent.StatusCode, body)
	}
	if got := mustMe(t, b); got.Member.Email != m.Email || got.Member.InstanceAdmin || got.Member.DisplayName != "Ada" {
		t.Errorf("member after = %+v, want only the name changed", got.Member)
	}
}

// mustMe returns GET /me as b reads it, with its member.
func mustMe(t *testing.T, b *testkit.Browser) *management.Me {
	t.Helper()
	res, err := b.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil || res.JSON200.Member == nil {
		t.Fatalf("GET /me = %d %s", res.StatusCode(), res.Body)
	}
	return res.JSON200
}

// changePassword sends POST /me/password through b.
func changePassword(t *testing.T, b *testkit.Browser, current, next string) *management.ChangeMyPasswordResponse {
	t.Helper()
	testkit.RecordSecret(next)
	res, err := b.ChangeMyPasswordWithResponse(t.Context(), management.PasswordChange{CurrentPassword: current, NewPassword: next})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const changedPassword = "violet-harbour-lantern-4471"

func TestChangingThePasswordKeepsThisSessionAndRevokesTheOthers(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	other := signedIn(t, srv, m)
	another := signedIn(t, srv, m)
	this := signedIn(t, srv, m)
	if _, err := another.SignOutWithResponse(t.Context()); err != nil { // ended already: not counted
		t.Fatal(err)
	}

	if res := changePassword(t, this, m.Password, changedPassword); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("POST /me/password = %d %s, want 204", res.StatusCode(), res.Body)
	}
	wantSignedIn(t, this, "the device that changed it")
	wantSignedOut(t, other, "another device")

	if status, code, _ := signInAs(t, testkit.NewBrowser(t, srv.URL), m.Email, m.Password); status != http.StatusUnauthorized ||
		code != "invalid_credentials" {
		t.Errorf("the old password = %d %s, want 401 invalid_credentials", status, code)
	}
	signedIn(t, srv, testkit.Member{ID: m.ID, Email: m.Email, Password: changedPassword})

	entries := auditOf(t, srv, "password.changed", m.ID.String())
	if len(entries) != 1 || entries[0].Actor.MemberId == nil || entries[0].Actor.MemberId.String() != m.ID.String() {
		t.Fatalf("password.changed entries = %+v, want one by the member", entries)
	}
	after := entries[0].After.(map[string]any)
	if after["email"] != m.Email || after["sessions_revoked"] != float64(1) {
		t.Errorf("entry after = %v, want the email and one session revoked", after)
	}
	if raw, err := json.Marshal(entries); err != nil || strings.Contains(string(raw), changedPassword) {
		t.Error("the audit entry carries the new password")
	}
}

func TestChangingThePasswordChecksTheCurrentOneAndThePolicy(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)

	res := changePassword(t, b, wrongPassword, changedPassword)
	refusedWith(t, "a wrong current password", res.StatusCode(), res.Body, res.ApplicationproblemJSON422,
		"invalid_credentials", "/current_password", "invalid_credentials")
	for name, next := range map[string]string{"short": "short-pw-11", "the email's local part": strings.Split(m.Email, "@")[0] + "-x-y-z-1", "common": "password1234"} {
		res := changePassword(t, b, m.Password, next)
		refusedWith(t, name, res.StatusCode(), res.Body, res.ApplicationproblemJSON422, "weak_password", "/new_password", "weak_password")
	}
	wantSignedIn(t, b, "the device after the refusals")
	signedIn(t, srv, m) // the password is unchanged
}

func TestAWrongCurrentPasswordCountsTowardsTheLockout(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := fromAddress(t, srv, someAddress())
	b.SignIn(t, m.Email, m.Password)

	for range 4 {
		res := changePassword(t, b, wrongPassword, changedPassword)
		refusedWith(t, "wrong current password", res.StatusCode(), res.Body, res.ApplicationproblemJSON422, "invalid_credentials", "/current_password", "invalid_credentials")
	}
	res := changePassword(t, b, wrongPassword, changedPassword)
	refusedWith(t, "the fifth wrong current password", res.StatusCode(), res.Body, res.ApplicationproblemJSON422, "invalid_credentials", "/current_password", "invalid_credentials")
	if locked := auditOf(t, srv, "auth.locked_out", m.ID.String()); len(locked) != 1 {
		t.Errorf("auth.locked_out entries = %d, want 1 once the fifth failure locks the email", len(locked))
	}

	// Locked: refused before the password is checked, the right one
	// included, here and at sign-in.
	res = changePassword(t, b, m.Password, changedPassword)
	if p := res.ApplicationproblemJSON429; p == nil || p.Code != "locked" || p.RetryAfter == nil || *p.RetryAfter <= 0 {
		t.Errorf("a change while locked = %d %s, want 429 locked with retry_after", res.StatusCode(), res.Body)
	}
	if status, code, _ := signInAs(t, fromAddress(t, srv, someAddress()), m.Email, m.Password); status != http.StatusTooManyRequests || code != "locked" {
		t.Errorf("sign-in while locked = %d %s, want 429 locked", status, code)
	}
	wantSignedIn(t, b, "the device that guessed")
}

func TestAWeakNewPasswordIsRefusedBeforeTheCurrentOneIsCounted(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := fromAddress(t, srv, someAddress())
	b.SignIn(t, m.Email, m.Password)
	for range 4 {
		changePassword(t, b, wrongPassword, changedPassword)
	}

	// Four failures stand; five more would lock the email if a weak new
	// password let the wrong current one count.
	for range 5 {
		res := changePassword(t, b, wrongPassword, "short-pw-11")
		refusedWith(t, "a weak new password with a wrong current one", res.StatusCode(), res.Body, res.ApplicationproblemJSON422,
			"weak_password", "/new_password", "weak_password")
	}
	if res := changePassword(t, b, m.Password, changedPassword); res.StatusCode() != http.StatusNoContent {
		t.Errorf("the right current password after = %d %s, want 204: no weak attempt counted", res.StatusCode(), res.Body)
	}
	if locked := auditOf(t, srv, "auth.locked_out", m.ID.String()); len(locked) != 0 {
		t.Errorf("auth.locked_out entries = %d, want none", len(locked))
	}
}

func TestARightCurrentPasswordClearsTheFailuresBeforeIt(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := fromAddress(t, srv, someAddress())
	b.SignIn(t, m.Email, m.Password)
	for range 4 {
		changePassword(t, b, wrongPassword, changedPassword)
	}
	if res := changePassword(t, b, m.Password, changedPassword); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("the right current password = %d %s, want 204", res.StatusCode(), res.Body)
	}
	for range 4 {
		changePassword(t, b, wrongPassword, "another-violet-harbour-9")
	}
	if res := changePassword(t, b, changedPassword, "another-violet-harbour-9"); res.StatusCode() != http.StatusNoContent {
		t.Errorf("after four more failures = %d %s, want 204: the count started again", res.StatusCode(), res.Body)
	}
}

func TestChangingThePasswordIsRefusedFromAToken(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	token := testkit.PersonalToken(t, testkit.Pool(t), m.ID, "script", access.ScopeRead, access.ScopeProjectAdmin)
	res, err := testkit.Client(t, srv, token).ChangeMyPasswordWithResponse(t.Context(),
		management.PasswordChange{CurrentPassword: m.Password, NewPassword: changedPassword})
	if err != nil {
		t.Fatal(err)
	}
	refusedWith(t, "a token", res.StatusCode(), res.Body, res.ApplicationproblemJSON403, "session_required", "", "")
	signedIn(t, srv, m)
}

func TestChangingThePasswordIsRefusedFromAnotherSite(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)
	b.Header.Set("Sec-Fetch-Site", "cross-site")
	res := changePassword(t, b, m.Password, changedPassword)
	refusedWith(t, "a cross-site change", res.StatusCode(), res.Body, res.ApplicationproblemJSON403, "cross_origin", "", "")

	// The same origin needs no header of the console's own: the browser's
	// fetch metadata is enough.
	b.Header.Set("Sec-Fetch-Site", "same-origin")
	if res := changePassword(t, b, m.Password, changedPassword); res.StatusCode() != http.StatusNoContent {
		t.Errorf("a same-origin change = %d %s, want 204", res.StatusCode(), res.Body)
	}
}

func TestAPasswordChangeBodyOver8KiBIsRefusedBeforeAnything(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)
	body := `{"current_password":"` + strings.Repeat("x", 8<<10) + `","new_password":"` + changedPassword + `"}`
	res, err := b.ChangeMyPasswordWithBodyWithResponse(t.Context(), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	refusedWith(t, "an 8 KiB body", res.StatusCode(), res.Body, res.ApplicationproblemJSON413, "request_too_large", "", "")
}

func TestEveryMemberReadsTheInstancesTokenPolicyFromMe(t *testing.T) {
	srv, pool, admin := freshInstance(t, testkit.Clock())
	m := testkit.NewMember(t, pool)
	b := signedIn(t, srv, m)

	got := mustMe(t, b)
	if got.MaxTokenLifetimeSeconds == nil || *got.MaxTokenLifetimeSeconds != defaultSettings.TokenMaxLifetimeSeconds || got.RequireTokenExpiry {
		t.Errorf("GET /me on a fresh instance = %v %v, want %d and no expiry required",
			got.MaxTokenLifetimeSeconds, got.RequireTokenExpiry, defaultSettings.TokenMaxLifetimeSeconds)
	}

	changed := defaultSettings
	changed.TokenMaxLifetimeSeconds, changed.TokenExpiryRequired = 60*86400, true
	putSettings(t, admin, changed)
	got = mustMe(t, b)
	if got.MaxTokenLifetimeSeconds == nil || *got.MaxTokenLifetimeSeconds != 60*86400 || !got.RequireTokenExpiry {
		t.Errorf("GET /me after the change = %v %v, want 60 days and expiry required", got.MaxTokenLifetimeSeconds, got.RequireTokenExpiry)
	}
	if res := updateMe(t, b, management.MePatch{DisplayName: new("Ada")}); res.JSON200 == nil || !res.JSON200.RequireTokenExpiry {
		t.Errorf("PATCH /me = %d %s, want the token policy too", res.StatusCode(), res.Body)
	}
}
