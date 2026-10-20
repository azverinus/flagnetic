// Package http is the server's HTTP edge: the public handler with its health
// and readiness probes, onto which the APIs and the console are mounted.
package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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
	Logger *slog.Logger
	// DB is pinged by every readiness check.
	DB Pinger
}

// Server is the public handler of a replica.
type Server struct {
	mux *http.ServeMux
	log *slog.Logger
	db  Pinger
}

// New returns the public handler with its probes mounted.
func New(o Options) *Server {
	s := &Server{mux: http.NewServeMux(), log: o.Logger, db: o.DB}
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)
	return s
}

// Handle mounts h on pattern, a ServeMux pattern with a method and a path.
func (s *Server) Handle(pattern string, h http.Handler) { s.mux.Handle(pattern, h) }

// ServeHTTP serves the mounted routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// health answers while the process runs, so an orchestrator restarts only a
// process that no longer answers at all.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

// ready answers whether this replica can serve: PostgreSQL answers a ping in
// time. A load balancer routes away from a replica that is not ready.
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
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
