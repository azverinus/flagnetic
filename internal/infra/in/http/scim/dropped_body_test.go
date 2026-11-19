package scim_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/scim"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// sendPartialBody opens a connection and posts a body declared as 500 bytes
// long of which only a few are sent, leaving the connection to the caller.
func sendPartialBody(t *testing.T, e env, token string) net.Conn {
	t.Helper()
	u, err := url.Parse(e.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "POST %s/Users HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/scim+json\r\nContent-Length: 500\r\n\r\n{\"userName\":",
		scim.BaseURL, u.Host, token); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestADirectoryThatDropsABodyIsNotAServerFailure(t *testing.T) {
	e := start(t)
	token := e.token(t, e.provider(t))
	conn := sendPartialBody(t, e, token)
	_ = conn.Close()

	const route = "/scim/v2/"
	deadline := time.Now().Add(10 * time.Second)
	var status any
	for time.Now().Before(deadline) && status == nil {
		for _, r := range e.srv.Logs.Find("request") {
			if r.Attrs["route"] == route {
				status = r.Attrs["status"]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != int64(499) {
		t.Errorf("access record status = %v, want 499", status)
	}
	if failed := e.srv.Logs.Find("SCIM request failed"); len(failed) != 0 {
		t.Errorf("a body the directory dropped was logged as a failure: %v", failed)
	}
	scrape := e.srv.Scrape(t)
	for code, want := range map[string]float64{"499": 1, "500": -1, "400": -1} {
		if got := testkit.Metric(t, scrape, `flagnetic_http_requests_total{code="`+code+`",method="POST",route="`+route+`"}`); got != want {
			t.Errorf("requests counted as %s = %v, want %v", code, got, want)
		}
	}
}

func TestABodyThatStallsIsAnsweredRequestTimeout(t *testing.T) {
	e := start(t, testkit.WithEdge(func(o *flaghttp.Options) { o.BodyReadTimeout = 300 * time.Millisecond }))
	token := e.token(t, e.provider(t))
	conn := sendPartialBody(t, e, token)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusRequestTimeout {
		b, _ := io.ReadAll(res.Body)
		t.Errorf("a stalled body was answered %d, want 408: %s %v", res.StatusCode, b, res.Header)
	}
}
