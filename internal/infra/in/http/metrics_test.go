package http_test

import (
	"bufio"
	"context"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// metric returns the value of the sample named series, such as
// `flagnetic_http_requests_total{code="200",method="GET",route="unmatched"}`,
// or -1 when the scrape has none.
func metric(t *testing.T, scrape, series string) float64 {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(scrape))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("%s: %v", series, err)
			}
			return f
		}
	}
	return -1
}

var okRoute = testkit.WithRoute("GET /test/flags/{key}", func(*flaghttp.Server) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) { _, _ = io.WriteString(w, "ok") })
})

func TestMetricsExposeRequestCountsByRoute(t *testing.T) {
	srv := testkit.StartServer(t, okRoute)
	for _, path := range []string{"/test/flags/a", "/test/flags/b", "/no/such/route"} {
		request(t, "GET", srv.URL+path, nil)
	}
	scrape := srv.Scrape(t)
	for series, want := range map[string]float64{
		`flagnetic_http_requests_total{code="200",method="GET",route="GET /test/flags/{key}"}`:      2,
		`flagnetic_http_requests_total{code="404",method="GET",route="unmatched"}`:                  1,
		`flagnetic_http_request_duration_seconds_count{method="GET",route="GET /test/flags/{key}"}`: 2,
	} {
		if got := metric(t, scrape, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
	if strings.Contains(scrape, "/test/flags/a") {
		t.Error("a path was used as a label; routes keep the label's cardinality bounded")
	}
}

func TestMetricsBoundTheMethodLabel(t *testing.T) {
	srv := testkit.StartServer(t)
	request(t, "FROBNICATE", srv.URL+"/healthz", nil)
	scrape := srv.Scrape(t)
	if got := metric(t, scrape, `flagnetic_http_requests_total{code="405",method="other",route="unmatched"}`); got != 1 {
		t.Errorf("an unknown method was not counted as other:\n%s", grep(scrape, "flagnetic_http_requests_total"))
	}
	if strings.Contains(scrape, "FROBNICATE") {
		t.Error("an unknown method became a label value")
	}
}

func TestPoolMetricsMoveAfterAQuery(t *testing.T) {
	srv := testkit.StartServer(t)
	before := srv.Scrape(t)
	if code, _ := get(t, srv.URL+"/readyz"); code != 200 {
		t.Fatalf("readiness = %d", code)
	}
	after := srv.Scrape(t)
	if b, a := metric(t, before, "flagnetic_db_pool_acquires_total"), metric(t, after, "flagnetic_db_pool_acquires_total"); b < 0 || a <= b {
		t.Errorf("flagnetic_db_pool_acquires_total went from %v to %v, want it to grow", b, a)
	}
	for _, name := range []string{"flagnetic_db_pool_acquired_conns", "flagnetic_db_pool_idle_conns", "flagnetic_db_pool_total_conns",
		"flagnetic_db_pool_max_conns", "flagnetic_db_pool_acquire_wait_seconds_total"} {
		if metric(t, after, name) < 0 {
			t.Errorf("no %s", name)
		}
	}
	if metric(t, after, "go_goroutines") < 0 || !strings.Contains(after, "process_") {
		t.Error("the Go runtime and process metrics are missing")
	}
}

func grep(s, prefix string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestWorkersOfTheInProcessServerRunOnlyWhenTicked(t *testing.T) {
	rounds := 0
	srv := testkit.StartServer(t, testkit.WithWorker(worker.Worker{Name: "rollup", Interval: time.Millisecond, Round: func(context.Context) error {
		rounds++
		return nil
	}}))
	time.Sleep(20 * time.Millisecond)
	if rounds != 0 {
		t.Fatalf("%d rounds ran on their own", rounds)
	}
	if err := srv.Tick(t, "rollup"); err != nil || rounds != 1 {
		t.Errorf("Tick = %v after %d rounds, want one round", err, rounds)
	}
	if got := metric(t, srv.Scrape(t), `flagnetic_worker_rounds_total{worker="rollup"}`); got != 1 {
		t.Errorf("flagnetic_worker_rounds_total = %v, want the round counted on the server's registry", got)
	}
}
