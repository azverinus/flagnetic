package testkit

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryLog records the SQL of every statement a pool sends, so a test can
// show how many queries a request costs.
type QueryLog struct {
	mu  sync.Mutex
	sql []string
}

var _ pgx.QueryTracer = (*QueryLog)(nil)

// TraceQueryStart records the statement.
func (l *QueryLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sql = append(l.sql, data.SQL)
	return ctx
}

// TraceQueryEnd does nothing: a statement counts once it is sent.
func (l *QueryLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Reset forgets every statement recorded so far.
func (l *QueryLog) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sql = nil
}

// Containing returns how many statements recorded since the last Reset hold
// text.
func (l *QueryLog) Containing(text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, q := range l.sql {
		if strings.Contains(q, text) {
			n++
		}
	}
	return n
}

// TracedPool returns a pool of its own on the package's migrated schema
// whose statements log records, closed when the test ends.
func TracedPool(t testing.TB) (*pgxpool.Pool, *QueryLog) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	log := &QueryLog{}
	cfg.ConnConfig.Tracer = log
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, log
}
