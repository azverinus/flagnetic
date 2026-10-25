// These tests write and read a table directly, unlike every other test in the
// repository: what is under test is the transaction itself, and there is no
// repository in front of it to observe it through.
package pgxtx_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/pkg/pgtest"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m, func(ctx context.Context, pool *pgxpool.Pool) error {
		_, err := pool.Exec(ctx, `CREATE TABLE note (key text PRIMARY KEY, body text NOT NULL DEFAULT '')`)
		return err
	}))
}

var errFailed = errors.New("failed")

// key returns a key no other test uses, so tests sharing the table never see
// each other's rows.
func key() string { return rand.Text() }

func insert(ctx context.Context, t *testing.T, pool *pgxpool.Pool, k string) {
	t.Helper()
	if _, err := pgxtx.Querier(ctx, pool).Exec(ctx, `INSERT INTO note (key) VALUES ($1)`, k); err != nil {
		t.Fatalf("insert %s: %v", k, err)
	}
}

func exists(t *testing.T, pool *pgxpool.Pool, k string) bool {
	t.Helper()
	var found bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM note WHERE key = $1)`, k).Scan(&found); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestWithinCommitsWhenTheFunctionSucceeds(t *testing.T) {
	pool := pgtest.Pool(t)
	tx := pgxtx.New(pool)
	k := key()
	err := tx.Within(t.Context(), func(ctx context.Context) error {
		insert(ctx, t, pool, k)
		if exists(t, pool, k) {
			t.Error("the write is visible outside the transaction before it commits")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Within = %v", err)
	}
	if !exists(t, pool, k) {
		t.Error("the write is not visible after the commit")
	}
}

func TestWithinRollsBackWhenTheFunctionFails(t *testing.T) {
	pool := pgtest.Pool(t)
	k := key()
	err := pgxtx.New(pool).Within(t.Context(), func(ctx context.Context) error {
		insert(ctx, t, pool, k)
		return errFailed
	})
	if !errors.Is(err, errFailed) {
		t.Fatalf("Within = %v, want the function's error", err)
	}
	if exists(t, pool, k) {
		t.Error("the write of a failed function was committed")
	}
}

func TestWithinRollsBackAndRepanicsOnAPanic(t *testing.T) {
	pool := pgtest.Pool(t)
	k := key()
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the function's panic", r)
			}
		}()
		_ = pgxtx.New(pool).Within(t.Context(), func(ctx context.Context) error {
			insert(ctx, t, pool, k)
			panic("boom")
		})
	}()
	if exists(t, pool, k) {
		t.Error("the write of a panicking function was committed")
	}
}

func TestNestedWithinJoinsTheOuterTransaction(t *testing.T) {
	pool := pgtest.Pool(t)
	tx := pgxtx.New(pool)
	outer, inner := key(), key()
	err := tx.Within(t.Context(), func(ctx context.Context) error {
		insert(ctx, t, pool, outer)
		_ = tx.Within(ctx, func(ctx context.Context) error {
			insert(ctx, t, pool, inner)
			return nil
		})
		if exists(t, pool, inner) {
			t.Error("the inner Within committed on its own")
		}
		return errFailed
	})
	if !errors.Is(err, errFailed) {
		t.Fatalf("Within = %v", err)
	}
	if exists(t, pool, outer) || exists(t, pool, inner) {
		t.Error("an outer failure did not roll back the inner Within's write")
	}
}

func TestAnInnerFailureRollsBackEverything(t *testing.T) {
	pool := pgtest.Pool(t)
	tx := pgxtx.New(pool)
	outer, inner := key(), key()
	err := tx.Within(t.Context(), func(ctx context.Context) error {
		insert(ctx, t, pool, outer)
		return tx.Within(ctx, func(ctx context.Context) error {
			insert(ctx, t, pool, inner)
			return errFailed
		})
	})
	if !errors.Is(err, errFailed) {
		t.Fatalf("Within = %v", err)
	}
	if exists(t, pool, outer) || exists(t, pool, inner) {
		t.Error("an inner failure left a write behind")
	}
}

func TestQuerierIsThePoolOutsideATransaction(t *testing.T) {
	pool := pgtest.Pool(t)
	k := key()
	insert(t.Context(), t, pool, k)
	if !exists(t, pool, k) {
		t.Error("a write outside any transaction is not visible at once")
	}
}

func TestSnapshotSeesOneViewWhileAnotherTransactionCommits(t *testing.T) {
	pool := pgtest.Pool(t)
	k := key()
	count := func(ctx context.Context) int {
		var n int
		if err := pgxtx.Querier(ctx, pool).QueryRow(ctx, `SELECT count(*) FROM note WHERE key = $1`, k).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	err := pgxtx.New(pool).Snapshot(t.Context(), func(ctx context.Context) error {
		before := count(ctx)
		insert(t.Context(), t, pool, k) // committed by another session
		if after := count(ctx); after != before {
			t.Errorf("the snapshot saw %d rows, then %d after another commit", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Snapshot = %v", err)
	}
}

func TestSnapshotIsReadOnly(t *testing.T) {
	pool := pgtest.Pool(t)
	err := pgxtx.New(pool).Snapshot(t.Context(), func(ctx context.Context) error {
		_, err := pgxtx.Querier(ctx, pool).Exec(ctx, `INSERT INTO note (key) VALUES ($1)`, key())
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "25006" {
		t.Errorf("a write in a snapshot = %v, want read_only_sql_transaction", err)
	}
}

func TestMixingWithinAndSnapshotPanics(t *testing.T) {
	pool := pgtest.Pool(t)
	tx := pgxtx.New(pool)
	for name, run := range map[string]func(context.Context) error{
		"snapshot inside within": func(ctx context.Context) error {
			return tx.Within(ctx, func(ctx context.Context) error {
				return tx.Snapshot(ctx, func(context.Context) error { return nil })
			})
		},
		"within inside snapshot": func(ctx context.Context) error {
			return tx.Snapshot(ctx, func(ctx context.Context) error {
				return tx.Within(ctx, func(context.Context) error { return nil })
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			_ = run(t.Context())
		})
	}
}

func TestAConcurrencyFailureIsPassedToTheConfiguredWrapper(t *testing.T) {
	errWrapped := errors.New("wrapped")
	tx := pgxtx.New(pgtest.Pool(t), pgxtx.WithConcurrencyFailure(func(err error) error {
		return errors.Join(errWrapped, err)
	}))
	for _, code := range []string{"40P01", "40001"} {
		failure := &pgconn.PgError{Code: code}
		err := tx.Within(t.Context(), func(context.Context) error { return failure })
		if !errors.Is(err, errWrapped) || !errors.Is(err, failure) {
			t.Errorf("SQLSTATE %s: Within = %v, want it wrapped with its cause", code, err)
		}
	}
	err := tx.Within(t.Context(), func(context.Context) error { return errFailed })
	if errors.Is(err, errWrapped) {
		t.Error("an ordinary failure was wrapped as a concurrency failure")
	}
}

func TestAfterCommitRunsOnceTheOutermostTransactionCommits(t *testing.T) {
	pool := pgtest.Pool(t)
	tx := pgxtx.New(pool)
	var ran []string
	err := tx.Within(t.Context(), func(ctx context.Context) error {
		pgxtx.AfterCommit(ctx, func() { ran = append(ran, "outer") })
		return tx.Within(ctx, func(ctx context.Context) error {
			pgxtx.AfterCommit(ctx, func() { ran = append(ran, "inner") })
			if len(ran) != 0 {
				t.Error("a hook ran before the commit")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "outer,inner" {
		t.Errorf("ran = %v, want both hooks in order after the commit", ran)
	}
}

func TestAfterCommitNeverRunsForARolledBackTransaction(t *testing.T) {
	tx := pgxtx.New(pgtest.Pool(t))
	ran := false
	_ = tx.Within(t.Context(), func(ctx context.Context) error {
		pgxtx.AfterCommit(ctx, func() { ran = true })
		return errors.New("fail")
	})
	if ran {
		t.Error("a hook ran for a transaction rolled back")
	}
}

func TestAfterCommitOutsideATransactionRunsAtOnce(t *testing.T) {
	ran := false
	pgxtx.AfterCommit(t.Context(), func() { ran = true })
	if !ran {
		t.Error("a hook outside a transaction did not run at once")
	}
}

func TestLocalKeepsAValueForTheTransactionsLife(t *testing.T) {
	pool := pgtest.Pool(t)
	tx := pgxtx.New(pool)
	type seen struct{}
	fresh := func() map[string]bool { return map[string]bool{} }
	for range 2 {
		err := tx.Within(t.Context(), func(ctx context.Context) error {
			first, ok := pgxtx.Local(ctx, seen{}, fresh)
			if !ok || len(first) != 0 {
				t.Errorf("a new transaction's value = %v, %v, want an empty one kept", first, ok)
			}
			first["a"] = true
			return tx.Within(ctx, func(ctx context.Context) error {
				if again, _ := pgxtx.Local(ctx, seen{}, fresh); !again["a"] {
					t.Error("a joined transaction does not see the value kept earlier in it")
				}
				return nil
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if v, ok := pgxtx.Local(t.Context(), seen{}, fresh); ok || len(v) != 0 {
		t.Errorf("outside a transaction = %v, %v, want a fresh value not kept", v, ok)
	}
}
