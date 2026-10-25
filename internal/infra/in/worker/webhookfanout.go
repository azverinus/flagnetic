package worker

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// WebhookFanOutName names the worker turning outbox events into webhook
// deliveries in logs, metrics and Tick.
const WebhookFanOutName = "webhook_fanout"

// webhookFanOutInterval is the time between rounds of the fan-out: an event
// waits about this long before its deliveries exist.
const webhookFanOutInterval = 5 * time.Second

// FanOut is the use case turning outbox events into deliveries.
type FanOut interface {
	Handle(ctx context.Context) (appgovernance.FanOutReport, error)
}

// WebhookFanOut returns the worker fanning the outbox out to the
// subscriptions wanting its events, every five seconds. Replicas running it
// at once claim different events. After each round it sets
// flagnetic_webhook_outbox_depth, registered on reg, to the events still
// waiting, so a backlog shows.
func WebhookFanOut(f FanOut, reg prometheus.Registerer) Worker {
	depth := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metrics.Namespace, Subsystem: "webhook", Name: "outbox_depth",
		Help: "Events waiting in the outbox to be fanned out to webhook subscriptions.",
	})
	reg.MustRegister(depth)
	return Worker{
		Name: WebhookFanOutName, Interval: webhookFanOutInterval,
		// Each batch is a transaction of its own, so a round cut short
		// leaves the rest to the next.
		Timeout: time.Minute,
		Round: func(ctx context.Context) error {
			report, err := f.Handle(ctx)
			if err == nil {
				depth.Set(float64(report.Depth))
			}
			return err
		},
	}
}
