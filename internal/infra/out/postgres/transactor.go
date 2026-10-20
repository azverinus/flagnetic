package postgres

import (
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
