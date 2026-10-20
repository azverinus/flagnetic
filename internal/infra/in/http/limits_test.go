package http_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// whoami answers the client the edge resolved for the request.
var whoami = testkit.WithRoute("GET /test/whoami", func(*flaghttp.Server) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		c := flaghttp.ClientOf(r.Context())
		_, _ = fmt.Fprintf(w, "%s %s", c.IP, c.Scheme)
	})
})

func trusting(prefixes ...string) testkit.ServerOption {
	return testkit.WithEdge(func(o *flaghttp.Options) {
		for _, p := range prefixes {
			o.TrustedProxies = append(o.TrustedProxies, netip.MustParsePrefix(p))
		}
	})
}

// clientIP returns the client_ip the access log recorded for the request
// with the given forwarding headers.
func clientIP(t *testing.T, srv *testkit.Server, header map[string]string) string {
	t.Helper()
	id := request(t, "GET", srv.URL+"/test/whoami", header).header.Get("X-Request-ID")
	for _, r := range srv.Logs.Find("request") {
		if r.Attrs["request_id"] == id {
			return fmt.Sprint(r.Attrs["client_ip"])
		}
	}
	t.Fatalf("no access log record for request %s", id)
	return ""
}

func TestForwardedForFromAnUntrustedPeerIsIgnored(t *testing.T) {
	srv := testkit.StartServer(t, whoami, trusting("10.0.0.0/8"))
	if got := clientIP(t, srv, map[string]string{"X-Forwarded-For": "203.0.113.7"}); got != "127.0.0.1" {
		t.Errorf("client_ip = %s, want the TCP peer 127.0.0.1", got)
	}
}

func TestForwardedForFromATrustedProxyNamesTheRightmostUntrustedAddress(t *testing.T) {
	srv := testkit.StartServer(t, whoami, trusting("127.0.0.1/32", "10.0.0.0/8"))
	for xff, want := range map[string]string{
		"203.0.113.7, 198.51.100.9, 10.0.0.5": "198.51.100.9",
		"::ffff:198.51.100.9":                 "198.51.100.9",
		"10.0.0.7, 10.0.0.5":                  "10.0.0.7",
		"":                                    "127.0.0.1",
	} {
		if got := clientIP(t, srv, map[string]string{"X-Forwarded-For": xff}); got != want {
			t.Errorf("X-Forwarded-For %q: client_ip = %s, want %s", xff, got, want)
		}
	}
}

func TestAnUnparsableForwardedEntryIsSkipped(t *testing.T) {
	srv := testkit.StartServer(t, whoami, trusting("127.0.0.1/32", "10.0.0.0/8"))
	a := clientIP(t, srv, map[string]string{"X-Forwarded-For": "198.51.100.9, unknown, 10.0.0.5"})
	b := clientIP(t, srv, map[string]string{"X-Forwarded-For": "198.51.100.10, unknown, 10.0.0.5"})
	if a != "198.51.100.9" || b != "198.51.100.10" {
		t.Errorf("clients behind a hop writing %q are %s and %s, want them distinct and themselves", "unknown", a, b)
	}
}

func TestForwardedProtoIsBelievedOnlyFromATrustedPeer(t *testing.T) {
	header := map[string]string{"X-Forwarded-Proto": "http, https"}
	whoamiBody := func(srv *testkit.Server) string {
		resp, err := nethttp.DefaultClient.Do(mustRequest(t, srv.URL+"/test/whoami", header))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if got := whoamiBody(testkit.StartServer(t, whoami, trusting("127.0.0.1/32"))); got != "127.0.0.1 https" {
		t.Errorf("from a trusted peer: %q, want the rightmost forwarded scheme https", got)
	}
	if got := whoamiBody(testkit.StartServer(t, whoami)); got != "127.0.0.1 http" {
		t.Errorf("from an untrusted peer: %q, want the connection's own scheme http", got)
	}
}

func mustRequest(t *testing.T, url string, header map[string]string) *nethttp.Request {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return req
}

func TestAnIdleConnectionIsClosed(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithEdge(func(o *flaghttp.Options) { o.IdleTimeout = 200 * time.Millisecond }))
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := nethttp.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	if _, err := br.ReadByte(); err != io.EOF { //nolint:errorlint // a closed connection reads io.EOF itself
		t.Fatalf("read on the idle connection = %v, want EOF", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the idle connection was closed after %v, want about 200ms", took)
	}
}

// slowBody sends the headers of a POST and half its body, then stalls, and
// returns the status the server answers with, or 0 when it answered nothing
// within wait.
func slowBody(t *testing.T, srv *testkit.Server, path string, wait time.Duration) int {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "POST "+path+" HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\nhello")
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	resp, err := nethttp.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func readBody(srv *flaghttp.Server) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			srv.WriteError(w, r, err)
			return
		}
		w.WriteHeader(nethttp.StatusNoContent)
	})
}

func TestASlowBodyIsCutOffExceptOnAStreamRoute(t *testing.T) {
	srv := testkit.StartServer(t,
		testkit.WithEdge(func(o *flaghttp.Options) { o.BodyReadTimeout = 200 * time.Millisecond }),
		testkit.WithRoute("POST /test/upload", readBody),
		testkit.WithRoute("POST /test/stream", readBody, flaghttp.Stream()),
	)
	if got := slowBody(t, srv, "/test/upload", 5*time.Second); got != nethttp.StatusRequestTimeout {
		t.Errorf("a stalled body on an ordinary route answered %d, want 408 once the body read timeout passed", got)
	}
	if got := slowBody(t, srv, "/test/stream", time.Second); got != 0 {
		t.Errorf("a stalled body on a stream route answered %d, want it still waiting", got)
	}
}

func TestAnOversizedBodyIsRefused(t *testing.T) {
	srv := testkit.StartServer(t,
		testkit.WithEdge(func(o *flaghttp.Options) { o.MaxBodyBytes = 16 }),
		testkit.WithRoute("POST /test/upload", readBody),
		testkit.WithRoute("POST /test/import", readBody, flaghttp.MaxBodyBytes(64)),
	)
	post := func(path string, n int, chunked bool) problem {
		t.Helper()
		var body io.Reader = strings.NewReader(strings.Repeat("x", n))
		if chunked {
			body = io.MultiReader(body) // hides the length, so the body is sent chunked
		}
		req, err := nethttp.NewRequestWithContext(t.Context(), "POST", srv.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := nethttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		p := problem{header: resp.Header, status: resp.StatusCode}
		if resp.StatusCode != nethttp.StatusNoContent {
			raw, _ := io.ReadAll(resp.Body)
			p.body = map[string]any{"raw": string(raw)}
		}
		return p
	}
	for _, chunked := range []bool{false, true} {
		if p := post("/test/upload", 17, chunked); p.status != 413 || p.header.Get("Content-Type") != "application/problem+json" ||
			!strings.Contains(fmt.Sprint(p.body["raw"]), `"code":"request_too_large"`) {
			t.Errorf("17 bytes (chunked %v) to a 16-byte route = %d %v, want a 413 problem", chunked, p.status, p.body)
		}
		if p := post("/test/upload", 16, chunked); p.status != 204 {
			t.Errorf("16 bytes (chunked %v) to a 16-byte route = %d, want it accepted", chunked, p.status)
		}
		if p := post("/test/import", 64, chunked); p.status != 204 {
			t.Errorf("64 bytes (chunked %v) to a route declaring 64 = %d, want it accepted", chunked, p.status)
		}
	}
}

func TestTheBodyReadTimeoutBoundsOnlyTheBody(t *testing.T) {
	cancelled := make(chan bool, 2)
	slowHandler := func(read bool) func(*flaghttp.Server) nethttp.Handler {
		return func(*flaghttp.Server) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				if read {
					// Exactly the declared length and no further, as a JSON
					// decoder reads: the end of the body is never seen.
					_, _ = io.ReadFull(r.Body, make([]byte, r.ContentLength))
				}
				select {
				case <-r.Context().Done():
					cancelled <- true
				case <-time.After(500 * time.Millisecond):
					cancelled <- false
				}
				w.WriteHeader(nethttp.StatusNoContent)
			})
		}
	}
	srv := testkit.StartServer(t,
		testkit.WithEdge(func(o *flaghttp.Options) { o.BodyReadTimeout = 100 * time.Millisecond }),
		testkit.WithRoute("POST /test/work", slowHandler(true)),
		testkit.WithRoute("GET /test/work", slowHandler(false)),
	)
	for _, method := range []string{"POST", "GET"} {
		var body io.Reader
		if method == "POST" {
			body = strings.NewReader("payload")
		}
		req, _ := nethttp.NewRequestWithContext(t.Context(), method, srv.URL+"/test/work", body)
		resp, err := nethttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if <-cancelled {
			t.Errorf("%s: a handler running past the body read timeout, its body long read, was cancelled", method)
		}
	}
}

func TestForwardedProtoIsTheLastLineFromTheNearestProxy(t *testing.T) {
	srv := testkit.StartServer(t, whoami, trusting("127.0.0.1/32"))
	req := mustRequest(t, srv.URL+"/test/whoami", nil)
	req.Header.Add("X-Forwarded-Proto", "https")
	req.Header.Add("X-Forwarded-Proto", "http")
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "127.0.0.1 http" {
		t.Errorf("got %q, want the scheme of the last line, http", b)
	}
}

func TestForwardedEntriesWithPortsAndAcrossHeaderLines(t *testing.T) {
	srv := testkit.StartServer(t, whoami, trusting("127.0.0.1/32", "10.0.0.0/8"))
	req := func(lines ...string) string {
		r := mustRequest(t, srv.URL+"/test/whoami", nil)
		for _, l := range lines {
			r.Header.Add("X-Forwarded-For", l)
		}
		resp, err := nethttp.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		id := resp.Header.Get("X-Request-ID")
		for _, rec := range srv.Logs.Find("request") {
			if rec.Attrs["request_id"] == id {
				return fmt.Sprint(rec.Attrs["client_ip"])
			}
		}
		return ""
	}
	if got := req("198.51.100.9:51234, 10.0.0.5"); got != "198.51.100.9" {
		t.Errorf("an entry with a port: client_ip = %s", got)
	}
	if got := req("198.51.100.9", "10.0.0.5"); got != "198.51.100.9" {
		t.Errorf("entries across two header lines: client_ip = %s", got)
	}
	if got := req("[2001:db8::7]:443"); got != "2001:db8::7" {
		t.Errorf("an IPv6 entry with a port: client_ip = %s", got)
	}
}
