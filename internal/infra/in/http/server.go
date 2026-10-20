// Package http is the server's HTTP edge: the public handler with its health
// and readiness probes, onto which the APIs and the console are mounted.
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// readinessTimeout bounds the database ping of a readiness check, so a probe
// answers before a load balancer's own timeout even when PostgreSQL hangs.
const readinessTimeout = 2 * time.Second

// Pinger is the database as readiness sees it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options are what the edge depends on.
type Options struct {
	// Logger is wrapped so that records logged with a request's context carry
	// the request's ID and route.
	Logger *slog.Logger
	// DB is pinged by every readiness check.
	DB Pinger
}

// Server is the public handler of a replica.
type Server struct {
	mux *http.ServeMux
	// patterns are the mounted patterns: the only values a route may take in
	// the log and the metrics besides "unmatched", so their cardinality stays
	// bounded.
	patterns map[string]bool
	log      *slog.Logger
	db       Pinger
	draining atomic.Bool
}

// New returns the public handler with its probes mounted.
func New(o Options) *Server {
	s := &Server{
		mux:      http.NewServeMux(),
		patterns: map[string]bool{},
		log:      slog.New(newLogHandler(o.Logger.Handler())),
		db:       o.DB,
	}
	s.Handle("GET /healthz", http.HandlerFunc(s.health))
	s.Handle("GET /readyz", http.HandlerFunc(s.ready))
	return s
}

// Drain makes readiness answer 503 from now on while every route is still
// served, so load balancers route away from a replica about to stop.
func (s *Server) Drain() { s.draining.Store(true) }

// Handle mounts h on pattern, a ServeMux pattern with a method and a path.
func (s *Server) Handle(pattern string, h http.Handler) {
	s.mux.Handle(pattern, h)
	s.patterns[pattern] = true
}

// Logger returns the logger of the edge, whose records logged with a
// request's context carry the request.
func (s *Server) Logger() *slog.Logger { return s.log }

// ServeHTTP serves a request: it gives the request its ID, routes it, answers
// a panic as an internal problem, and writes one access log record.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	st := &requestState{id: requestID(r)}
	w.Header().Set("X-Request-ID", st.id)
	r = r.WithContext(context.WithValue(r.Context(), stateKey{}, st))
	rw := &recorder{ResponseWriter: w}

	defer func() {
		p := recover()
		if p == nil {
			s.accessLog(r, st, rw, time.Since(start), false)
			return
		}
		aborted := p == http.ErrAbortHandler //nolint:errorlint // a sentinel compared as net/http does
		if !aborted {
			s.log.ErrorContext(r.Context(), "panic serving a request", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
		if aborted || rw.committed() {
			// The response cannot become a problem any more: aborting the
			// connection is the only way to tell the client it is incomplete,
			// rather than letting a truncated body end like a whole one.
			s.accessLog(r, st, rw, time.Since(start), true)
			panic(http.ErrAbortHandler)
		}
		s.writeProblem(rw, r, internalProblem())
		s.accessLog(r, st, rw, time.Since(start), false)
	}()

	if _, pattern := s.mux.Handler(r); s.patterns[pattern] {
		st.route = pattern
	} else {
		// No route, a wrong method, or a redirect to a cleaned path.
		st.route = "unmatched"
		s.serveUnmatched(rw, r)
		return
	}
	s.mux.ServeHTTP(rw, r)
}

// quietRoutes are the probes, left out of the access log so that load
// balancers polling them do not drown it.
var quietRoutes = map[string]bool{"GET /healthz": true, "GET /readyz": true}

func (s *Server) accessLog(r *http.Request, st *requestState, rw *recorder, took time.Duration, aborted bool) {
	if quietRoutes[st.route] {
		return
	}
	status := rw.status
	if status == 0 && !aborted {
		status = http.StatusOK // a handler that wrote nothing answered 200
	}
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Duration("duration", took),
		slog.Int64("bytes", rw.bytes),
	}
	if aborted {
		attrs = append(attrs, slog.Bool("aborted", true))
	}
	s.log.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
}

// health answers while the process runs, so an orchestrator restarts only a
// process that no longer answers at all.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

// ready answers whether this replica can serve: PostgreSQL answers a ping in
// time. A load balancer routes away from a replica that is not ready.
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		writeStatus(w, http.StatusServiceUnavailable, "draining")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		s.log.WarnContext(r.Context(), "not ready", "reason", "PostgreSQL did not answer", "error", err.Error())
		writeStatus(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeStatus(w, http.StatusOK, "ok")
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
	}{status})
}
