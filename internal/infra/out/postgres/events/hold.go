package events

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

// IngestHold keeps the transaction of a batch of events open once its rows
// are written, until the test holding it lets it commit: what shows that a
// transaction still open holds the rollups back and is counted once it
// commits. A replica runs without one in production.
type IngestHold struct {
	mu   sync.Mutex
	next *heldIngest
}

type heldIngest struct {
	written chan string
	release chan struct{}
	once    sync.Once
}

// NewIngestHold returns a hold holding nothing until HoldNext.
func NewIngestHold() *IngestHold { return &IngestHold{} }

// HoldNext holds the next batch ingested: written receives the ID of its
// transaction once its rows are written, and release lets it commit. Release
// may be called more than once, and before the batch arrives, which then
// holds nothing.
func (h *IngestHold) HoldNext() (written <-chan string, release func()) {
	x := &heldIngest{written: make(chan string, 1), release: make(chan struct{})}
	h.mu.Lock()
	h.next = x
	h.mu.Unlock()
	return x.written, func() {
		x.once.Do(func() { close(x.release) })
		h.mu.Lock()
		if h.next == x {
			h.next = nil
		}
		h.mu.Unlock()
	}
}

// Wrap returns tx holding the units of work h is asked to hold, pool being
// what tx begins them on.
func (h *IngestHold) Wrap(tx appevents.Transactor, pool *pgxpool.Pool) appevents.Transactor {
	return heldTransactor{hold: h, tx: tx, pool: pool}
}

type heldTransactor struct {
	hold *IngestHold
	tx   appevents.Transactor
	pool *pgxpool.Pool
}

func (t heldTransactor) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.tx.Within(ctx, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		t.hold.mu.Lock()
		x := t.hold.next
		t.hold.next = nil
		t.hold.mu.Unlock()
		if x == nil {
			return nil
		}
		var xid string
		if err := pgxtx.Querier(ctx, t.pool).QueryRow(ctx, `SELECT pg_current_xact_id()::text`).Scan(&xid); err != nil {
			return fmt.Errorf("reading the held transaction's ID: %w", err)
		}
		x.written <- xid
		select {
		case <-x.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}
