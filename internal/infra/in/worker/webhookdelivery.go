package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// WebhookDeliveryName names the worker sending webhook deliveries in logs,
// metrics and Tick.
const WebhookDeliveryName = "webhook_delivery"

// attemptsAtOnce is how many attempts one replica makes at once, and how
// many deliveries it claims at once. What a receiver sees is bounded per
// subscription by the claim; this keeps one replica from opening a hundred
// connections in one round. A claim takes no more than that because it has
// to attempt every delivery it leases before the lease runs out: with all of
// them one project's, attemptsPerProject at a time and each taking the full
// timeout, that is attemptsAtOnce/attemptsPerProject timeouts, which
// appgovernance.LeaseFor must cover, or the deliveries that wait lose their
// lease and keep every other replica from taking them.
const attemptsAtOnce = 16

// deliveryRoundFor is how long a round goes on claiming while deliveries are
// due; the attempts it started then finish within their timeout.
const deliveryRoundFor = time.Minute

// Deliverer is the use case attempting the deliveries that are due.
type Deliverer interface {
	Claim(ctx context.Context, limit int) (appgovernance.Claim, error)
	Attempt(ctx context.Context, token appgovernance.LeaseToken, delivery governance.DeliveryID) (appgovernance.Attempted, error)
}

// WebhookDelivery returns the worker sending the webhook deliveries that are
// due, every interval. A round claims a batch, attempts it attemptsAtOnce at
// a time and claims again until nothing is due, so a round driven by Tick
// returns with every due delivery attempted. Replicas running it at once
// lease different deliveries. It counts every delivery that settles in
// flagnetic_webhook_deliveries_total by status, registered on reg, and logs
// an attempt whose outcome went unrecorded: the delivery is attempted again
// once its lease runs out.
func WebhookDelivery(d Deliverer, interval time.Duration, log *slog.Logger, reg prometheus.Registerer) Worker {
	settled := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "webhook", Name: "deliveries_total",
		Help: "Webhook deliveries settled, by status: succeeded, failed or exhausted.",
	}, []string{"status"})
	skipped := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "webhook", Name: "attempts_skipped_total",
		Help: "Webhook delivery attempts skipped because their lease was no longer held when their turn came.",
	})
	reg.MustRegister(settled, skipped)
	attempt := func(ctx context.Context, token appgovernance.LeaseToken, delivery governance.DeliveryID) {
		done, err := d.Attempt(ctx, token, delivery)
		switch {
		case done.Skipped:
			// The lease no longer held when the attempt came to it; nothing
			// was sent, and the delivery is another replica's or due again.
			// A delivery that settled, or whose subscription was disabled or
			// deleted meanwhile, is not attempted either but is not counted:
			// the count is of claims outrunning their lease.
			skipped.Inc()
			log.InfoContext(ctx, "webhook delivery attempt skipped: its lease was no longer held",
				"delivery", delivery.String())
		case errors.Is(err, appgovernance.ErrLeaseLost):
			// Another replica took the delivery while this one waited on
			// the receiver; its outcome stands, and the receiver
			// deduplicates on the event id.
			log.InfoContext(ctx, "webhook delivery attempt not recorded: the lease was lost",
				"delivery", delivery.String())
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "webhook delivery attempt failed", "delivery", delivery.String(), "error", err.Error())
		}
		// A status is reported only once the attempt is recorded, so the
		// delivery is counted even when what followed failed, such as
		// telling those who manage the subscription it made the platform
		// disable.
		if done.Status != "" && done.Status != governance.DeliveryPending {
			settled.WithLabelValues(string(done.Status)).Inc()
		}
		if done.Disabled {
			log.WarnContext(ctx, "webhook subscription disabled for failing persistently",
				"webhook", done.Webhook.String(), "delivery", delivery.String())
		}
	}
	return Worker{
		Name: WebhookDeliveryName, Interval: interval,
		// The claiming stops after deliveryRoundFor; the attempts started
		// by then finish within their own timeout.
		Timeout: deliveryRoundFor + 2*governance.DeliveryTimeout,
		Round: func(ctx context.Context) error {
			until := time.Now().Add(deliveryRoundFor)
			slots := make(chan struct{}, attemptsAtOnce)
			perProject := newProjectSlots(attemptsPerProject)
			var attempts sync.WaitGroup
			defer attempts.Wait()
			for ctx.Err() == nil && time.Now().Before(until) {
				c, err := d.Claim(ctx, attemptsAtOnce)
				if err != nil || len(c.Deliveries) == 0 {
					return err
				}
				for _, claimed := range c.Deliveries {
					// Each attempt waits for its project's slot, then the
					// replica's, in a goroutine of its own, so a project at its
					// allowance holds back its own deliveries and no other's.
					attempts.Go(func() {
						release, err := perProject.acquire(ctx, claimed.Project)
						if err != nil {
							return
						}
						defer release()
						select {
						case slots <- struct{}{}:
						case <-ctx.Done():
							return
						}
						defer func() { <-slots }()
						_ = safego.Call(ctx, WebhookDeliveryName, log, func(ctx context.Context) error {
							attempt(ctx, c.Token, claimed.ID)
							return nil
						})
					})
				}
				// A subscription at its limit has more due than one claim
				// takes; the next claim finds them once these settle.
				attempts.Wait()
			}
			return ctx.Err()
		},
	}
}
