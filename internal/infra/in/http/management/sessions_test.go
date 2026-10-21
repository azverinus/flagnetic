package management_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// signedIn returns a browser of srv signed in as m.
func signedIn(t *testing.T, srv *testkit.Server, m testkit.Member) *testkit.Browser {
	t.Helper()
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, m.Email, m.Password)
	return b
}

// mySessions returns the sessions b's member is signed in to, as b reads
// them.
func mySessions(t *testing.T, b *testkit.Browser) []management.Session {
	t.Helper()
	res, err := b.ListMySessionsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("GET /me/sessions = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// currentOf returns the session marked current in sessions.
func currentOf(t *testing.T, sessions []management.Session) management.Session {
	t.Helper()
	for _, s := range sessions {
		if s.Current {
			return s
		}
	}
	t.Fatalf("no session marked current in %+v", sessions)
	return management.Session{}
}

// meStatus returns the status of GET /me through b and the problem's code.
func meStatus(t *testing.T, b *testkit.Browser) (int, string) {
	t.Helper()
	res, err := b.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON401; p != nil {
		return res.StatusCode(), p.Code
	}
	return res.StatusCode(), ""
}

// wantSignedOut fails unless b's session has ended.
func wantSignedOut(t *testing.T, b *testkit.Browser, who string) {
	t.Helper()
	if code, problem := meStatus(t, b); code != http.StatusUnauthorized || problem != "session_expired" {
		t.Errorf("%s: GET /me = %d %s, want 401 session_expired", who, code, problem)
	}
}

// wantSignedIn fails unless b's session still signs its member in.
func wantSignedIn(t *testing.T, b *testkit.Browser, who string) {
	t.Helper()
	if code, problem := meStatus(t, b); code != http.StatusOK {
		t.Errorf("%s: GET /me = %d %s, want 200", who, code, problem)
	}
}

func TestTheSessionListShowsEveryDeviceAndMarksTheCurrentOne(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	m := testkit.NewMember(t, testkit.Pool(t))
	first := signedIn(t, srv, m)
	opened := c.Now()
	c.Advance(time.Hour)
	second := signedIn(t, srv, m)

	sessions := mySessions(t, second)
	if len(sessions) != 2 || sessions[0].Id == sessions[1].Id {
		t.Fatalf("sessions = %+v, want two distinct", sessions)
	}
	current := currentOf(t, sessions)
	if current.Id != sessions[0].Id || sessions[1].Current {
		t.Errorf("sessions = %+v, want the newest first and only it current", sessions)
	}
	if !current.CreatedAt.Equal(c.Now()) || !current.LastSeenAt.Equal(c.Now()) || !current.ExpiresAt.Equal(c.Now().Add(30*24*time.Hour)) {
		t.Errorf("current session times = %+v, want opened now and ending in 30 days", current)
	}
	if other := sessions[1]; !other.CreatedAt.Equal(opened) {
		t.Errorf("other session opened at %v, want %v", other.CreatedAt, opened)
	}
	for _, s := range sessions {
		if s.Method != management.SessionMethodLocal || s.ClientIp == nil || *s.ClientIp != "127.0.0.1" ||
			!strings.Contains(s.UserAgent, "Go-http-client") {
			t.Errorf("session %+v, want a local sign-in from 127.0.0.1 by the Go client", s)
		}
	}
	res, err := second.ListMySessionsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []*testkit.Browser{first, second} {
		random, _, _ := strings.Cut(b.Cookie(management.SessionCookie), ".")
		if strings.Contains(string(res.Body), random) {
			t.Error("the session list carries a cookie's secret")
		}
	}

	token := testkit.PersonalToken(t, testkit.Pool(t), m.ID, "script", access.ScopeRead)
	byToken, err := testkit.Client(t, srv, token).ListMySessionsWithResponse(t.Context())
	if err != nil || byToken.JSON200 == nil {
		t.Fatalf("GET /me/sessions with a token = %v, %v", byToken.StatusCode(), err)
	}
	if len(byToken.JSON200.Items) != 2 || slices.ContainsFunc(byToken.JSON200.Items, func(s management.Session) bool { return s.Current }) {
		t.Errorf("sessions read with a token = %+v, want both and none current", byToken.JSON200.Items)
	}
}

func TestTheSessionListLeavesOutEndedSessions(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	m := testkit.NewMember(t, testkit.Pool(t))
	signedIn(t, srv, m) // left idle
	signedOut := signedIn(t, srv, m)
	if res, err := signedOut.SignOutWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("sign-out = %v, %v", res.StatusCode(), err)
	}
	live := signedIn(t, srv, m)
	for range 25 {
		c.Advance(time.Hour)
		wantSignedIn(t, live, "the session in use")
	}

	sessions := mySessions(t, live)
	if len(sessions) != 1 || !sessions[0].Current {
		t.Errorf("sessions = %+v, want only the current one", sessions)
	}
}

func TestRevokingOneSessionSignsThatDeviceOut(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	other := signedIn(t, srv, m)
	this := signedIn(t, srv, m)
	otherID := currentOf(t, mySessions(t, other)).Id

	res, err := this.RevokeMySessionWithResponse(t.Context(), otherID.String())
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /me/sessions/{other} = %v %s, %v; want 204", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("revoking another device set cookies %q, want none", got)
	}
	wantSignedOut(t, other, "the device revoked")
	wantSignedIn(t, this, "the device revoking")

	stranger := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	strangersID := currentOf(t, mySessions(t, stranger)).Id
	for name, session := range map[string]string{
		"revoked already":    otherID.String(),
		"another member's":   strangersID.String(),
		"not an identifier":  "not-an-id",
		"nobody's at all":    "01890000-0000-7000-8000-000000000000",
		"an empty-like path": "%20",
	} {
		res, err := this.RevokeMySessionWithResponse(t.Context(), session)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode() != http.StatusNotFound || res.ApplicationproblemJSON404 == nil || res.ApplicationproblemJSON404.Code != "not_found" {
			t.Errorf("%s: DELETE = %d %s, want 404 not_found", name, res.StatusCode(), res.Body)
		}
	}
	wantSignedIn(t, stranger, "another member")
}

func TestRevokingTheCurrentSessionClearsTheCookie(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)
	cookie := b.Cookie(management.SessionCookie)

	res, err := b.RevokeMySessionWithResponse(t.Context(), currentOf(t, mySessions(t, b)).Id.String())
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /me/sessions/{current} = %v %s, %v; want 204", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 1 || got[0] != clearedCookie {
		t.Errorf("Set-Cookie = %q, want the session cookie cleared", got)
	}
	b.SetCookie(management.SessionCookie, cookie)
	wantSignedOut(t, b, "the old cookie")
}

func TestRevokingAllKeepsTheCurrentSessionUnlessAskedOtherwise(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	others := []*testkit.Browser{signedIn(t, srv, m), signedIn(t, srv, m), signedIn(t, srv, m)}
	this := signedIn(t, srv, m)

	res, err := this.RevokeMySessionsWithResponse(t.Context(), &management.RevokeMySessionsParams{})
	if err != nil || res.JSON200 == nil || res.JSON200.Revoked != 3 {
		t.Fatalf("DELETE /me/sessions = %v %s, %v; want 3 revoked", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("keeping the current session set cookies %q, want none", got)
	}
	for _, b := range others {
		wantSignedOut(t, b, "another device")
	}
	wantSignedIn(t, this, "the current device")

	cookie := this.Cookie(management.SessionCookie)
	includeCurrent := true
	res, err = this.RevokeMySessionsWithResponse(t.Context(), &management.RevokeMySessionsParams{IncludeCurrent: &includeCurrent})
	if err != nil || res.JSON200 == nil || res.JSON200.Revoked != 1 {
		t.Fatalf("DELETE /me/sessions?include_current = %v %s, %v; want 1 revoked", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 1 || got[0] != clearedCookie {
		t.Errorf("Set-Cookie = %q, want the session cookie cleared", got)
	}
	this.SetCookie(management.SessionCookie, cookie)
	wantSignedOut(t, this, "the old cookie")
}

func TestAnInstanceAdminListsAndRevokesAnotherMembersSessions(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	member := signedIn(t, srv, m)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	list, err := admin.ListMemberSessionsWithResponse(t.Context(), m.ID.String())
	if err != nil || list.JSON200 == nil {
		t.Fatalf("GET /members/{member}/sessions = %v %s, %v", list.StatusCode(), list.Body, err)
	}
	if len(list.JSON200.Items) != 1 || list.JSON200.Items[0].Current {
		t.Errorf("sessions = %+v, want one, not current", list.JSON200.Items)
	}

	revoked, err := admin.RevokeMemberSessionsWithResponse(t.Context(), m.ID.String())
	if err != nil || revoked.JSON200 == nil || revoked.JSON200.Revoked != 1 {
		t.Fatalf("DELETE /members/{member}/sessions = %v %s, %v; want 1 revoked", revoked.StatusCode(), revoked.Body, err)
	}
	if got := revoked.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("revoking with a token set cookies %q, want none", got)
	}
	wantSignedOut(t, member, "the member")
	if list, err = admin.ListMemberSessionsWithResponse(t.Context(), m.ID.String()); err != nil || list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Errorf("sessions after revoking = %v %s, %v; want none", list.StatusCode(), list.Body, err)
	}

	for _, unknown := range []string{"01890000-0000-7000-8000-000000000000", "not-an-id"} {
		l, err := admin.ListMemberSessionsWithResponse(t.Context(), unknown)
		if err != nil || l.StatusCode() != http.StatusNotFound {
			t.Errorf("GET sessions of %s = %v, %v; want 404", unknown, l.StatusCode(), err)
		}
		r, err := admin.RevokeMemberSessionsWithResponse(t.Context(), unknown)
		if err != nil || r.StatusCode() != http.StatusNotFound {
			t.Errorf("DELETE sessions of %s = %v, %v; want 404", unknown, r.StatusCode(), err)
		}
	}
}

func TestAnInstanceAdminRevokingTheirOwnSessionsEndsTheCurrentOneToo(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := signedIn(t, srv, admin)

	res, err := b.RevokeMemberSessionsWithResponse(t.Context(), admin.ID.String())
	if err != nil || res.JSON200 == nil || res.JSON200.Revoked != 1 {
		t.Fatalf("DELETE /members/{self}/sessions = %v %s, %v; want 1 revoked", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 1 || got[0] != clearedCookie {
		t.Errorf("Set-Cookie = %q, want the session cookie cleared", got)
	}
}

func TestOnlyAnInstanceAdminReachesAnotherMembersSessions(t *testing.T) {
	srv := testkit.StartServer(t)
	victim := testkit.NewMember(t, testkit.Pool(t))
	victimsBrowser := signedIn(t, srv, victim)
	m := testkit.NewMember(t, testkit.Pool(t))
	for name, c := range map[string]*management.ClientWithResponses{
		"session": signedIn(t, srv, m).ClientWithResponses,
		// A token carrying instance:admin still acts for a member who is no
		// instance admin.
		"token": testkit.Client(t, srv, testkit.PersonalToken(t, testkit.Pool(t), m.ID, "script", access.ScopeRead, access.ScopeInstanceAdmin)),
	} {
		list, err := c.ListMemberSessionsWithResponse(t.Context(), victim.ID.String())
		if err != nil || list.ApplicationproblemJSON403 == nil || list.ApplicationproblemJSON403.Code != "forbidden" {
			t.Errorf("%s: GET another's sessions = %v %s, %v; want 403 forbidden", name, list.StatusCode(), list.Body, err)
		}
		revoke, err := c.RevokeMemberSessionsWithResponse(t.Context(), victim.ID.String())
		if err != nil || revoke.ApplicationproblemJSON403 == nil || revoke.ApplicationproblemJSON403.Code != "forbidden" {
			t.Errorf("%s: DELETE another's sessions = %v %s, %v; want 403 forbidden", name, revoke.StatusCode(), revoke.Body, err)
		}
	}
	wantSignedIn(t, victimsBrowser, "the member whose sessions were addressed")
}

func TestATokenCannotSignItsMemberOut(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)
	sessionID := currentOf(t, mySessions(t, b)).Id.String()
	every := []access.Scope{access.ScopeRead, access.ScopeFlagsWrite, access.ScopeExperimentsWrite, access.ScopeProjectAdmin}
	token := testkit.Client(t, srv, testkit.PersonalToken(t, testkit.Pool(t), m.ID, "leaked", every...))
	includeCurrent := true

	all, err := token.RevokeMySessionsWithResponse(t.Context(), &management.RevokeMySessionsParams{IncludeCurrent: &includeCurrent})
	if err != nil || all.ApplicationproblemJSON403 == nil || all.ApplicationproblemJSON403.Code != "session_required" {
		t.Errorf("DELETE /me/sessions with a token = %v %s, %v; want 403 session_required", all.StatusCode(), all.Body, err)
	}
	one, err := token.RevokeMySessionWithResponse(t.Context(), sessionID)
	if err != nil || one.ApplicationproblemJSON403 == nil || one.ApplicationproblemJSON403.Code != "session_required" {
		t.Errorf("DELETE /me/sessions/{id} with a token = %v %s, %v; want 403 session_required", one.StatusCode(), one.Body, err)
	}
	wantSignedIn(t, b, "the member")
}

func TestRevokingSessionsIsAudited(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	other, remaining := signedIn(t, srv, m), signedIn(t, srv, m)
	this := signedIn(t, srv, m)
	otherID, remainingID := currentOf(t, mySessions(t, other)).Id, currentOf(t, mySessions(t, remaining)).Id
	thisID := currentOf(t, mySessions(t, this)).Id

	if res, err := this.RevokeMySessionWithResponse(t.Context(), otherID.String()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /me/sessions/{other} = %v, %v", res.StatusCode(), err)
	}
	entries := auditOf(t, srv, "session.revoked", m.ID.String())
	if len(entries) != 1 {
		t.Fatalf("session.revoked entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Actor.Kind != management.ActorKindMember || e.Actor.Id != m.ID.String() || e.Entity.Kind != "member" || e.Entity.Key != m.Email {
		t.Errorf("entry = %+v, want the member revoking their own session", e)
	}
	if got := sessionIDsOf(t, e); !slices.Equal(got, []string{otherID.String()}) {
		t.Errorf("session_ids = %v, want the one revoked", got)
	}

	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	if res, err := admin.RevokeMemberSessionsWithResponse(t.Context(), m.ID.String()); err != nil || res.JSON200 == nil || res.JSON200.Revoked != 2 {
		t.Fatalf("admin revocation = %v %s, %v", res.StatusCode(), res.Body, err)
	}
	if res, err := admin.RevokeMemberSessionsWithResponse(t.Context(), m.ID.String()); err != nil || res.JSON200 == nil || res.JSON200.Revoked != 0 {
		t.Fatalf("revoking nothing = %v %s, %v; want 0", res.StatusCode(), res.Body, err)
	}
	entries = auditOf(t, srv, "session.revoked", m.ID.String())
	if len(entries) != 2 {
		t.Fatalf("session.revoked entries = %d, want 2: revoking nothing records nothing", len(entries))
	}
	want := []string{remainingID.String(), thisID.String()}
	slices.Sort(want)
	if got := sessionIDsOf(t, entries[0]); !slices.Equal(got, want) {
		t.Errorf("session_ids = %v, want both remaining sessions %v in order", got, want)
	}
	if entries[0].Actor.Kind != management.ActorKindToken || entries[0].Actor.MemberId == nil {
		t.Errorf("actor = %+v, want the instance admin's token", entries[0].Actor)
	}
}

// sessionIDsOf returns the session identifiers a session.revoked entry names.
func sessionIDsOf(t *testing.T, e management.AuditEntry) []string {
	t.Helper()
	after, ok := e.After.(map[string]any)
	if !ok {
		t.Fatalf("entry after = %v, want an object", e.After)
	}
	raw, _ := after["session_ids"].([]any)
	ids := make([]string, len(raw))
	for i, v := range raw {
		ids[i], _ = v.(string)
	}
	return ids
}

func TestRevokingASessionByAnIdentifierInAnotherCaseStillSignsThisDeviceOut(t *testing.T) {
	srv := testkit.StartServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	cookie := b.Cookie(management.SessionCookie)

	res, err := b.RevokeMySessionWithResponse(t.Context(), strings.ToUpper(currentOf(t, mySessions(t, b)).Id.String()))
	if err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /me/sessions/{ID} = %v %s, %v; want 204", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 1 || got[0] != clearedCookie {
		t.Errorf("Set-Cookie = %q, want the session cookie cleared", got)
	}
	b.SetCookie(management.SessionCookie, cookie)
	wantSignedOut(t, b, "the old cookie")
}

func TestAnEndedSessionIsRefusedWithTheCookieCleared(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	c.Advance(24 * time.Hour)

	res, err := b.GetMeWithResponse(t.Context())
	if err != nil || res.ApplicationproblemJSON401 == nil || res.ApplicationproblemJSON401.Code != "session_expired" {
		t.Fatalf("GET /me a day idle = %v %s, %v; want 401 session_expired", res.StatusCode(), res.Body, err)
	}
	if got := res.HTTPResponse.Header.Values("Set-Cookie"); len(got) != 1 || got[0] != clearedCookie {
		t.Errorf("Set-Cookie = %q, want the session cookie cleared", got)
	}
}

func TestTheAbsoluteTimeoutHoldsWhileTheSessionIsInUse(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	ends := currentOf(t, mySessions(t, b)).ExpiresAt
	for range 29 {
		c.Advance(23 * time.Hour)
		if got := currentOf(t, mySessions(t, b)).ExpiresAt; !got.Equal(ends) {
			t.Fatalf("expiry moved to %v while in use, want %v", got, ends)
		}
	}
}

func TestASessionsLastRequestIsWrittenAtMostOnceAMinute(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	opened := c.Now()
	for range 5 {
		c.Advance(10 * time.Second)
		if got := currentOf(t, mySessions(t, b)).LastSeenAt; !got.Equal(opened) {
			t.Fatalf("last seen = %v within a minute, want %v", got, opened)
		}
	}
	c.Set(opened.Add(70 * time.Second))
	wantSignedIn(t, b, "the member")
	if got := currentOf(t, mySessions(t, b)).LastSeenAt; !got.Equal(opened.Add(70 * time.Second)) {
		t.Errorf("last seen = %v after a minute, want the request's time", got)
	}
}
