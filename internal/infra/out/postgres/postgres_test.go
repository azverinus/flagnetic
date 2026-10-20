package postgres_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestADeadlockComesBackUnavailable(t *testing.T) {
	pool := testkit.Pool(t)
	tx := postgres.NewTransactor(pool)

	// Two units of work take the same two locks in opposite orders, each
	// waiting until the other holds its first: PostgreSQL breaks the cycle by
	// failing one of them.
	var holding sync.WaitGroup
	holding.Add(2)
	lock := func(ctx context.Context, key int) error {
		_, err := pgxtx.Querier(ctx, pool).Exec(ctx, `SELECT pg_advisory_xact_lock(4242, $1)`, key)
		return err
	}
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i, order := range [][2]int{{1, 2}, {2, 1}} {
		wg.Go(func() {
			results[i] = tx.Within(t.Context(), func(ctx context.Context) error {
				if err := lock(ctx, order[0]); err != nil {
					holding.Done()
					return err
				}
				holding.Done()
				holding.Wait()
				return lock(ctx, order[1])
			})
		})
	}
	wg.Wait()

	unavailable := 0
	for _, err := range results {
		if errors.IsKind(err, errors.KindUnavailable) {
			unavailable++
		} else if err != nil {
			t.Errorf("Within = %v, want nil or unavailable", err)
		}
	}
	if unavailable != 1 {
		t.Errorf("%d units of work came back unavailable, want exactly the one PostgreSQL failed: %v", unavailable, results)
	}
}
