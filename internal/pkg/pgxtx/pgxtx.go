// Package pgxtx carries a PostgreSQL transaction in a context, so the code that
// owns a unit of work begins it and the code that runs statements finds it,
// without the transaction appearing in any signature in between.
//
// The owner calls Within (or Snapshot for a consistent read), and every
// statement goes through Querier, which returns the context's transaction or,
// outside one, the pool. Code declaring the unit of work needs only an
// interface with the Within method, so it never mentions the driver.
//
// There is no automatic retry: a deadlock or serialization failure is returned
// (through the configured wrapper) for the caller to report, and the lock
// order of a unit of work is fixed in its code instead.
package pgxtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is what a statement runs on: a transaction or the pool.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, rows pgx.CopyFromSource) (int64, error)
}

// Querier returns the transaction ctx carries, or pool outside one. Every
// statement of a repository goes through it, so the repository takes part in
// whatever unit of work its caller began.
func Querier(ctx context.Context, pool *pgxpool.Pool) DB {
	if c, ok := ctx.Value(txKey{}).(*current); ok {
		return c.tx
	}
	return pool
}

type txKey struct{}

type mode int

const (
	readWrite mode = iota + 1
	snapshot
)

type current struct {
	tx   pgx.Tx
	mode mode
	// afterCommit are the hooks to run once tx commits, in order.
	afterCommit []func()
	// locals are the values kept for the transaction's life by Local.
	locals map[any]any
}

// AfterCommit runs fn once the transaction ctx carries commits, and never if
// it rolls back; outside a transaction, where each statement commits on its
// own, it runs fn at once. It lets a repository tell the rest of its process
// about a change only once other transactions can see it, such as a
// replica's cache learning the version it just wrote. fn runs on the
// committing goroutine and must return promptly.
func AfterCommit(ctx context.Context, fn func()) {
	if c, ok := ctx.Value(txKey{}).(*current); ok {
		c.afterCommit = append(c.afterCommit, fn)
		return
	}
	fn()
}

// Local returns the value the transaction ctx carries under key, storing
// init() there first when it carries none, so code called more than once in
// one unit of work remembers what it learned earlier in it, such as what a
// row held before the unit of work changed it. key is compared as a map
// key, so a package uses an unexported type of its own. Outside a
// transaction there is nothing to remember: it returns init() and false.
func Local[T any](ctx context.Context, key any, init func() T) (T, bool) {
	c, ok := ctx.Value(txKey{}).(*current)
	if !ok {
		return init(), false
	}
	if v, ok := c.locals[key]; ok {
		// Only Local stores under key, and always a T for that key.
		return v.(T), true
	}
	if c.locals == nil {
		c.locals = map[any]any{}
	}
	v := init()
	c.locals[key] = v
	return v, true
}

// Transactor begins transactions on a pool.
type Transactor struct {
	pool              *pgxpool.Pool
	concurrencyFailed func(error) error
}

// Option configures a Transactor.
type Option func(*Transactor)

// WithConcurrencyFailure sets how a deadlock (SQLSTATE 40P01) or a
// serialization failure (40001) returned by a unit of work is reported: wrap
// turns it into the error the caller returns, keeping it as the cause.
func WithConcurrencyFailure(wrap func(error) error) Option {
	return func(t *Transactor) { t.concurrencyFailed = wrap }
}

// New returns a Transactor beginning transactions on pool.
func New(pool *pgxpool.Pool, opts ...Option) *Transactor {
	t := &Transactor{pool: pool, concurrencyFailed: func(err error) error { return err }}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Within runs fn in a READ COMMITTED read-write transaction carried by the
// context it passes to fn. It commits when fn returns nil and rolls back when
// fn returns an error or panics, re-panicking after the rollback. A Within
// inside another joins the outer transaction: only the outermost commits, so a
// unit of work composed of others is still one. An inner failure rolls
// everything back when the outer function returns it; an outer function that
// swallows it commits whatever statements succeeded. A Within inside a Snapshot is
// a programming error and panics.
func (t *Transactor) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.run(ctx, readWrite, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}, fn)
}

// Snapshot runs fn in a REPEATABLE READ READ ONLY transaction, so a query of
// several statements sees one consistent view while other transactions
// commit. A Snapshot inside another joins it; one inside a Within is a
// programming error and panics.
func (t *Transactor) Snapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.run(ctx, snapshot, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

func (t *Transactor) run(ctx context.Context, m mode, opts pgx.TxOptions, fn func(context.Context) error) (err error) {
	if c, ok := ctx.Value(txKey{}).(*current); ok {
		if c.mode != m {
			panic(fmt.Sprintf("pgxtx: a %s cannot run inside a %s", m, c.mode))
		}
		return fn(ctx)
	}

	tx, err := t.pool.BeginTx(ctx, opts)
	if err != nil {
		return t.report(fmt.Errorf("begin transaction: %w", err))
	}
	// Unless fn returned and the commit was attempted, the transaction is
	// rolled back on the way out: after a panic, which is then re-raised, and
	// after runtime.Goexit too, so the connection always goes back to the
	// pool. The rollback runs even when ctx is cancelled.
	finished := false
	defer func() {
		if finished {
			return
		}
		r := recover()
		_ = tx.Rollback(context.WithoutCancel(ctx))
		if r != nil {
			panic(r)
		}
	}()

	c := &current{tx: tx, mode: m}
	if err := fn(context.WithValue(ctx, txKey{}, c)); err != nil {
		finished = true
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return t.report(err)
	}
	finished = true
	if err := tx.Commit(ctx); err != nil {
		return t.report(fmt.Errorf("commit transaction: %w", err))
	}
	for _, hook := range c.afterCommit {
		hook()
	}
	return nil
}

func (t *Transactor) report(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001") {
		return t.concurrencyFailed(err)
	}
	return err
}

func (m mode) String() string {
	if m == snapshot {
		return "Snapshot"
	}
	return "Within"
}
