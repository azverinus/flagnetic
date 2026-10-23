// These tests terminate the listener's backend through pg_stat_activity: the
// connection is the listener's own, and terminating it is the only way to
// break it the way a network or a restart does.
package pglisten_test

import (
	"context"
	"crypto/rand"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/pkg/pgtest"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(context.Context, *pgxpool.Pool) error { return nil }))
}

// recorder is a handler that hands every call to the test.
type recorder struct {
	listening chan bool
	payloads  chan string
}

func newRecorder() *recorder {
	return &recorder{listening: make(chan bool, 64), payloads: make(chan string, 64)}
}

func (r *recorder) Listening(up bool)       { r.listening <- up }
func (r *recorder) Received(payload string) { r.payloads <- payload }

func (r *recorder) expectListening(t *testing.T, want bool, within time.Duration) {
	t.Helper()
	select {
	case got := <-r.listening:
		if got != want {
			t.Fatalf("Listening(%v), want Listening(%v)", got, want)
		}
	case <-time.After(within):
		t.Fatalf("no Listening(%v) within %v", want, within)
	}
}

type listener struct {
	rec     *recorder
	appName string
	fault   *pglisten.Fault
}

// start runs a listener on channel until the test ends.
func start(t *testing.T, channel string, opts pglisten.Options) *listener {
	t.Helper()
	cfg, err := pgx.ParseConfig(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	l := &listener{
		rec:     newRecorder(),
		appName: "pglisten-" + strings.ToLower(rand.Text()),
		fault:   &pglisten.Fault{},
	}
	cfg.RuntimeParams["application_name"] = l.appName
	opts.Logger = slog.New(slog.DiscardHandler)
	opts.Fault = l.fault
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pglisten.Run(ctx, cfg, channel, l.rec, opts)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	l.rec.expectListening(t, true, 10*time.Second)
	return l
}

func notify(t *testing.T, channel, payload string) {
	t.Helper()
	if _, err := pgtest.Pool(t).Exec(t.Context(), "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		t.Fatal(err)
	}
}

func channel() string { return "test_" + strings.ToLower(rand.Text()) }

func TestAPayloadIsDelivered(t *testing.T) {
	ch := channel()
	l := start(t, ch, pglisten.Options{})
	notify(t, ch, "flag beta changed")
	select {
	case got := <-l.rec.payloads:
		if got != "flag beta changed" {
			t.Errorf("payload = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the payload was not delivered")
	}
}

func TestTheListenerReconnectsAfterItsBackendIsTerminated(t *testing.T) {
	ch := channel()
	l := start(t, ch, pglisten.Options{})
	if _, err := pgtest.Pool(t).Exec(t.Context(),
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1", l.appName); err != nil {
		t.Fatal(err)
	}
	l.rec.expectListening(t, false, 5*time.Second)
	l.rec.expectListening(t, true, 10*time.Second)
	notify(t, ch, "after the reconnect")
	select {
	case got := <-l.rec.payloads:
		if got != "after the reconnect" {
			t.Errorf("payload = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no payload after the reconnect")
	}
}

func TestASilencedListenerIsDetectedAndReconnects(t *testing.T) {
	l := start(t, channel(), pglisten.Options{PingInterval: 100 * time.Millisecond, PingTimeout: 200 * time.Millisecond})
	l.fault.Silence()
	silenced := time.Now()
	l.rec.expectListening(t, false, 5*time.Second)
	if took := time.Since(silenced); took > 2*time.Second {
		t.Errorf("a silenced listener was detected after %v, want within the ping interval and timeout", took)
	}
	// Found deaf, it stays down while silenced, well past its 100ms backoff.
	select {
	case up := <-l.rec.listening:
		t.Fatalf("Listening(%v) while silenced, want the listener to stay down", up)
	case <-time.After(500 * time.Millisecond):
	}
	l.fault.Restore()
	l.rec.expectListening(t, true, 10*time.Second)
}

func TestPingsDoNotReachTheHandler(t *testing.T) {
	l := start(t, channel(), pglisten.Options{PingInterval: 20 * time.Millisecond, PingTimeout: time.Second})
	time.Sleep(200 * time.Millisecond)
	select {
	case got := <-l.rec.payloads:
		t.Errorf("the handler received %q", got)
	case up := <-l.rec.listening:
		t.Errorf("the listener reported Listening(%v) while its pings came back", up)
	default:
	}
}

type panicky struct{ *recorder }

func (p panicky) Received(payload string) {
	if payload == "poison" {
		panic("poison payload")
	}
	p.recorder.Received(payload)
}

func TestAPanickingHandlerIsRecoveredAndTheListenerReconnects(t *testing.T) {
	ch := channel()
	cfg, err := pgx.ParseConfig(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	counted := make(chan string, 4)
	ctx, cancel := context.WithCancel(safego.WithPanicHook(context.Background(), func(name string) { counted <- name }))
	done := make(chan struct{})
	go func() {
		defer close(done)
		pglisten.Run(ctx, cfg, ch, panicky{rec}, pglisten.Options{Logger: slog.New(slog.DiscardHandler)})
	}()
	t.Cleanup(func() { cancel(); <-done })
	rec.expectListening(t, true, 10*time.Second)
	notify(t, ch, "poison")
	rec.expectListening(t, false, 5*time.Second)
	select {
	case name := <-counted:
		if name != "listener "+ch {
			t.Errorf("the panic was counted under %q, want %q", name, "listener "+ch)
		}
	case <-time.After(5 * time.Second):
		t.Error("the handler's panic was not counted")
	}
	rec.expectListening(t, true, 10*time.Second)
}
