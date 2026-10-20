package pgxtx_test

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

// transactor is how an application handler declares the unit of work it owns:
// one method, no driver type, satisfied by *pgxtx.Transactor.
type transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

var _ transactor = (*pgxtx.Transactor)(nil)

// renameNote is a repository method: it runs on whatever transaction its
// caller began, or on the pool outside one.
func renameNote(ctx context.Context, pool *pgxpool.Pool, from, to string) error {
	_, err := pgxtx.Querier(ctx, pool).Exec(ctx, `UPDATE note SET key = $2 WHERE key = $1`, from, to)
	return err
}

// A handler owns the transaction; the repositories it calls take part in it.
func ExampleTransactor_Within() {
	var pool *pgxpool.Pool // the server's pool
	var tx transactor = pgxtx.New(pool)

	_ = tx.Within(context.Background(), func(ctx context.Context) error {
		if err := renameNote(ctx, pool, "draft", "final"); err != nil {
			return err // rolls back both renames
		}
		return renameNote(ctx, pool, "final-old", "archive")
	})
}
