package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/errors"
)

// NewTransactor returns the transactor of the server's pool. A deadlock or
// serialization failure comes back as unavailable, worth retrying by the
// client: the server does not retry it, because a unit of work fixes its lock
// order instead, and a retry would hide an order that is wrong.
func NewTransactor(pool *pgxpool.Pool) *pgxtx.Transactor {
	return pgxtx.New(pool, pgxtx.WithConcurrencyFailure(func(err error) error {
		return errors.Unavailable("concurrent_change",
			"the database refused the change because of a concurrent one; try again").WithCause(err)
	}))
}

// AfterCommit runs functions once the unit of work their context carries has
// committed, and never if it rolls back.
type AfterCommit struct{}

// AfterCommit runs fn as pgxtx.AfterCommit does, handing it the context
// without the transaction, which has ended by then.
func (AfterCommit) AfterCommit(ctx context.Context, fn func(committed context.Context)) {
	pgxtx.AfterCommit(ctx, func() { fn(pgxtx.Outside(ctx)) })
}
