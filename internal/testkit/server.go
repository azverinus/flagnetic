package testkit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
)

// Server is a replica's public handler served in-process, for black-box HTTP
// tests without a network listener of the binary's own.
type Server struct {
	// URL is the base URL, such as http://127.0.0.1:53211.
	URL string
	// Logs captures everything the server logs.
	Logs *LogSink

	metrics http.Handler
}

// Scrape returns the server's metrics in the Prometheus text format, as the
// metrics listener serves them.
func (s *Server) Scrape(t testing.TB) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.metrics.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape = %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

type serverOptions struct {
	pool   *pgxpool.Pool
	edge   []func(*flaghttp.Options)
	routes []route
}

type route struct {
	pattern string
	build   func(*flaghttp.Server) http.Handler
	opts    []flaghttp.RouteOption
}

// WithRoute mounts the handler build returns on pattern, for a test to
// exercise the edge through routes of its own. build receives the server, to
// answer errors through it or log with its logger.
func WithRoute(pattern string, build func(*flaghttp.Server) http.Handler, opts ...flaghttp.RouteOption) ServerOption {
	return func(o *serverOptions) { o.routes = append(o.routes, route{pattern, build, opts}) }
}

// WithEdge changes the edge's options, which otherwise are the defaults of
// the configuration.
func WithEdge(change func(*flaghttp.Options)) ServerOption {
	return func(o *serverOptions) { o.edge = append(o.edge, change) }
}

// ServerOption changes what the in-process server is built with.
type ServerOption func(*serverOptions)

// WithPool serves with pool instead of the package's migrated one, such as a
// pool whose PostgreSQL is unreachable.
func WithPool(pool *pgxpool.Pool) ServerOption {
	return func(o *serverOptions) { o.pool = pool }
}

// StartServer serves the public handler until the test ends.
func StartServer(t testing.TB, opts ...ServerOption) *Server {
	t.Helper()
	o := serverOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.pool == nil {
		o.pool = Pool(t)
	}
	logs := NewLogSink()
	edge := flaghttp.OptionsFrom(defaults(t))
	reg := metrics.NewRegistry()
	reg.MustRegister(postgres.NewPoolCollector(o.pool))
	edge.Logger, edge.DB, edge.Metrics = logs.Logger(), o.pool, flaghttp.NewMetrics(reg)
	for _, change := range o.edge {
		change(&edge)
	}
	h := flaghttp.New(edge)
	for _, r := range o.routes {
		h.Handle(r.pattern, r.build(h), r.opts...)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Config = h.HTTPServer()
	srv.Start()
	t.Cleanup(srv.Close)
	return &Server{URL: srv.URL, Logs: logs, metrics: metrics.Handler(reg)}
}

// defaults returns the configuration a replica has when nothing is set.
func defaults(t testing.TB) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) {
		if k == "DATABASE_URL" {
			return "postgres://localhost/unused", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
