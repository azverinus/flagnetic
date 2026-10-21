// Package http is the server's HTTP edge: the public handler with its health
// and readiness probes, onto which the APIs and the console are mounted.
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
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
	// TrustedProxies are the peers whose X-Forwarded-For and
	// X-Forwarded-Proto are believed.
	TrustedProxies []netip.Prefix
	// ReadHeaderTimeout bounds reading a request's headers, so slow clients
	// cannot hold connections.
	ReadHeaderTimeout time.Duration
	// IdleTimeout closes a keep-alive connection idle that long.
	IdleTimeout time.Duration
	// BodyReadTimeout bounds reading a request's body, except on stream
	// routes; 0 sets none.
	BodyReadTimeout time.Duration
	// MaxBodyBytes limits a request's body unless its route declares its own
	// limit; 0 sets none.
	MaxBodyBytes int64
	// Metrics records every request; nil records none.
	Metrics *Metrics
}

// OptionsFrom returns the edge's options as cfg sets them, Logger and DB
// aside.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		TrustedProxies:    cfg.TrustedProxies,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		BodyReadTimeout:   cfg.HTTPBodyReadTimeout,
		MaxBodyBytes:      cfg.HTTPMaxBodyBytes,
	}
}

// RouteOption declares how the edge treats one route's requests.
type RouteOption func(*routeConfig)

type routeConfig struct {
	stream       bool
	maxBodyBytes int64
	secretInPath bool
}

// Stream marks a long-lived route, such as server-sent events: its requests
// get no body read deadline, which would otherwise end them.
func Stream() RouteOption { return func(c *routeConfig) { c.stream = true } }

// MaxBodyBytes gives a route a body limit of its own, for the few that take
// large bodies.
func MaxBodyBytes(n int64) RouteOption { return func(c *routeConfig) { c.maxBodyBytes = n } }

// SecretInPath marks a route whose path carries a secret, such as an
// invitation's: its requests are logged with the route's pattern in place
// of their path, so the secret never reaches a log line.
func SecretInPath() RouteOption { return func(c *routeConfig) { c.secretInPath = true } }

// Server is the public handler of a replica.
type Server struct {
	mux *http.ServeMux
	// patterns are the mounted patterns: the only values a route may take in
	// the log and the metrics besides "unmatched", so their cardinality stays
	// bounded.
	patterns map[string]routeConfig
	// secretPaths match, whatever the method, the paths of the routes
	// carrying a secret, so a request reaching one with a method it does not
	// serve is not logged with its secret either.
	secretPaths *http.ServeMux
	secretSeen  map[string]bool
	log         *slog.Logger
	db          Pinger
	opts        Options
	draining    atomic.Bool
}

// New returns the public handler with its probes mounted.
func New(o Options) *Server {
	s := &Server{
		mux:         http.NewServeMux(),
		patterns:    map[string]routeConfig{},
		secretPaths: http.NewServeMux(),
		secretSeen:  map[string]bool{},
		log:         slog.New(newLogHandler(o.Logger.Handler())),
		db:          o.DB,
		opts:        o,
	}
	s.Handle("GET /healthz", http.HandlerFunc(s.health))
	s.Handle("GET /readyz", http.HandlerFunc(s.ready))
	return s
}

// Drain makes readiness answer 503 from now on while every route is still
// served, so load balancers route away from a replica about to stop.
func (s *Server) Drain() { s.draining.Store(true) }

// Handle mounts h on pattern, a ServeMux pattern with a method and a path.
func (s *Server) Handle(pattern string, h http.Handler, opts ...RouteOption) {
	c := routeConfig{maxBodyBytes: s.opts.MaxBodyBytes}
	for _, opt := range opts {
		opt(&c)
	}
	s.mux.Handle(pattern, h)
	s.patterns[pattern] = c
	if _, path, _ := strings.Cut(pattern, " "); c.secretInPath && !s.secretSeen[path] {
		s.secretSeen[path] = true
		s.secretPaths.Handle(path, http.NotFoundHandler())
	}
}

// HTTPServer returns an http.Server serving the edge with its connection
// timeouts. It sets no write timeout, which would cut off streams.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Handler:           s,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
	}
}

// Logger returns the logger of the edge, whose records logged with a
// request's context carry the request.
func (s *Server) Logger() *slog.Logger { return s.log }

// ServeHTTP serves a request: it gives the request its ID, routes it, answers
// a panic as an internal problem, and writes one access log record.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	st := &requestState{id: requestID(r), client: resolveClient(r, s.opts.TrustedProxies)}
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

	_, pattern := s.mux.Handler(r)
	route, ok := s.patterns[pattern]
	if !ok {
		// No route, or a wrong method.
		st.route = "unmatched"
		s.serveUnmatched(rw, r)
		return
	}
	st.route = pattern
	if !route.stream && s.opts.BodyReadTimeout > 0 && r.Body != nil && r.Body != http.NoBody {
		r.Body = withReadDeadline(r.Body, r.ContentLength, http.NewResponseController(w), s.opts.BodyReadTimeout)
	}
	if route.maxBodyBytes > 0 {
		if r.ContentLength > route.maxBodyBytes {
			s.WriteError(rw, r, &http.MaxBytesError{Limit: route.maxBodyBytes})
			return
		}
		// The connection's own writer, so net/http learns of a body too
		// large and closes the connection rather than read the rest.
		r.Body = http.MaxBytesReader(w, r.Body, route.maxBodyBytes)
	}
	s.mux.ServeHTTP(rw, r)
}

// quietRoutes are the probes, left out of the access log so that load
// balancers polling them do not drown it.
var quietRoutes = map[string]bool{"GET /healthz": true, "GET /readyz": true}

// accessLog records a served request in the metrics and, probes aside, in
// the log.
func (s *Server) accessLog(r *http.Request, st *requestState, rw *recorder, took time.Duration, aborted bool) {
	status := rw.status
	if status == 0 && !aborted {
		status = http.StatusOK // a handler that wrote nothing answered 200
	}
	// Probes are counted, unlike logged: their rate is what a scrape shows of
	// the load balancers, at no cost in labels.
	s.opts.Metrics.observe(r.Method, st.route, status, took)
	if quietRoutes[st.route] {
		return
	}
	path := r.URL.Path
	if _, secret := s.secretPaths.Handler(r); secret != "" {
		path = secret
	}
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.Duration("duration", took),
		slog.Int64("bytes", rw.bytes),
		slog.String("client_ip", st.client.IP.String()),
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
