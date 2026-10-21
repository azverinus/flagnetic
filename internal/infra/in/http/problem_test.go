package http_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// failing mounts a route answering err.
func failing(pattern string, err error) testkit.ServerOption {
	return testkit.WithRoute(pattern, func(srv *flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			srv.WriteError(w, r, err)
		})
	})
}

type problem struct {
	header nethttp.Header
	status int
	body   map[string]any
}

func request(t *testing.T, method, url string, header map[string]string) problem {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	p := problem{header: resp.Header, status: resp.StatusCode}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/") {
		if err := json.Unmarshal(raw, &p.body); err != nil {
			t.Fatalf("%s %s: body %q is not JSON: %v", method, url, raw, err)
		}
	}
	return p
}

// checkProblem asserts the members every problem document carries.
func checkProblem(t *testing.T, p problem, status int, code, title string) {
	t.Helper()
	if p.status != status {
		t.Errorf("status = %d, want %d", p.status, status)
	}
	if ct := p.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	want := map[string]any{"type": "urn:flagnetic:problem:" + code, "title": title, "status": float64(status), "code": code}
	for k, v := range want {
		if p.body[k] != v {
			t.Errorf("%s = %v, want %v", k, p.body[k], v)
		}
	}
	if id := p.header.Get("X-Request-ID"); id == "" || p.body["request_id"] != id {
		t.Errorf("request_id = %v, X-Request-ID = %q; want them equal and present", p.body["request_id"], id)
	}
}

func TestEveryKindIsAnsweredAsAProblemDocument(t *testing.T) {
	cases := []struct {
		err    *apperrors.Error
		status int
		title  string
	}{
		{apperrors.Invalid("flag_key_invalid", "the key is invalid"), 422, "Invalid"},
		{apperrors.NotFound("flag_not_found", "no flag beta"), 404, "Not found"},
		{apperrors.Conflict("flag_key_taken", "the key is taken"), 409, "Conflict"},
		{apperrors.Forbidden("permission_missing", "you may not"), 403, "Forbidden"},
		{apperrors.Unauthenticated("session_missing", "sign in"), 401, "Unauthenticated"},
		{apperrors.PreconditionFailed("version_mismatch", "changed since"), 412, "Precondition failed"},
		{apperrors.Gone("invitation_expired", "expired"), 410, "Gone"},
		{apperrors.RateLimited("too_many_requests", "slow down", time.Second), 429, "Too many requests"},
		{apperrors.Unavailable("database_unavailable", "try again"), 503, "Unavailable"},
	}
	var opts []testkit.ServerOption
	for i, c := range cases {
		opts = append(opts, failing(fmt.Sprintf("GET /test/%d", i), fmt.Errorf("wrapped: %w", c.err)))
	}
	srv := testkit.StartServer(t, opts...)
	for i, c := range cases {
		t.Run(c.err.Kind.String(), func(t *testing.T) {
			p := request(t, "GET", fmt.Sprintf("%s/test/%d", srv.URL, i), nil)
			checkProblem(t, p, c.status, c.err.Code, c.title)
			if p.body["detail"] != c.err.Detail {
				t.Errorf("detail = %v, want %q", p.body["detail"], c.err.Detail)
			}
			if _, ok := p.body["errors"]; ok {
				t.Error("errors is present without violations")
			}
		})
	}
}

func TestFieldViolationsAndExtensionsAreMembers(t *testing.T) {
	err := apperrors.Invalid("request_invalid", "two fields are wrong",
		apperrors.Violation{Pointer: "/variations/1/value", Code: "type", Detail: "must be a boolean"},
		apperrors.Violation{Pointer: "/password", Code: "weak_password", Detail: "too short", Params: map[string]any{"rule": "length", "min": 12}},
	).WithExtension("references", []string{"segment beta"}).WithExtension("status", "shadowed")
	srv := testkit.StartServer(t, failing("POST /test/flags", err))

	p := request(t, "POST", srv.URL+"/test/flags", nil)
	checkProblem(t, p, 422, "request_invalid", "Invalid")
	violations, _ := p.body["errors"].([]any)
	if len(violations) != 2 {
		t.Fatalf("errors = %v", p.body["errors"])
	}
	if v := violations[0].(map[string]any); v["pointer"] != "/variations/1/value" || v["code"] != "type" || v["detail"] != "must be a boolean" {
		t.Errorf("errors[0] = %v", v)
	} else if _, ok := v["params"]; ok {
		t.Errorf("errors[0] = %v, want no params member when the violation has none", v)
	}
	if params, _ := violations[1].(map[string]any)["params"].(map[string]any); params["rule"] != "length" || params["min"] != 12.0 {
		t.Errorf("errors[1].params = %v, want the rule and its bound", params)
	}
	if refs, _ := p.body["references"].([]any); len(refs) != 1 || refs[0] != "segment beta" {
		t.Errorf("references = %v, want the extension as a top-level member", p.body["references"])
	}
}

func TestRateLimitedWritesRetryAfter(t *testing.T) {
	srv := testkit.StartServer(t, failing("GET /test/limited", apperrors.RateLimited("sign_in_locked", "too many attempts", 89500*time.Millisecond)))
	p := request(t, "GET", srv.URL+"/test/limited", nil)
	checkProblem(t, p, 429, "sign_in_locked", "Too many requests")
	if got := p.header.Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, want 90 (seconds, rounded up)", got)
	}
	if p.body["retry_after"] != 90.0 {
		t.Errorf("retry_after = %v, want 90", p.body["retry_after"])
	}
}

func TestAnUnavailableErrorWithARetryDelayWritesRetryAfter(t *testing.T) {
	srv := testkit.StartServer(t, failing("GET /test/busy", apperrors.Unavailable("busy", "no slot").WithRetryAfter(time.Second)))
	p := request(t, "GET", srv.URL+"/test/busy", nil)
	checkProblem(t, p, 503, "busy", "Unavailable")
	if got := p.header.Get("Retry-After"); got != "1" || p.body["retry_after"] != 1.0 {
		t.Errorf("Retry-After = %q, retry_after = %v; want 1", got, p.body["retry_after"])
	}
}

func TestAnErrorWithoutAKindIsInternalAndLogged(t *testing.T) {
	srv := testkit.StartServer(t, failing("GET /test/broken", fmt.Errorf("query flags: %w", errors.New("connection reset"))))
	p := request(t, "GET", srv.URL+"/test/broken", nil)
	checkProblem(t, p, 500, "internal", "Internal error")
	if detail, ok := p.body["detail"]; ok {
		t.Errorf("detail = %v; an internal error must not tell the client anything", detail)
	}
	logged := srv.Logs.Find("request failed")
	if len(logged) != 1 || logged[0].Level != slog.LevelError ||
		!strings.Contains(fmt.Sprint(logged[0].Attrs["error"]), "connection reset") ||
		logged[0].Attrs["request_id"] != p.body["request_id"] {
		t.Errorf("logged %v, want one error record with the cause and the request ID", logged)
	}
}

func TestAdapterOnlyProblems(t *testing.T) {
	srv := testkit.StartServer(t,
		failing("POST /test/decode", flaghttp.InvalidRequest("the body is not JSON")),
		failing("PUT /test/write", flaghttp.PreconditionRequired("send If-Match with the version you read")),
	)
	p := request(t, "POST", srv.URL+"/test/decode", nil)
	checkProblem(t, p, 400, "invalid_request", "Invalid request")
	if p.body["detail"] != "the body is not JSON" {
		t.Errorf("detail = %v", p.body["detail"])
	}
	checkProblem(t, request(t, "PUT", srv.URL+"/test/write", nil), 428, "precondition_required", "Precondition required")
}

func TestUnmatchedRoutesAreProblems(t *testing.T) {
	srv := testkit.StartServer(t)
	checkProblem(t, request(t, "GET", srv.URL+"/nope", nil), 404, "not_found", "Not found")
	p := request(t, "DELETE", srv.URL+"/healthz", nil)
	checkProblem(t, p, 405, "method_not_allowed", "Method not allowed")
	if allow := p.header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want the methods the path has", allow)
	}
}

func TestAPanicIsAnsweredInternalAndLoggedWithItsStack(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/panic", func(*flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { panic("poison row") })
	}))
	p := request(t, "GET", srv.URL+"/test/panic", nil)
	checkProblem(t, p, 500, "internal", "Internal error")
	logged := srv.Logs.Find("panic serving a request")
	if len(logged) != 1 || logged[0].Attrs["request_id"] != p.body["request_id"] ||
		logged[0].Attrs["panic"] != "poison row" || !strings.Contains(fmt.Sprint(logged[0].Attrs["stack"]), "problem_test.go") {
		t.Errorf("logged %v, want the panic with its stack and the request ID", logged)
	}
	// The server keeps serving.
	checkProblem(t, request(t, "GET", srv.URL+"/test/panic", nil), 500, "internal", "Internal error")
}

func TestAnAbortedHandlerIsNotAnsweredInternal(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/abort", func(*flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { panic(nethttp.ErrAbortHandler) })
	}))
	resp, err := nethttp.Get(srv.URL + "/test/abort")
	if err == nil {
		_ = resp.Body.Close()
		t.Errorf("an aborted handler was answered %d, want the connection aborted", resp.StatusCode)
	}
	if len(srv.Logs.Find("panic serving a request")) != 0 {
		t.Error("an aborted handler was logged as a panic")
	}
}

var wellFormedID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func TestEveryResponseCarriesARequestID(t *testing.T) {
	srv := testkit.StartServer(t)
	seen := map[string]bool{}
	for _, path := range []string{"/healthz", "/readyz", "/nope", "/healthz"} {
		id := request(t, "GET", srv.URL+path, nil).header.Get("X-Request-ID")
		if !wellFormedID.MatchString(id) || seen[id] {
			t.Errorf("GET %s: X-Request-ID = %q, want a fresh well-formed ID", path, id)
		}
		seen[id] = true
	}
}

func TestIncomingRequestIDIsEchoedWhenWellFormed(t *testing.T) {
	srv := testkit.StartServer(t)
	const id = "lb-7f3a:42.retry_1"
	if got := request(t, "GET", srv.URL+"/nope", map[string]string{"X-Request-ID": id}); got.header.Get("X-Request-ID") != id || got.body["request_id"] != id {
		t.Errorf("X-Request-ID = %q, request_id = %v; want the incoming %q", got.header.Get("X-Request-ID"), got.body["request_id"], id)
	}
	for _, bad := range []string{"has space", "<script>", strings.Repeat("a", 129)} {
		got := request(t, "GET", srv.URL+"/healthz", map[string]string{"X-Request-ID": bad}).header.Get("X-Request-ID")
		if got == bad || !wellFormedID.MatchString(got) {
			t.Errorf("incoming %q: X-Request-ID = %q, want a minted one", bad, got)
		}
	}
}

func TestEachRequestIsAccessLogged(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/flags/{key}", func(*flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) { _, _ = io.WriteString(w, "hello") })
	}))
	id := request(t, "GET", srv.URL+"/test/flags/beta", nil).header.Get("X-Request-ID")
	request(t, "GET", srv.URL+"/no/such/route", nil)
	request(t, "GET", srv.URL+"/healthz", nil)
	request(t, "GET", srv.URL+"/readyz", nil)

	records := srv.Logs.Find("request")
	if len(records) != 2 {
		t.Fatalf("%d access log records, want 2 (probes are not logged): %v", len(records), records)
	}
	r := records[0].Attrs
	want := map[string]any{"request_id": id, "method": "GET", "route": "GET /test/flags/{key}", "path": "/test/flags/beta", "status": int64(200), "bytes": int64(5)}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, r[k], r[k], v)
		}
	}
	if d, ok := r["duration"].(time.Duration); !ok || d <= 0 {
		t.Errorf("duration = %v", r["duration"])
	}
	if records[0].Level != slog.LevelInfo {
		t.Errorf("level = %v", records[0].Level)
	}
	if u := records[1].Attrs; u["route"] != "unmatched" || u["status"] != int64(404) {
		t.Errorf("unmatched request logged as %v", u)
	}
}

func TestLinesLoggedWhileServingCarryTheRequest(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/work", func(s *flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			s.Logger().InfoContext(r.Context(), "working")
		})
	}))
	id := request(t, "GET", srv.URL+"/test/work", nil).header.Get("X-Request-ID")
	logged := srv.Logs.Find("working")
	if len(logged) != 1 || logged[0].Attrs["request_id"] != id || logged[0].Attrs["route"] != "GET /test/work" {
		t.Errorf("logged %v, want request_id %s and the route", logged, id)
	}
}

func TestAPanicAfterTheResponseStartedAbortsTheConnection(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/half", func(*flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			_, _ = io.WriteString(w, "first half")
			w.(nethttp.Flusher).Flush()
			panic("second half failed")
		})
	}))
	resp, err := nethttp.Get(srv.URL + "/test/half")
	if err != nil {
		return // aborted before the headers arrived
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("a response cut short by a panic was read as complete")
	}
}

func TestRequestAttributesStayAtTheTopLevelOfGroupedLogs(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/grouped", func(s *flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			s.Logger().With("component", "cache").WithGroup("cache").InfoContext(r.Context(), "grouped", "hits", 3)
		})
	}))
	id := request(t, "GET", srv.URL+"/test/grouped", nil).header.Get("X-Request-ID")
	logged := srv.Logs.Find("grouped")
	if len(logged) != 1 {
		t.Fatalf("logged %v", logged)
	}
	if a := logged[0].Attrs; a["request_id"] != id || a["route"] != "GET /test/grouped" || a["component"] != "cache" || a["cache.hits"] != int64(3) {
		t.Errorf("attrs = %v, want request_id and route at the top level and the group kept", a)
	}
}

func TestAPathThatOnlyNeedsCleaningIsRedirected(t *testing.T) {
	srv := testkit.StartServer(t)
	client := &nethttp.Client{CheckRedirect: func(*nethttp.Request, []*nethttp.Request) error { return nethttp.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/nope/../healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 || resp.Header.Get("Location") != "/healthz" {
		t.Errorf("GET /nope/../healthz = %d to %q, want a redirect to /healthz", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestASecretLoggedWhileServingIsRedacted(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithRoute("GET /test/secret", func(s *flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			s.Logger().InfoContext(r.Context(), "configured", "password", secret.New("hunter2"))
		})
	}))
	request(t, "GET", srv.URL+"/test/secret", nil)
	if logged := srv.Logs.Find("configured"); len(logged) != 1 || logged[0].Attrs["password"] != "[redacted]" {
		t.Errorf("logged %v, want the password redacted", logged)
	}
}
