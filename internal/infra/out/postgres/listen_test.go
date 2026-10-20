package postgres_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/testkit"
)

type states chan bool

func (s states) Listening(up bool) { s <- up }
func (s states) Received(string)   {}

func TestADeafListenerIsReportedWhileItsReplicaStaysReady(t *testing.T) {
	srv := testkit.StartServer(t)
	fault := &pglisten.Fault{}
	s := make(states, 16)
	srv.Listen("rulesets", s, pglisten.Options{PingInterval: 50 * time.Millisecond, PingTimeout: 100 * time.Millisecond, Fault: fault})

	// The replica's readiness and its listener gauge, read again and again
	// while the listener goes deaf and comes back.
	ready := func() int {
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	up := func() float64 { return testkit.Metric(t, srv.Scrape(t), `flagnetic_listener_up{channel="rulesets"}`) }
	await := func(want bool) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			if code := ready(); code != 200 {
				t.Fatalf("/readyz = %d with the listener %v, want 200 throughout", code, !want)
			}
			select {
			case got := <-s:
				if got != want {
					t.Fatalf("Listening(%v), want Listening(%v)", got, want)
				}
				return
			case <-deadline:
				t.Fatalf("no Listening(%v)", want)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	await(true)
	if got := up(); got != 1 {
		t.Errorf("flagnetic_listener_up = %v while listening", got)
	}
	fault.Silence()
	await(false)
	if got := up(); got != 0 {
		t.Errorf("flagnetic_listener_up = %v while deaf, want 0", got)
	}
	fault.Restore()
	await(true)
	if got := up(); got != 1 {
		t.Errorf("flagnetic_listener_up = %v after reconnecting, want 1", got)
	}
}
