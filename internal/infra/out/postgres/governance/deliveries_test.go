package governance_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// pendingDeliveries stores an enabled instance subscription with n
// deliveries due at testkit.Start, and returns them.
func pendingDeliveries(t *testing.T, pool *pgxpool.Pool, ids *id.Source, n int) []governance.Delivery {
	t.Helper()
	w := governance.Webhook{
		ID: id.New[governance.Webhook](ids), SealedSecret: "sealed", EnabledAt: testkit.Start, CreatedAt: testkit.Start,
		UpdatedAt: testkit.Start, Version: 1,
		WebhookInput: governance.WebhookInput{Name: "archive", URL: "https://receiver.example/hook",
			Events: []string{governance.EventAuditEntry}, Environments: []string{}, Enabled: true},
	}
	if err := pggovernance.NewWebhooks(pool).CreateWebhook(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	out := make([]governance.Delivery, n)
	for i := range out {
		out[i] = governance.NewDelivery(id.New[governance.Delivery](ids), w.ID, governance.QueuedEvent{
			ID: id.New[governance.Event](ids), Type: governance.EventAuditEntry, Envelope: []byte(`{}`),
		}, testkit.Start)
	}
	if err := pggovernance.NewOutbox(pool).CreateDeliveries(t.Context(), out); err != nil {
		t.Fatal(err)
	}
	return out
}

func claim(t *testing.T, pool *pgxpool.Pool, s *pggovernance.Deliveries, token appgovernance.LeaseToken, now time.Time,
) []governance.DeliveryID {
	t.Helper()
	var leased []governance.DeliveryID
	err := postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
		var err error
		leased, err = s.ClaimDeliveries(ctx, token, now, now.Add(time.Minute), 100)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return leased
}

// A lease is the token's while it runs: no more than four of a
// subscription's deliveries are leased at once, another token neither
// renews nor records, and a lease that ran out is not renewed but leased
// again.
func TestADeliveryLeaseIsTheTokensWhileItRuns(t *testing.T) {
	pool := testkit.FreshPool(t)
	ids := testkit.IDs(testkit.Clock())
	s := pggovernance.NewDeliveries(pool)
	pendingDeliveries(t, pool, ids, 6)
	first, second := id.New[appgovernance.Lease](ids), id.New[appgovernance.Lease](ids)
	now := testkit.Start.Add(time.Second)

	leased := claim(t, pool, s, first, now)
	if len(leased) != governance.MaxDeliveriesInFlight {
		t.Fatalf("leased %d deliveries of one subscription, want %d", len(leased), governance.MaxDeliveriesInFlight)
	}
	if more := claim(t, pool, s, second, now); len(more) != 0 {
		t.Fatalf("a second claim leased %d beside four running leases", len(more))
	}
	d := leased[0]
	if _, held, err := s.RenewLease(t.Context(), d, second, now, now.Add(time.Minute)); err != nil || held {
		t.Errorf("another token renewed the lease: %v, %v", held, err)
	}
	l, held, err := s.RenewLease(t.Context(), d, first, now, now.Add(time.Minute))
	if err != nil || !held || l.Delivery.ID != d || l.URL != "https://receiver.example/hook" || len(l.SealedSecrets) != 1 ||
		len(l.Delivery.Attempts) != 0 {
		t.Fatalf("renew = %+v, %v, %v", l, held, err)
	}
	attempt := governance.DeliveryAttempt{N: 1, At: now, StatusCode: new(500)}
	next := now.Add(time.Hour)
	if recorded, err := s.RecordAttempt(t.Context(), d, second, attempt, governance.DeliveryPending, &next, now); err != nil || recorded {
		t.Errorf("another token recorded an attempt: %v, %v", recorded, err)
	}
	if recorded, err := s.RecordAttempt(t.Context(), d, first, attempt, governance.DeliveryPending, &next, now); err != nil || !recorded {
		t.Errorf("the holder could not record: %v, %v", recorded, err)
	}
	if err := s.CountAttempt(t.Context(), l.Delivery.Webhook, false, now); err != nil {
		t.Fatal(err)
	}

	later := now.Add(2 * time.Minute)
	if _, held, err := s.RenewLease(t.Context(), leased[1], first, later, later.Add(time.Minute)); err != nil || held {
		t.Errorf("a lease that ran out was renewed: %v, %v", held, err)
	}
	// The three leases that ran out and the two never leased are due; the
	// one recorded waits for its retry.
	if again := claim(t, pool, s, second, later); len(again) != governance.MaxDeliveriesInFlight {
		t.Errorf("once the leases ran out a claim leased %d, want %d", len(again), governance.MaxDeliveriesInFlight)
	}
}

func TestADeliveryStoreThatCannotReachPostgreSQLFails(t *testing.T) {
	pool := testkit.FreshPool(t)
	ids := testkit.IDs(testkit.Clock())
	s := pggovernance.NewDeliveries(pool)
	d := pendingDeliveries(t, pool, ids, 1)[0]
	token := id.New[appgovernance.Lease](ids)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	now := testkit.Start
	if _, err := s.ClaimDeliveries(ctx, token, now, now, 1); err == nil {
		t.Error("claim succeeded")
	}
	if _, _, err := s.RenewLease(ctx, d.ID, token, now, now); err == nil {
		t.Error("renew succeeded")
	}
	if err := s.LockWebhook(ctx, d.Webhook); err == nil {
		t.Error("lock succeeded")
	}
	if _, err := s.RecordAttempt(ctx, d.ID, token, governance.DeliveryAttempt{N: 1, At: now}, governance.DeliveryFailed, nil, now); err == nil {
		t.Error("record succeeded")
	}
	if err := s.CountAttempt(ctx, d.Webhook, true, now); err == nil {
		t.Error("count succeeded")
	}
	if _, err := s.TryLockWebhookRetention(ctx); err == nil {
		t.Error("retention lock succeeded")
	}
	if _, err := s.PruneDeliveries(ctx, now, 1); err == nil {
		t.Error("prune succeeded")
	}
	if _, err := s.ForgetExpiredSecrets(ctx, now); err == nil {
		t.Error("forgetting expired secrets succeeded")
	}
}
