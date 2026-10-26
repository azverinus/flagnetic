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

// attemptsAtOnce is how many attempts one replica makes at once. What a
// receiver sees is bounded per subscription by the claim; this keeps one
// replica from opening a hundred connections in one round.
const attemptsAtOnce = 16

// deliveryRoundFor is how long a round goes on claiming while deliveries are
// due; the attempts it started then finish within their timeout.
const deliveryRoundFor = time.Minute

// Deliverer is the use case attempting the deliveries that are due.
type Deliverer interface {
	Claim(ctx context.Context) (appgovernance.Claim, error)
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
	reg.MustRegister(settled)
	attempt := func(ctx context.Context, token appgovernance.LeaseToken, delivery governance.DeliveryID) {
		done, err := d.Attempt(ctx, token, delivery)
		switch {
		case errors.Is(err, appgovernance.ErrLeaseLost):
			// Another replica took the delivery while this one waited on
			// the receiver; its outcome stands, and the receiver
			// deduplicates on the event id.
			log.InfoContext(ctx, "webhook delivery attempt not recorded: the lease was lost",
				"delivery", delivery.String())
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "webhook delivery attempt failed", "delivery", delivery.String(), "error", err.Error())
		case err == nil && done.Status != "" && done.Status != governance.DeliveryPending:
			settled.WithLabelValues(string(done.Status)).Inc()
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
			var attempts sync.WaitGroup
			defer attempts.Wait()
			for ctx.Err() == nil && time.Now().Before(until) {
				c, err := d.Claim(ctx)
				if err != nil || len(c.Deliveries) == 0 {
					return err
				}
				for _, delivery := range c.Deliveries {
					select {
					case slots <- struct{}{}:
					case <-ctx.Done():
						return ctx.Err()
					}
					attempts.Go(func() {
						defer func() { <-slots }()
						_ = safego.Call(ctx, WebhookDeliveryName, log, func(ctx context.Context) error {
							attempt(ctx, c.Token, delivery)
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
