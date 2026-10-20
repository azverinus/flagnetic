package cli_test

import (
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
	"github.com/azverinus/flagnetic/internal/testkit"
)

var echo = cli.WithRoute("GET /test/echo", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "echo")
}))

func getStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// signal sends a stop signal without waiting for serve to exit.
func (r *replica) signal() { r.signals <- syscall.SIGTERM }

// wait returns the exit code and how long serve took to exit after since.
func (r *replica) wait(since time.Time, bound time.Duration) (int, time.Duration) {
	r.t.Helper()
	select {
	case <-r.done:
		return r.code, time.Since(since)
	case <-time.After(bound):
		r.t.Fatalf("serve still running %v after the signal:\n%s", bound, r.stderr.String())
		return 0, 0
	}
}

func TestAReplicaDrainsBeforeItStops(t *testing.T) {
	r := serve(t, map[string]string{
		"DATABASE_URL":             testkit.EmptyDatabase(t),
		"FLAGNETIC_SHUTDOWN_DRAIN": "1s",
		"FLAGNETIC_SHUTDOWN_GRACE": "1s",
	}, echo)

	start := time.Now()
	r.signal()
	deadline := time.Now().Add(time.Second)
	for {
		code, body := getStatus(t, r.baseURL+"/readyz")
		if code == 503 && body == `{"status":"draining"}`+"\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz = %d %q a second into a drain of one, want 503 draining", code, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, body := getStatus(t, r.baseURL+"/test/echo"); code != 200 || body != "echo" {
		t.Errorf("a request made during the drain = %d %q, want it served", code, body)
	}

	code, took := r.wait(start, 10*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, r.stderr.String())
	}
	if took < time.Second || took > 5*time.Second {
		t.Errorf("serve stopped %v after SIGTERM, want after the 1s drain and within drain plus grace", took)
	}
}

func TestAZeroDrainGoesStraightToTheGracePeriod(t *testing.T) {
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_SHUTDOWN_DRAIN": "0"})
	if code, took := r.stop(); code != 0 || took > 2*time.Second {
		t.Errorf("exit code %d after %v, want 0 at once with no drain", code, took)
	}
}

func TestASecondSignalExitsAtOnce(t *testing.T) {
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_SHUTDOWN_DRAIN": "30s"})
	start := time.Now()
	r.signal()
	if !waitFor(func() bool { return r.findLog("draining") != nil }) {
		t.Fatalf("serve did not start draining:\n%s", r.stderr.String())
	}
	r.signal()
	code, took := r.wait(start, 5*time.Second)
	if code != 1 || took > 2*time.Second {
		t.Errorf("exit code %d after %v, want 1 at once", code, took)
	}
	if !strings.Contains(r.stderr.String(), `"level":"WARN","msg":"second signal`) {
		t.Errorf("no warn log for the second signal:\n%s", r.stderr.String())
	}
	// serve winds down behind the exit; let it finish before the test's
	// database goes away.
	if !waitFor(func() bool { return r.findLog("stopped") != nil }) {
		t.Errorf("serve did not wind down after exiting:\n%s", r.stderr.String())
	}
}

func waitFor(cond func() bool) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return false
}

func healthcheck(addr string) result {
	return run(map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic", "FLAGNETIC_LISTEN_ADDR": addr}, nil, "healthcheck")
}

func TestHealthcheck(t *testing.T) {
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_SHUTDOWN_DRAIN": "30s"})
	addr := strings.TrimPrefix(r.baseURL, "http://")
	_, port, _ := net.SplitHostPort(addr)

	if res := healthcheck(addr); res.code != 0 {
		t.Errorf("against a ready replica: exit code = %d, want 0:\n%s", res.code, res.stderr)
	}
	if res := healthcheck(":" + port); res.code != 0 {
		t.Errorf("against a listen address with no host: exit code = %d, want 0 through loopback:\n%s", res.code, res.stderr)
	}

	r.signal()
	if !waitFor(func() bool { return r.findLog("draining") != nil }) {
		t.Fatal("serve did not start draining")
	}
	if res := healthcheck(addr); res.code != 1 {
		t.Errorf("against a draining replica: exit code = %d, want 1", res.code)
	}

	idle, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nothing := idle.Addr().String()
	_ = idle.Close()
	if res := healthcheck(nothing); res.code != 1 {
		t.Errorf("against nothing listening: exit code = %d, want 1", res.code)
	}
}
