package cli_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// syncBuffer is a stderr a replica writes from several goroutines while the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// replica is a `flagnetic serve` running in-process.
type replica struct {
	t       *testing.T
	baseURL string
	stderr  *syncBuffer
	signals chan os.Signal
	// done is closed when serve has exited with code.
	done chan struct{}
	code int
}

// serve starts `flagnetic serve` and waits for its "listening" log line. The
// replica is stopped when the test ends unless the test stopped it.
func serve(t *testing.T, env map[string]string, opts ...cli.Option) *replica {
	t.Helper()
	// A test that does not look at the drain skips it, so it stops at once.
	// Nor does it open a metrics listener, unless it looks at one.
	for k, v := range map[string]string{"FLAGNETIC_LISTEN_ADDR": "127.0.0.1:0", "FLAGNETIC_SHUTDOWN_DRAIN": "0", "FLAGNETIC_METRICS_LISTEN_ADDR": "", "FLAGNETIC_SECRET_KEY": secretKey} {
		if _, ok := env[k]; !ok {
			env[k] = v
		}
	}
	r := &replica{t: t, stderr: &syncBuffer{}, signals: make(chan os.Signal, 2), done: make(chan struct{})}
	lookup := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
	go func() {
		defer close(r.done)
		r.code = cli.Main([]string{"serve"}, lookup, &bytes.Buffer{}, r.stderr, r.signals, opts...)
	}()
	t.Cleanup(func() {
		select {
		case <-r.done:
			return
		case r.signals <- syscall.SIGTERM:
		}
		select {
		case <-r.done:
		case <-time.After(30 * time.Second):
			t.Error("serve did not stop when the test ended")
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if rec := r.findLog("listening"); rec != nil {
			addr, ok := rec["addr"].(string)
			if !ok {
				t.Fatalf("the listening record has no addr: %v", rec)
			}
			r.baseURL = "http://" + addr
			return r
		}
		select {
		case <-r.done:
			t.Fatalf("serve exited with %d before listening:\n%s", r.code, r.stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("serve did not start listening:\n%s", r.stderr.String())
	return nil
}

func (r *replica) findLog(msg string) map[string]any {
	sc := bufio.NewScanner(strings.NewReader(r.stderr.String()))
	for sc.Scan() {
		var rec map[string]any
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec["msg"] == msg {
			return rec
		}
	}
	return nil
}

// stop sends SIGTERM and returns the exit code and how long serve took to exit.
func (r *replica) stop() (int, time.Duration) {
	r.t.Helper()
	start := time.Now()
	r.signals <- syscall.SIGTERM
	select {
	case <-r.done:
		return r.code, time.Since(start)
	case <-time.After(30 * time.Second):
		r.t.Fatalf("serve still running 30s after SIGTERM:\n%s", r.stderr.String())
		return 0, 0
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "flagnetic.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeAnswersProbesAndStopsOnSIGTERM(t *testing.T) {
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)})
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(r.baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
	if code, _ := r.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, r.stderr.String())
	}
}

func TestServeReadsAYAMLConfigurationFile(t *testing.T) {
	file := writeConfigFile(t, "database_url: "+testkit.EmptyDatabase(t)+"\nlisten_addr: 127.0.0.1:0\nlog_level: debug\n")
	r := serve(t, map[string]string{"FLAGNETIC_CONFIG_FILE": file}, fixtures)
	if rec := r.findLog("migrations applied"); rec == nil || rec["applied"] != 2.0 {
		t.Errorf("serve did not migrate the file's database: %v\n%s", rec, r.stderr.String())
	}
}

func TestTheEnvironmentOverridesTheConfigurationFile(t *testing.T) {
	file := writeConfigFile(t, "database_url: "+testkit.EmptyDatabase(t)+"\nauto_migrate: false\n")
	r := serve(t, map[string]string{"FLAGNETIC_CONFIG_FILE": file, "FLAGNETIC_AUTO_MIGRATE": "true"}, fixtures)
	if r.findLog("migrations applied") == nil {
		t.Errorf("FLAGNETIC_AUTO_MIGRATE=true did not win over the file:\n%s", r.stderr.String())
	}
}

func TestAnEmptyVariableStillOverridesTheConfigurationFile(t *testing.T) {
	file := writeConfigFile(t, "database_url: "+testkit.EmptyDatabase(t)+"\n")
	res := run(map[string]string{"FLAGNETIC_CONFIG_FILE": file, "DATABASE_URL": ""}, nil, "migrate")
	if res.code != 2 || !strings.Contains(res.stderr, "DATABASE_URL is required") {
		t.Errorf("exit code = %d, stderr:\n%s\nwant 2: a set but empty DATABASE_URL wins over the file", res.code, res.stderr)
	}
}

func TestAConfigurationFileWithAnUnknownKeyIsRefused(t *testing.T) {
	file := writeConfigFile(t, "database_url: postgres://localhost/flagnetic\nlisten_port: \":9090\"\n")
	for _, cmd := range []string{"serve", "migrate"} {
		res := run(map[string]string{"FLAGNETIC_CONFIG_FILE": file}, nil, cmd)
		if res.code != 2 || !strings.Contains(res.stderr, "listen_port") {
			t.Errorf("%s: exit code = %d, stderr:\n%s\nwant 2 naming the unknown key", cmd, res.code, res.stderr)
		}
	}
}

func TestAMalformedSettingIsRefusedBeforeAnythingStarts(t *testing.T) {
	res := run(map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic", "FLAGNETIC_SHUTDOWN_GRACE": "soon"}, nil, "serve")
	if res.code != 2 || !strings.Contains(res.stderr, "FLAGNETIC_SHUTDOWN_GRACE") || !strings.Contains(res.stderr, "for example 15s") {
		t.Errorf("exit code = %d, stderr:\n%s\nwant 2 naming the variable and an example", res.code, res.stderr)
	}
}

func TestServeStopsWithinTheGracePeriodWhileARequestIsStuck(t *testing.T) {
	entered := make(chan struct{})
	stuck := cli.WithRoute("GET /test/stuck", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done() // released only by the cancellation after the grace period
	}))
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_SHUTDOWN_GRACE": "500ms"}, stuck)

	go func() {
		resp, err := http.Get(r.baseURL + "/test/stuck")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered

	code, took := r.stop()
	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, r.stderr.String())
	}
	if took < 500*time.Millisecond || took > 5*time.Second {
		t.Errorf("serve stopped %v after SIGTERM, want the 500ms grace period and not much more", took)
	}
}

func TestServeFailsWhenItCannotListen(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	res := run(map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_LISTEN_ADDR": taken.Addr().String()}, nil, "serve")
	if res.code != 1 {
		t.Errorf("exit code = %d, want 1:\n%s", res.code, res.stderr)
	}
}

func TestPoolSize(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	withPoolMax := func(n string) string {
		sep := "&"
		if !strings.Contains(url, "?") {
			sep = "?"
		}
		return url + sep + "pool_max_conns=" + n
	}
	for _, tt := range []struct {
		name     string
		env      map[string]string
		want     float64
		notified bool
	}{
		{"default", map[string]string{"DATABASE_URL": url}, 10, false},
		{"from the URL", map[string]string{"DATABASE_URL": withPoolMax("3")}, 3, false},
		{"from the variable", map[string]string{"DATABASE_URL": url, "FLAGNETIC_DB_MAX_CONNS": "7"}, 7, false},
		{"the variable wins over the URL", map[string]string{"DATABASE_URL": withPoolMax("3"), "FLAGNETIC_DB_MAX_CONNS": "7"}, 7, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := run(tt.env, nil, "migrate")
			if res.code != 0 {
				t.Fatalf("exit code = %d:\n%s", res.code, res.stderr)
			}
			if got := res.logRecord(t, "database pool")["max_conns"]; got != tt.want {
				t.Errorf("max_conns = %v, want %v", got, tt.want)
			}
			notified := false
			for _, rec := range res.logRecords(t) {
				notified = notified || strings.Contains(rec["msg"].(string), "pool_max_conns")
			}
			if notified != tt.notified {
				t.Errorf("notice that the variable overrides the URL logged = %v, want %v:\n%s", notified, tt.notified, res.stderr)
			}
		})
	}
	res := run(map[string]string{"DATABASE_URL": url, "FLAGNETIC_DB_MAX_CONNS": "lots"}, nil, "migrate")
	if res.code != 2 || !strings.Contains(res.stderr, "FLAGNETIC_DB_MAX_CONNS") {
		t.Errorf("a malformed pool size: exit code = %d, stderr:\n%s", res.code, res.stderr)
	}
}
