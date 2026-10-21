package management_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// freshInstance starts a server timed by c on an instance of its own, whose
// settings no other test changes, and returns it with a client of its
// bootstrapped instance admin.
func freshInstance(t *testing.T, c clock.Clock) (*testkit.Server, *pgxpool.Pool, *management.ClientWithResponses) {
	t.Helper()
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool), testkit.WithClock(c))
	return srv, pool, testkit.Client(t, srv, testkit.Bootstrap(t, pool))
}

// defaultSettings are a fresh instance's auth settings as the API answers
// them.
var defaultSettings = management.AuthSettings{
	SessionIdleTimeoutSeconds: 86400, SessionAbsoluteTimeoutSeconds: 2592000, TokenMaxLifetimeSeconds: 31536000,
	TokenExpiryRequired: false, PasswordMinLength: 12,
}

func getSettings(t *testing.T, c *management.ClientWithResponses) management.AuthSettings {
	t.Helper()
	res, err := c.GetAuthSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("GET /instance/auth-settings = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func putSettings(t *testing.T, c *management.ClientWithResponses, s management.AuthSettings) management.AuthSettings {
	t.Helper()
	res, err := c.UpdateAuthSettingsWithResponse(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("PUT /instance/auth-settings = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func TestAuthSettingsDefaultOnAFreshInstance(t *testing.T) {
	_, _, admin := freshInstance(t, testkit.Clock())
	if got := getSettings(t, admin); got != defaultSettings {
		t.Errorf("settings = %+v, want %+v", got, defaultSettings)
	}
}

func TestReadingAndUpdatingEveryAuthSetting(t *testing.T) {
	_, _, admin := freshInstance(t, testkit.Clock())
	want := management.AuthSettings{
		SessionIdleTimeoutSeconds: 7200, SessionAbsoluteTimeoutSeconds: 172800, TokenMaxLifetimeSeconds: 604800,
		TokenExpiryRequired: true, PasswordMinLength: 16,
	}
	if got := putSettings(t, admin, want); got != want {
		t.Errorf("PUT answered %+v, want %+v", got, want)
	}
	if got := getSettings(t, admin); got != want {
		t.Errorf("GET after PUT = %+v, want %+v", got, want)
	}
	putSettings(t, admin, want) // the same settings again change nothing

	action := "auth_settings.updated"
	res, err := admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %v", res.StatusCode(), err)
	}
	if len(res.JSON200.Items) != 1 {
		t.Fatalf("auth_settings.updated entries = %d, want 1: settings left as they were record nothing", len(res.JSON200.Items))
	}
	e := res.JSON200.Items[0]
	before, _ := e.Before.(map[string]any)
	after, _ := e.After.(map[string]any)
	if e.Entity.Kind != "auth_settings" || before["session_idle_timeout_seconds"] != float64(86400) ||
		after["session_idle_timeout_seconds"] != float64(7200) || before["password_min_length"] != float64(12) ||
		after["password_min_length"] != float64(16) || after["token_expiry_required"] != true {
		t.Errorf("entry = %+v, want the settings before and after", e)
	}
}

func TestAuthSettingsValidationRefusals(t *testing.T) {
	_, _, admin := freshInstance(t, testkit.Clock())
	for name, tc := range map[string]struct {
		change  func(*management.AuthSettings)
		pointer string
		code    string
	}{
		"idle beyond absolute": {func(s *management.AuthSettings) {
			s.SessionIdleTimeoutSeconds, s.SessionAbsoluteTimeoutSeconds = 172800, 86400
		}, "/session_idle_timeout_seconds", "idle_beyond_absolute"},
		"idle below a minute": {func(s *management.AuthSettings) { s.SessionIdleTimeoutSeconds = 30 },
			"/session_idle_timeout_seconds", "out_of_range"},
		"absolute beyond ten years": {func(s *management.AuthSettings) { s.SessionAbsoluteTimeoutSeconds = 315360001 },
			"/session_absolute_timeout_seconds", "out_of_range"},
		"a token lifetime no duration holds": {func(s *management.AuthSettings) { s.TokenMaxLifetimeSeconds = 1 << 62 },
			"/token_max_lifetime_seconds", "out_of_range"},
		"a negative token lifetime": {func(s *management.AuthSettings) { s.TokenMaxLifetimeSeconds = -1 << 62 },
			"/token_max_lifetime_seconds", "out_of_range"},
		"a password minimum below the floor": {func(s *management.AuthSettings) { s.PasswordMinLength = 11 },
			"/password_min_length", "out_of_range"},
		"a password minimum above the ceiling": {func(s *management.AuthSettings) { s.PasswordMinLength = 129 },
			"/password_min_length", "out_of_range"},
	} {
		s := defaultSettings
		tc.change(&s)
		res, err := admin.UpdateAuthSettingsWithResponse(t.Context(), s)
		if err != nil {
			t.Fatal(err)
		}
		p := res.ApplicationproblemJSON422
		if p == nil || p.Code != "invalid_auth_settings" || p.Errors == nil ||
			!slices.ContainsFunc(*p.Errors, func(v management.Violation) bool { return v.Pointer == tc.pointer && v.Code == tc.code }) {
			t.Errorf("%s: PUT = %d %s, want 422 invalid_auth_settings with %s %s", name, res.StatusCode(), res.Body, tc.pointer, tc.code)
		}
	}
	if got := getSettings(t, admin); got != defaultSettings {
		t.Errorf("settings after refusals = %+v, want the defaults", got)
	}
}

func TestEverythingUnderAuthSettingsNeedsAnInstanceAdmin(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	for name, c := range map[string]*management.ClientWithResponses{
		"session": signedIn(t, srv, m).ClientWithResponses,
		"token": testkit.Client(t, srv, testkit.PersonalToken(t, testkit.Pool(t), m.ID, "script",
			access.ScopeRead, access.ScopeInstanceAdmin)),
	} {
		get, err := c.GetAuthSettingsWithResponse(t.Context())
		if err != nil || get.ApplicationproblemJSON403 == nil || get.ApplicationproblemJSON403.Code != "forbidden" {
			t.Errorf("%s: GET = %v %s, %v; want 403 forbidden", name, get.StatusCode(), get.Body, err)
		}
		put, err := c.UpdateAuthSettingsWithResponse(t.Context(), defaultSettings)
		if err != nil || put.ApplicationproblemJSON403 == nil || put.ApplicationproblemJSON403.Code != "forbidden" {
			t.Errorf("%s: PUT = %v %s, %v; want 403 forbidden", name, put.StatusCode(), put.Body, err)
		}
	}
	read := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t), access.ScopeRead))
	if put, err := read.UpdateAuthSettingsWithResponse(t.Context(), defaultSettings); err != nil ||
		put.ApplicationproblemJSON403 == nil || put.ApplicationproblemJSON403.Code != "insufficient_scope" {
		t.Errorf("PUT with a read-only token = %v %s, %v; want 403 insufficient_scope", put.StatusCode(), put.Body, err)
	}
}

func TestAChangedSessionIdleTimeoutExpiresASessionSooner(t *testing.T) {
	c := testkit.Clock()
	srv, pool, admin := freshInstance(t, c)
	b := signedIn(t, srv, testkit.NewMember(t, pool))
	c.Advance(30 * time.Minute)
	wantSignedIn(t, b, "half an hour on")

	s := defaultSettings
	s.SessionIdleTimeoutSeconds = 3600
	putSettings(t, admin, s)
	c.Advance(59 * time.Minute)
	wantSignedIn(t, b, "59 minutes idle under an hour's timeout")
	c.Advance(61 * time.Minute)
	wantSignedOut(t, b, "61 minutes idle under an hour's timeout")
}

func TestALoweredAbsoluteTimeoutEndsALiveSession(t *testing.T) {
	c := testkit.Clock()
	srv, pool, admin := freshInstance(t, c)
	m := testkit.NewMember(t, pool)
	b := signedIn(t, srv, m)
	for range 3 {
		c.Advance(time.Hour)
		wantSignedIn(t, b, "in use")
	}

	s := defaultSettings
	s.SessionIdleTimeoutSeconds, s.SessionAbsoluteTimeoutSeconds = 3600, 2*3600
	putSettings(t, admin, s)
	wantSignedOut(t, b, "three hours after opening under a two-hour absolute timeout")

	fresh := signedIn(t, srv, m)
	if got := currentOf(t, mySessions(t, fresh)).ExpiresAt; !got.Equal(c.Now().Add(2 * time.Hour)) {
		t.Errorf("a session opened now ends at %v, want two hours on", got)
	}
	s.SessionAbsoluteTimeoutSeconds = 90 * 24 * 3600
	putSettings(t, admin, s)
	if got := currentOf(t, mySessions(t, fresh)).ExpiresAt; !got.Equal(c.Now().Add(2 * time.Hour)) {
		t.Errorf("after raising the timeout the session ends at %v, want still two hours on", got)
	}
}

func TestAnAuthSettingsChangeIsAppliedByAnotherReplicaWithinASecond(t *testing.T) {
	_, pool, adminOfA := freshInstance(t, testkit.Clock())
	b := testkit.StartServer(t, testkit.WithPool(pool))
	adminOfB := testkit.Client(t, b, testkit.Bootstrap(t, pool))
	listening := `flagnetic_listener_up{channel="auth_settings"}`
	for deadline := time.Now().Add(5 * time.Second); testkit.Metric(t, b.Scrape(t), listening) != 1; {
		if time.Now().After(deadline) {
			t.Fatal("replica B is not listening for auth settings changes")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := getSettings(t, adminOfB); got != defaultSettings { // B now holds its copy
		t.Fatalf("B's settings = %+v, want the defaults", got)
	}

	s := defaultSettings
	s.PasswordMinLength = 20
	putSettings(t, adminOfA, s)
	changed := time.Now()
	for getSettings(t, adminOfB).PasswordMinLength != 20 {
		if time.Since(changed) > time.Second {
			t.Fatal("replica B did not apply the change within a second")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
