package sdk_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestAServerKeyDownloadsItsEnvironmentsRulesetWithItsVersionAsETag(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	_, value := p.sdkKey(t, "staging", management.SdkKeyKindServer)

	res := ruleset(t, srv, value)
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("GET /ruleset = %d: %s", res.StatusCode(), res.Body)
	}
	doc := decode(t, res.Body)
	if doc.Scope != "server" || doc.Environment.Key != "staging" || len(doc.Flags) != 1 || doc.Flags[0].Key != "checkout.new-flow" {
		t.Errorf("ruleset = %s, want staging's server ruleset with its flag", res.Body)
	}
	h := res.HTTPResponse.Header
	if want := `"` + strconv.FormatInt(doc.Version, 10) + `"`; doc.Version <= 0 || h.Get("ETag") != want {
		t.Errorf("ETag = %q, want the version %d quoted", h.Get("ETag"), doc.Version)
	}
	if h.Get("Cache-Control") != "private, no-cache" || h.Get("Vary") != "Authorization, Accept-Encoding" ||
		h.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v, want private, no-cache varying by key and coding", h)
	}
}

func TestIfNoneMatchNamingTheCurrentVersionIsAnswered304ComparedWeakly(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	etag := ruleset(t, srv, value).HTTPResponse.Header.Get("ETag")

	for _, inm := range []string{etag, "W/" + etag, `"0", ` + etag, `W/"0",W/` + etag, "*"} {
		res := ruleset(t, srv, value, header("If-None-Match", inm))
		h := res.HTTPResponse.Header
		if res.StatusCode() != http.StatusNotModified || len(res.Body) != 0 || h.Get("ETag") != etag ||
			h.Get("Cache-Control") != "private, no-cache" || h.Get("Vary") != "Authorization, Accept-Encoding" {
			t.Errorf("If-None-Match %s: %d %v %s, want 304 with the ETag and no body", inm, res.StatusCode(), h, res.Body)
		}
	}
	for _, inm := range []string{`"0"`, `"0", "` + etag[1:len(etag)-1] + `0"`, etag[1 : len(etag)-1]} {
		if res := ruleset(t, srv, value, header("If-None-Match", inm)); res.StatusCode() != http.StatusOK {
			t.Errorf("If-None-Match %s: %d, want 200 with the ruleset", inm, res.StatusCode())
		}
	}
}

func TestTheRulesetIsGzippedWhenTheRequestAcceptsIt(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "search.ranking")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	plain := ruleset(t, srv, value)

	res := do(client(t, srv).GetRuleset(t.Context(), &sdk.GetRulesetParams{}, bearer(value), header("Accept-Encoding", "br;q=1, gzip;q=0.5")))
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("Vary") != "Authorization, Accept-Encoding" {
		t.Fatalf("headers = %v, want gzip varying by coding", res.Header)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, plain.Body) {
		t.Errorf("decompressed = %s, want %s", body, plain.Body)
	}
	refusedGzip := ruleset(t, srv, value, header("Accept-Encoding", "gzip;q=0"))
	if refusedGzip.HTTPResponse.Header.Get("Content-Encoding") != "" {
		t.Error("gzip with quality 0 was answered in gzip")
	}
	refusedByName := ruleset(t, srv, value, header("Accept-Encoding", "*;q=1, gzip;q=0"))
	if refusedByName.HTTPResponse.Header.Get("Content-Encoding") != "" {
		t.Error("gzip refused by name was answered in gzip because * accepts any coding")
	}
}

func TestAClientKeyIsRefusedAtTheRuleset(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
	res := ruleset(t, srv, value)
	refused(t, "client key", res.StatusCode(), res.Body, http.StatusForbidden, "key_kind_not_allowed")
}

func TestKeysThatDoNotWorkAreRefused401WithTheirChallenge(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	revoked, revokedValue := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.revoke(t, "production", revoked)
	expired, expiredValue := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.rotate(t, "production", expired, 0)

	invalid := `Bearer error="invalid_token", error_description="sdk_key_invalid"`
	for _, tc := range []struct {
		name      string
		editors   []sdk.RequestEditorFn
		code      string
		challenge string
	}{
		{"no key", nil, "sdk_key_missing", "Bearer"},
		{"another scheme", []sdk.RequestEditorFn{header("Authorization", "Basic dXNlcjpwYXNz")}, "sdk_key_missing", "Bearer"},
		{"malformed", []sdk.RequestEditorFn{bearer("fn_srv_short")}, "sdk_key_invalid", invalid},
		{"unknown", []sdk.RequestEditorFn{bearer("fn_srv_" + "0123456789012345678901234567890123456789")}, "sdk_key_invalid", invalid},
		{"revoked", []sdk.RequestEditorFn{bearer(revokedValue)}, "sdk_key_revoked",
			`Bearer error="invalid_token", error_description="sdk_key_revoked"`},
		{"expired", []sdk.RequestEditorFn{bearer(expiredValue)}, "sdk_key_expired",
			`Bearer error="invalid_token", error_description="sdk_key_expired"`},
	} {
		res := do(client(t, srv).GetRulesetWithResponse(t.Context(), &sdk.GetRulesetParams{}, tc.editors...))
		refused(t, tc.name, res.StatusCode(), res.Body, http.StatusUnauthorized, tc.code)
		if got := res.HTTPResponse.Header.Get("WWW-Authenticate"); got != tc.challenge {
			t.Errorf("%s: WWW-Authenticate = %q, want %q", tc.name, got, tc.challenge)
		}
	}
}

func TestTheClockDecidesWhenARotatedKeyStopsWorking(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	k, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.rotate(t, "production", k, 3600)

	if res := ruleset(t, srv, value); res.StatusCode() != http.StatusOK {
		t.Fatalf("within the overlap: %d: %s", res.StatusCode(), res.Body)
	}
	c.Advance(3600 * 1e9)
	res := ruleset(t, srv, value)
	refused(t, "at the end of the overlap", res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_expired")
}

func TestTheSchemaVersionAskedForIsServedFromOneUpAndRefusedBelow(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)

	for _, v := range []string{"1", "2", "99", "123456789012345678901234567890"} {
		if res := ruleset(t, srv, value, header("Flagnetic-Schema-Version", v)); res.StatusCode() != http.StatusOK {
			t.Errorf("schema %s: %d %s, want 200", v, res.StatusCode(), res.Body)
		}
	}
	for _, v := range []string{"0", "-3", "one", "1.5", ""} {
		res := ruleset(t, srv, value, header("Flagnetic-Schema-Version", v))
		refused(t, "schema "+v, res.StatusCode(), res.Body, http.StatusNotAcceptable, "schema_version_unsupported")
	}
	twice := ruleset(t, srv, value, header("Flagnetic-Schema-Version", "1"), header("Flagnetic-Schema-Version", "2"))
	refused(t, "two schema versions", twice.StatusCode(), twice.Body, http.StatusNotAcceptable, "schema_version_unsupported")
}

func TestPathsAndMethodsTheSDKAPIDoesNotServeAreProblems(t *testing.T) {
	srv := testkit.StartServer(t)
	for _, tc := range []struct {
		method, path string
		status       int
		code, allow  string
	}{
		{http.MethodGet, "/sdk/v1/rulesets", http.StatusNotFound, "not_found", ""},
		{http.MethodGet, "/sdk/v2/ruleset", http.StatusNotFound, "not_found", ""},
		{http.MethodDelete, "/sdk/v1/ruleset", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD, OPTIONS"},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		refused(t, tc.method+" "+tc.path, res.StatusCode, body, tc.status, tc.code)
		if res.Header.Get("Content-Type") != "application/problem+json" || res.Header.Get("Allow") != tc.allow {
			t.Errorf("%s %s: headers %v", tc.method, tc.path, res.Header)
		}
	}
}

func TestAKeyThatCannotBeLookedUpIsAnswered503(t *testing.T) {
	unreachable, err := pgxpool.New(t.Context(), "postgres://flagnetic@127.0.0.1:1/flagnetic?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unreachable.Close)
	srv := testkit.StartServer(t, testkit.WithPool(unreachable))
	res := ruleset(t, srv, "fn_srv_"+"0123456789012345678901234567890123456789")
	refused(t, "lookup failed", res.StatusCode(), res.Body, http.StatusServiceUnavailable, "unavailable")
	if got := res.HTTPResponse.Header.Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
}

func TestAKeysUseIsRecordedAtMostOnceAMinute(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	k, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	lastUsed := func() string {
		t.Helper()
		if err := srv.Tick(t, "sdk_key_last_use"); err != nil {
			t.Fatal(err)
		}
		res := do(p.admin.ListSdkKeysWithResponse(t.Context(), p.key, "production"))
		for _, listed := range res.JSON200.Items {
			if listed.Id == k.Id && listed.LastUsedAt != nil {
				return listed.LastUsedAt.UTC().Format("15:04:05")
			}
		}
		return "never"
	}

	if got := lastUsed(); got != "never" {
		t.Fatalf("before any use: %s", got)
	}
	ruleset(t, srv, value)
	if got := lastUsed(); got != "09:00:00" {
		t.Errorf("after the first use: %s, want 09:00:00", got)
	}
	c.Advance(30e9)
	ruleset(t, srv, value)
	if got := lastUsed(); got != "09:00:00" {
		t.Errorf("after a use 30s later: %s, want still 09:00:00", got)
	}
	c.Advance(30e9)
	ruleset(t, srv, value)
	if got := lastUsed(); got != "09:01:00" {
		t.Errorf("after a use a minute later: %s, want 09:01:00", got)
	}
}

func TestARequestBodyMayBeGzipAndItsLimitCountsItDecompressed(t *testing.T) {
	const limit = 64
	srv := testkit.StartServer(t, testkit.WithRoute("POST /sdk/v1/echo", func(edge *flaghttp.Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				edge.WriteError(w, r, err)
				return
			}
			_, _ = w.Write(body)
		})
	}, flaghttp.MaxBodyBytes(limit)))
	post := func(coding string, body []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/sdk/v1/echo", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Encoding", coding)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		got, _ := io.ReadAll(res.Body)
		return res.StatusCode, got
	}
	gzipped := func(b []byte) []byte {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(b)
		_ = zw.Close()
		return buf.Bytes()
	}

	if status, got := post("gzip", gzipped([]byte(`{"hello":"world"}`))); status != http.StatusOK || string(got) != `{"hello":"world"}` {
		t.Errorf("gzip body: %d %s, want it decompressed", status, got)
	}
	large := bytes.Repeat([]byte("a"), 10*limit)
	if small := gzipped(large); len(small) >= limit {
		t.Fatalf("the compressed body is %d bytes, not under the limit", len(small))
	}
	status, got := post("gzip", gzipped(large))
	refused(t, "over the limit once decompressed", status, got, http.StatusRequestEntityTooLarge, "request_too_large")
	status, got = post("gzip", []byte("not gzip at all"))
	refused(t, "not gzip", status, got, http.StatusBadRequest, "invalid_request")
	status, got = post("br", []byte("x"))
	refused(t, "another coding", status, got, http.StatusUnsupportedMediaType, "unsupported_media_type")
}
