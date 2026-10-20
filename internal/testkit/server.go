package testkit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// Server is a replica's public handler served in-process, for black-box HTTP
// tests without a network listener of the binary's own.
type Server struct {
	// URL is the base URL, such as http://127.0.0.1:53211.
	URL string
	// Logs captures everything the server logs.
	Logs *LogSink
}

type serverOptions struct {
	pool   *pgxpool.Pool
	routes []route
}

type route struct {
	pattern string
	build   func(*flaghttp.Server) http.Handler
}

// WithRoute mounts the handler build returns on pattern, for a test to
// exercise the edge through routes of its own. build receives the server, to
// answer errors through it or log with its logger.
func WithRoute(pattern string, build func(*flaghttp.Server) http.Handler) ServerOption {
	return func(o *serverOptions) { o.routes = append(o.routes, route{pattern, build}) }
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
	h := flaghttp.New(flaghttp.Options{Logger: logs.Logger(), DB: o.pool})
	for _, r := range o.routes {
		h.Handle(r.pattern, r.build(h))
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Server{URL: srv.URL, Logs: logs}
}
