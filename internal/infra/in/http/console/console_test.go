package console_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// start serves the console as serve does, English by default.
func start(t *testing.T) *testkit.Server {
	t.Helper()
	return testkit.StartServer(t, testkit.WithConsole("en"))
}

// getDocument fetches a path from the in-process server without following redirects
// and without any credential: what a browser asking for a document does.
func getDocument(t *testing.T, base, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestConsoleServesTheEntryDocumentAtTheRoot(t *testing.T) {
	h := start(t)

	res := getDocument(t, h.URL, "/")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the entry document names the hashed assets", got)
	}
}

func TestConsoleFallsBackToTheEntryDocumentForApplicationRoutes(t *testing.T) {
	h := start(t)

	// Routes the single-page application owns; the server knows none of them.
	for _, path := range []string{"/sign-in", "/projects/checkout", "/projects/checkout/flags?environment=production", "/deep/link/that/does/not/exist"} {
		res := getDocument(t, h.URL, path)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 with the entry document", path, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", path, ct)
		}
	}
}

func TestConsoleDoesNotAnswerForTheAPISDKAndOperationalPrefixes(t *testing.T) {
	h := start(t)

	// An unknown path under a prefix the server owns is a 404 from that
	// surface, never the entry document: an SDK or a script must never
	// parse HTML where it expects JSON.
	for _, path := range []string{"/api/v1/no-such-operation", "/api/v2/projects", "/sdk/v1/no-such-operation", "/healthz/extra", "/metrics/extra"} {
		res := getDocument(t, h.URL, path)
		if res.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200, want a refusal rather than the entry document", path)
		}
		if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want a machine-readable answer", path, ct)
		}
	}
}

func TestConsoleCachesHashedAssetsForeverAndNeverTheEntryDocument(t *testing.T) {
	h := start(t)

	// Whatever build the binary carries, its entry document names at least
	// one asset whose file name holds a content hash.
	asset := firstAsset(t, h.URL)
	res := getDocument(t, h.URL, asset)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", asset, res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want an immutable year", got)
	}
}

// firstAsset returns the first hashed asset the entry document names.
func firstAsset(t *testing.T, base string) string {
	t.Helper()
	res := getDocument(t, base, "/")
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`/assets/[A-Za-z0-9._-]+`).FindString(string(body))
	if match == "" {
		t.Fatalf("the entry document names no asset under %s:\n%s", "/assets/", body)
	}
	return match
}

func TestConsoleRefusesAMissingAsset(t *testing.T) {
	h := start(t)

	// A missing file under the asset prefix is a 404, never the entry
	// document: a stale hashed URL must fail loudly.
	res := getDocument(t, h.URL, "/assets/gone.0000000.js")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}

func TestConsoleDoesNotCacheARefusal(t *testing.T) {
	h := start(t)

	// A stale hashed URL must not be cached for a year, or the 404 outlives
	// the deploy that would have fixed it.
	res := getDocument(t, h.URL, "/assets/gone.0000000.js")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q, want no immutable caching on a refusal", got)
	}
}

func TestConsoleSendsASameOriginContentSecurityPolicy(t *testing.T) {
	h := start(t)

	res := getDocument(t, h.URL, "/")
	policy := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'self'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("Content-Security-Policy = %q, want it to contain %q", policy, want)
		}
	}
	if strings.Contains(policy, "unsafe-eval") {
		t.Errorf("Content-Security-Policy = %q, want no unsafe-eval", policy)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestConsoleRefusesAMethodOtherThanGETOrHEAD(t *testing.T) {
	h := start(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.URL+"/sign-in", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", res.StatusCode)
	}
}

func TestConsoleRefusesAnotherMethodNamingTheOnesItAnswers(t *testing.T) {
	h := start(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, h.URL+"/projects/checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("status = %d, Allow = %q, want 405 naming GET, HEAD", res.StatusCode, res.Header.Get("Allow"))
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want a problem document", ct)
	}
}

func TestConsoleRevalidatesAFileThatIsNotHashed(t *testing.T) {
	h := start(t)

	// The icon, like the script applying the theme before the first paint,
	// keeps its name across builds, so a browser must ask whether it changed.
	res := getDocument(t, h.URL, "/favicon.svg")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /favicon.svg = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

func TestConsoleNamesTheConfiguredDefaultLocale(t *testing.T) {
	h := testkit.StartServer(t, testkit.WithConsole("ru"))

	res := getDocument(t, h.URL, "/sign-in")
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `<meta name="flagnetic-default-locale" content="ru"`) {
		t.Errorf("the entry document does not name ru as the default locale:\n%s", body)
	}
}

func TestConsoleOpensAnInvitationLinkWithoutLoggingItsSecret(t *testing.T) {
	h := start(t)
	secret := "c2VjcmV0LXRoYXQtbXVzdC1uZXZlci1iZS1sb2dnZWQ"

	// The link an instance admin passes on is a console page: the browser
	// gets the entry document, and the log line names the path's pattern.
	res := getDocument(t, h.URL, "/invitations/"+secret)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET the invitation link = %d, want 200 with the entry document", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	var logged bool
	for _, r := range h.Logs.Records() {
		for key, value := range r.Attrs {
			if s, ok := value.(string); ok && strings.Contains(s, secret) {
				t.Errorf("log record %q holds the secret in %s: %s", r.Message, key, s)
			}
		}
		if r.Message == "request" && r.Attrs["path"] == "/invitations/{secret}" {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no request record with the path /invitations/{secret}: %v", h.Logs.Records())
	}
}
