package cli_test

import (
	"net"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMetricsAreServedOnTheirOwnListenerOnly(t *testing.T) {
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_METRICS_LISTEN_ADDR": "127.0.0.1:0"})
	rec := r.findLog("metrics listening")
	if rec == nil {
		t.Fatalf("no metrics listener:\n%s", r.stderr.String())
	}
	getStatus(t, r.baseURL+"/healthz")

	code, body := getStatus(t, "http://"+rec["addr"].(string)+"/metrics")
	if code != 200 || !strings.Contains(body, `flagnetic_http_requests_total{code="200",method="GET",route="GET /healthz"}`) ||
		!strings.Contains(body, "flagnetic_db_pool_max_conns 10") || !strings.Contains(body, "go_goroutines") {
		t.Errorf("GET /metrics on the metrics listener = %d:\n%s", code, body)
	}
	if code, _ := getStatus(t, r.baseURL+"/metrics"); code != 404 {
		t.Errorf("GET /metrics on the public listener = %d, want 404", code)
	}
}

func TestAnEmptyMetricsAddressOpensNoPort(t *testing.T) {
	// The file asks for a listener; the variable set empty switches it off.
	file := writeConfigFile(t, "metrics_listen_addr: 127.0.0.1:0\n")
	r := serve(t, map[string]string{
		"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_CONFIG_FILE": file, "FLAGNETIC_METRICS_LISTEN_ADDR": "",
	})
	if r.findLog("metrics listening") != nil {
		t.Errorf("a metrics listener was opened:\n%s", r.stderr.String())
	}
}

func TestServeFailsWhenTheMetricsListenerCannotBind(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	res := run(map[string]string{
		"DATABASE_URL":                  testkit.EmptyDatabase(t),
		"FLAGNETIC_LISTEN_ADDR":         "127.0.0.1:0",
		"FLAGNETIC_METRICS_LISTEN_ADDR": taken.Addr().String(),
	}, nil, "serve")
	if res.code != 1 || !strings.Contains(res.stderr, "metrics listener") {
		t.Errorf("exit code = %d, want 1 naming the metrics listener:\n%s", res.code, res.stderr)
	}
}
