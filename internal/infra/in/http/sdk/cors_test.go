package sdk_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const exposed = "ETag, Retry-After, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset"

// send sends a request to srv's SDK API with the headers given as name,
// value pairs, and reads the answer whole.
func send(t *testing.T, srv *testkit.Server, method, path string, body []byte, headers ...string) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, _ := io.ReadAll(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: got}
}

// clientKeyWithOrigins creates a client key of production allowing origins.
func (p project) clientKeyWithOrigins(t *testing.T, origins ...string) (management.SdkKey, string) {
	t.Helper()
	res := do(p.admin.CreateSdkKeyWithResponse(t.Context(), p.key, "production",
		management.SdkKeyCreate{Kind: "client", Name: "web", AllowedOrigins: &origins}))
	if res.JSON201 == nil {
		t.Fatalf("POST …/sdk-keys = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON201.Key, res.JSON201.Value
}

// browserHeaders fails unless h lets a browser's script read the answer
// from any origin.
func browserHeaders(t *testing.T, name string, h http.Header) {
	t.Helper()
	if h.Get("Access-Control-Allow-Origin") != "*" || h.Get("Access-Control-Expose-Headers") != exposed {
		t.Errorf("%s: Allow-Origin %q, Expose-Headers %q", name, h.Get("Access-Control-Allow-Origin"), h.Get("Access-Control-Expose-Headers"))
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("%s: credentials allowed", name)
	}
}

func varies(h http.Header, name string) bool {
	for _, v := range h.Values("Vary") {
		for f := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), name) {
				return true
			}
		}
	}
	return false
}

func TestAPreflightIsAnsweredWithoutAKeyOnEveryPath(t *testing.T) {
	srv := testkit.StartServer(t)
	for _, path := range []string{"/ruleset", "/evaluate", "/stream"} {
		a := send(t, srv, http.MethodOptions, sdk.BaseURL+path, nil,
			"Origin", "https://app.example.com", "Access-Control-Request-Method", "POST",
			"Access-Control-Request-Headers", "authorization, content-type")
		if a.status != http.StatusNoContent {
			t.Errorf("OPTIONS %s = %d: %s", path, a.status, a.body)
			continue
		}
		browserHeaders(t, "OPTIONS "+path, a.header)
		for name, want := range map[string]string{
			"Access-Control-Allow-Methods": "GET, POST, OPTIONS",
			"Access-Control-Allow-Headers": "Authorization, Content-Type, Content-Encoding, Flagnetic-Schema-Version, Last-Event-ID",
			"Access-Control-Max-Age":       "86400",
		} {
			if got := a.header.Get(name); got != want {
				t.Errorf("OPTIONS %s: %s = %q, want %q", path, name, got, want)
			}
		}
	}
	a := send(t, srv, http.MethodOptions, sdk.BaseURL+"/unknown", nil, "Origin", "https://app.example.com")
	refused(t, "OPTIONS on a path not served", a.status, a.body, http.StatusNotFound, "not_found")
}

func TestABrowserCanReadEveryAnswerProblemsIncluded(t *testing.T) {
	srv := testkit.StartServer(t, limits(func(l *config.RateLimits) { l.ServerRuleset = 1 }))
	p := newProject(t, srv)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)

	ok := send(t, srv, http.MethodGet, sdk.BaseURL+"/ruleset", nil, "Authorization", "Bearer "+server)
	if ok.status != http.StatusOK {
		t.Fatalf("ruleset = %d", ok.status)
	}
	browserHeaders(t, "200", ok.header)
	limited := send(t, srv, http.MethodGet, sdk.BaseURL+"/ruleset", nil, "Authorization", "Bearer "+server)
	refused(t, "429", limited.status, limited.body, http.StatusTooManyRequests, "rate_limited")
	browserHeaders(t, "429", limited.header)
	for name, a := range map[string]answer{
		"401": send(t, srv, http.MethodGet, sdk.BaseURL+"/ruleset", nil),
		"404": send(t, srv, http.MethodGet, sdk.BaseURL+"/unknown", nil),
	} {
		browserHeaders(t, name, a.header)
	}
}

func TestAListedOriginIsEchoedAndAnUnlistedOneRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, client := p.clientKeyWithOrigins(t, "https://app.example.com")
	body := []byte(`{"context":{}}`)
	auth := []string{"Authorization", "Bearer " + client, "Content-Type", "application/json"}

	a := send(t, srv, http.MethodPost, sdk.BaseURL+"/evaluate", body, append(auth, "Origin", "https://app.example.com")...)
	if a.status != http.StatusOK || a.header.Get("Access-Control-Allow-Origin") != "https://app.example.com" || !varies(a.header, "Origin") {
		t.Errorf("listed origin: %d, Allow-Origin %q, Vary %v", a.status, a.header.Get("Access-Control-Allow-Origin"), a.header.Values("Vary"))
	}
	if a.header.Get("Access-Control-Expose-Headers") != exposed {
		t.Errorf("listed origin: Expose-Headers %q", a.header.Get("Access-Control-Expose-Headers"))
	}
	a = send(t, srv, http.MethodPost, sdk.BaseURL+"/evaluate", []byte("not even JSON"), append(auth, "Origin", "https://elsewhere.example")...)
	refused(t, "unlisted origin", a.status, a.body, http.StatusForbidden, "origin_not_allowed")
	a = send(t, srv, http.MethodPost, sdk.BaseURL+"/evaluate", body, auth...)
	if a.status != http.StatusOK || a.header.Get("Access-Control-Allow-Origin") != "" || !varies(a.header, "Origin") {
		t.Errorf("no origin: %d, Allow-Origin %q, Vary %v", a.status, a.header.Get("Access-Control-Allow-Origin"), a.header.Values("Vary"))
	}
}

func TestAClientKeyListingNoOriginAllowsEveryOrigin(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	for _, origin := range []string{"https://app.example.com", "https://elsewhere.example", "null"} {
		a := send(t, srv, http.MethodPost, sdk.BaseURL+"/evaluate", []byte(`{"context":{}}`),
			"Authorization", "Bearer "+client, "Content-Type", "application/json", "Origin", origin)
		if a.status != http.StatusOK {
			t.Errorf("from %s: %d %s", origin, a.status, a.body)
		}
		browserHeaders(t, origin, a.header)
		if varies(a.header, "Origin") {
			t.Errorf("from %s: Vary %v, want no Origin", origin, a.header.Values("Vary"))
		}
	}
}

func TestAServerKeyFromABrowserIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	for _, a := range []answer{
		send(t, srv, http.MethodGet, sdk.BaseURL+"/ruleset", nil, "Authorization", "Bearer "+server, "Origin", "https://app.example.com"),
		send(t, srv, http.MethodPost, sdk.BaseURL+"/evaluate", []byte(`{"context":{}}`),
			"Authorization", "Bearer "+server, "Content-Type", "application/json", "Origin", "null"),
		send(t, srv, http.MethodGet, sdk.BaseURL+"/stream", nil, "Authorization", "Bearer "+server, "Origin", "https://app.example.com"),
	} {
		refused(t, "server key from a browser", a.status, a.body, http.StatusForbidden, "server_key_from_browser")
	}
}

func TestTheStreamFollowsTheOriginsOfAKeyInItsQuery(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, restricted := p.clientKeyWithOrigins(t, "https://app.example.com")
	_, open := p.sdkKey(t, "production", management.SdkKeyKindClient)

	s := openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &restricted}, header("Origin", "https://app.example.com"))
	if h := s.res.Header; s.res.StatusCode != http.StatusOK || h.Get("Access-Control-Allow-Origin") != "https://app.example.com" || !varies(h, "Origin") {
		t.Errorf("listed origin: %d, Allow-Origin %q, Vary %v", s.res.StatusCode, h.Get("Access-Control-Allow-Origin"), h.Values("Vary"))
	}
	s = openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &open}, header("Origin", "https://elsewhere.example"))
	if s.res.StatusCode != http.StatusOK {
		t.Errorf("a key listing no origin: %d", s.res.StatusCode)
	}
	browserHeaders(t, "a key listing no origin", s.res.Header)
	s = openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &restricted}, header("Origin", "https://elsewhere.example"))
	var body []byte
	if s.res.StatusCode != http.StatusOK {
		body, _ = io.ReadAll(s.res.Body)
	}
	refused(t, "unlisted origin", s.res.StatusCode, body, http.StatusForbidden, "origin_not_allowed")
}

func TestAnOriginChangedThroughOneReplicaAppliesOnAnotherWithinASecond(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	p := newProject(t, a)
	k, client := p.clientKeyWithOrigins(t, "https://app.example.com")
	from := func(origin string) int {
		return send(t, b, http.MethodPost, sdk.BaseURL+"/evaluate", []byte(`{"context":{}}`),
			"Authorization", "Bearer "+client, "Content-Type", "application/json", "Origin", origin).status
	}
	if got := from("https://new.example.com"); got != http.StatusForbidden {
		t.Fatalf("before the change: %d, want 403", got)
	}
	if got := from("https://app.example.com"); got != http.StatusOK {
		t.Fatalf("before the change, the listed origin: %d", got)
	}
	update := func(origins ...string) {
		t.Helper()
		res := do(p.admin.UpdateSdkKeyWithResponse(t.Context(), p.key, "production", k.Id.String(), management.SdkKeyPatch{AllowedOrigins: origins}))
		if res.JSON200 == nil {
			t.Fatalf("PATCH …/sdk-keys = %d: %s", res.StatusCode(), res.Body)
		}
	}
	update("https://new.example.com")
	if !eventually(time.Second, func() bool { return from("https://new.example.com") == http.StatusOK }) {
		t.Error("an origin added on one replica is still refused by the other after a second")
	}
	if got := from("https://app.example.com"); got != http.StatusForbidden {
		t.Errorf("an origin removed on one replica: %d on the other, want 403", got)
	}
}
