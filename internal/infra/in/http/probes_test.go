package http_test

import (
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := nethttp.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type = %q, want application/json", url, ct)
	}
	return resp.StatusCode, string(body)
}

func TestProbesWithPostgreSQLReachable(t *testing.T) {
	srv := testkit.StartServer(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		if code, body := get(t, srv.URL+path); code != 200 || body != `{"status":"ok"}`+"\n" {
			t.Errorf("GET %s = %d %q, want 200 {\"status\":\"ok\"}", path, code, body)
		}
	}
}

func TestReadinessFailsButHealthPassesWhenPostgreSQLIsUnreachable(t *testing.T) {
	unreachable, err := pgxpool.New(t.Context(), "postgres://flagnetic@127.0.0.1:1/flagnetic?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unreachable.Close)
	srv := testkit.StartServer(t, testkit.WithPool(unreachable))

	if code, body := get(t, srv.URL+"/readyz"); code != 503 || body != `{"status":"unavailable"}`+"\n" {
		t.Errorf("GET /readyz = %d %q, want 503 {\"status\":\"unavailable\"}", code, body)
	}
	if code, _ := get(t, srv.URL+"/healthz"); code != 200 {
		t.Errorf("GET /healthz = %d, want 200 while the process runs", code)
	}
	warned := false
	for _, r := range srv.Logs.Find("not ready") {
		warned = warned || (r.Level == slog.LevelWarn && r.Attrs["error"] != nil)
	}
	if !warned {
		t.Error("the failed readiness check was not logged at warn with its error")
	}
}
