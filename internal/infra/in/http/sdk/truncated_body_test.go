package sdk_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// dropBody posts to path a body declared as declared bytes long, sends only
// sent of it, and closes the connection: a client that went away mid-upload.
func dropBody(t *testing.T, srv *testkit.Server, path, key string, headers string, sent []byte, declared int) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\n%sContent-Length: %d\r\n\r\n",
		path, u.Host, key, headers, declared); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(sent); err != nil {
		t.Fatal(err)
	}
}

func TestABodyTheClientDroppedIsNotAServerFailure(t *testing.T) {
	plain := bytes.Repeat([]byte(" "), 200)
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(bytes.Repeat([]byte(`{"events":[],"pad":"xxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`), 100))
	_ = zw.Close()

	for _, c := range []struct {
		name, headers string
		sent          []byte
	}{
		{"identity", "", plain},
		{"gzip", "Content-Encoding: gzip\r\n", zipped.Bytes()[:60]},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := testkit.StartServer(t)
			p := newProject(t, srv)
			_, key := p.sdkKey(t, "production", management.SdkKeyKindClient)
			dropBody(t, srv, sdk.BaseURL+"/events", key, c.headers, c.sent, 1000)

			const route = "POST /sdk/v1/events"
			deadline := time.Now().Add(10 * time.Second)
			var status any
			for time.Now().Before(deadline) && status == nil {
				for _, r := range srv.Logs.Find("request") {
					if r.Attrs["route"] == route {
						status = r.Attrs["status"]
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status != int64(499) {
				t.Errorf("access record status = %v, want 499", status)
			}
			if failed := srv.Logs.Find("request failed"); len(failed) != 0 {
				t.Errorf("a body the client dropped was logged as a failure: %v", failed)
			}
			for _, r := range srv.Logs.Find("request") {
				if r.Level > slog.LevelInfo {
					t.Errorf("record %v at %v", r.Attrs, r.Level)
				}
			}
			scrape := srv.Scrape(t)
			if got := testkit.Metric(t, scrape, `flagnetic_http_requests_total{code="499",method="POST",route="`+route+`"}`); got != 1 {
				t.Errorf("requests counted as 499 = %v, want 1", got)
			}
			if got := testkit.Metric(t, scrape, `flagnetic_http_requests_total{code="500",method="POST",route="`+route+`"}`); got != -1 {
				t.Errorf("requests counted as 500 = %v, want none", got)
			}
			if got := testkit.Metric(t, scrape, `flagnetic_http_requests_total{code="400",method="POST",route="`+route+`"}`); got != -1 {
				t.Errorf("requests counted as 400 = %v, want none", got)
			}
		})
	}
}
