package safego_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

type panics struct {
	mu    sync.Mutex
	names []string
}

func (p *panics) count(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = append(p.names, name)
}

func (p *panics) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.names...)
}

func TestGoRecoversLogsAndCountsAPanic(t *testing.T) {
	var log bytes.Buffer
	counted := &panics{}
	ctx := safego.WithPanicHook(t.Context(), counted.count)
	done := make(chan struct{})
	safego.Go(ctx, "cache-refresh", slog.New(slog.NewJSONHandler(&log, nil)), func(context.Context) {
		defer close(done)
		panic("poison row")
	})
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for len(counted.seen()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := counted.seen(); len(got) != 1 || got[0] != "cache-refresh" {
		t.Errorf("panic hook saw %v, want cache-refresh once", got)
	}
	out := log.String()
	for _, want := range []string{`"level":"ERROR"`, `"name":"cache-refresh"`, `"panic":"poison row"`, `safego_test.go`} {
		if !strings.Contains(out, want) {
			t.Errorf("log %s lacks %s", out, want)
		}
	}
}

func TestGoPassesTheContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(t.Context(), key{}, "value")
	got := make(chan any, 1)
	safego.Go(ctx, "reader", slog.Default(), func(ctx context.Context) { got <- ctx.Value(key{}) })
	if v := <-got; v != "value" {
		t.Errorf("fn got %v", v)
	}
}

func TestCallReturnsAPanicAsAnError(t *testing.T) {
	var log bytes.Buffer
	err := safego.Call(t.Context(), "round", slog.New(slog.NewJSONHandler(&log, nil)), func(context.Context) error {
		rows := []string{}
		_ = rows[len(rows)] // a runtime panic
		return nil
	})
	var p *safego.PanicError
	if !errors.As(err, &p) || !strings.Contains(p.Error(), "index out of range") {
		t.Errorf("Call = %v, want a PanicError naming the panic", err)
	}
	if !strings.Contains(log.String(), "stack") {
		t.Errorf("the panic was not logged with its stack: %s", log.String())
	}
	wantErr := errors.New("plain failure")
	if err := safego.Call(t.Context(), "round", slog.Default(), func(context.Context) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Errorf("Call = %v, want the function's own error", err)
	}
}

func TestAPanicWithoutAHookIsStillRecovered(t *testing.T) {
	err := safego.Call(context.Background(), "round", slog.New(slog.DiscardHandler), func(context.Context) error { panic("x") })
	var p *safego.PanicError
	if !errors.As(err, &p) {
		t.Errorf("Call = %v", err)
	}
}
