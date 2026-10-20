// Package metrics is the replica's Prometheus registry and the listener that
// serves it. The listener is separate from the public one and serves
// /metrics alone, so internal metrics are never exposed with the APIs.
package metrics

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace prefixes every metric of the server's own.
const Namespace = "flagnetic"

// NewRegistry returns a registry with the Go runtime and process metrics. It
// is private to the replica, so nothing registered globally by a dependency
// leaks into it.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// Handler serves g's metrics on GET /metrics and nothing else.
func Handler(g prometheus.Gatherer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(g, promhttp.HandlerOpts{}))
	return mux
}

// Listener is the metrics listener of a replica.
type Listener struct {
	srv *http.Server
	ln  net.Listener
}

// Listen binds addr for g's metrics. Serving starts with Serve.
func Listen(ctx context.Context, addr string, g prometheus.Gatherer) (*Listener, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// Only Prometheus scrapes this listener; a short header timeout is all it
	// needs against a stuck connection.
	srv := &http.Server{Handler: Handler(g), ReadHeaderTimeout: 10 * time.Second}
	return &Listener{srv: srv, ln: ln}, nil
}

// Addr is the bound address.
func (l *Listener) Addr() net.Addr { return l.ln.Addr() }

// Serve serves until Stop; it returns http.ErrServerClosed then.
func (l *Listener) Serve() error { return l.srv.Serve(l.ln) }

// Stop lets a scrape in flight finish, for at most a few seconds since a
// scrape is cheap, and then closes the listener.
func (l *Listener) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := l.srv.Shutdown(ctx); err != nil {
		_ = l.srv.Close()
	}
}
