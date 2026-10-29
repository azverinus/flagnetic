package access_test

import (
	"context"
	"crypto/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// waitForLockWait returns once a statement counting a source's failure in
// pool's database waits on a lock another transaction holds.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (SELECT FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%sign_in_source_failures%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no reservation came to wait on the source's row")
}

func TestAReservationBegunBeforeTheLockWasFirstCountedStillMeetsIt(t *testing.T) {
	pool := testkit.Pool(t)
	store, tx := pgaccess.NewStore(pool), postgres.NewTransactor(pool)
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[0], b[1] = 0x20, 0x01
	attempt := access.NewAttempt("nobody-"+strings.ToLower(rand.Text())+"@example.com", netip.AddrFrom16(b), testkit.Start)

	// One transaction counts the email's first failures up to its lock, and
	// holds the source's row until told to commit.
	locked, commit, committed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		committed <- tx.Within(context.Background(), func(ctx context.Context) error {
			for range access.LockoutThreshold {
				if _, err := store.ReserveAttempt(ctx, attempt); err != nil {
					return err
				}
			}
			close(locked)
			<-commit
			return nil
		})
	}()
	<-locked

	// Another begins meanwhile, before the email's failures exist for it,
	// and waits on the source's row.
	type reserved struct {
		r   access.Reservation
		err error
	}
	second := make(chan reserved, 1)
	go func() {
		var got reserved
		got.err = tx.Within(context.Background(), func(ctx context.Context) error {
			var err error
			got.r, err = store.ReserveAttempt(ctx, attempt)
			return err
		})
		second <- got
	}()
	waitForLockWait(t, pool)
	close(commit)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	got := <-second
	if got.err != nil {
		t.Fatal(got.err)
	}
	e, ok := apperrors.From(got.r.Refusal())
	if got.r.Failures != 0 || !got.r.LockedUntil.Equal(testkit.Start.Add(time.Minute)) || !ok || e.Code != "locked" {
		t.Errorf("the reservation that began before the lock: %+v refused %v, want it uncounted and locked for a minute",
			got.r, got.r.Refusal())
	}
}
