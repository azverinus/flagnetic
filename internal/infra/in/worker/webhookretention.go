package worker

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// WebhookRetentionName names the webhook retention worker in logs, metrics
// and Tick.
const WebhookRetentionName = "webhook_retention"

// WebhookRetention returns the worker pruning webhook delivery logs and
// expired previous secrets, once an hour: nothing waits on it, it only keeps
// the logs bounded. Its metric flagnetic_webhook_pruned_total, registered on
// reg, counts what each kind lost.
func WebhookRetention(h *appgovernance.RunWebhookRetention, reg prometheus.Registerer) Worker {
	pruned := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "webhook", Name: "pruned_total",
		Help: "What the webhook retention worker removed: deliveries past retention and previous secrets past their overlap.",
	}, []string{"kind"})
	reg.MustRegister(pruned)
	for _, kind := range []string{"deliveries", "secrets"} {
		pruned.WithLabelValues(kind)
	}
	return Worker{
		Name: WebhookRetentionName, Interval: time.Hour,
		// A backlog is pruned a batch per transaction; the next round goes
		// on where this one stopped.
		Timeout: 5 * time.Minute,
		Round: func(ctx context.Context) error {
			n, err := h.Handle(ctx)
			pruned.WithLabelValues("deliveries").Add(float64(n.Deliveries))
			pruned.WithLabelValues("secrets").Add(float64(n.Secrets))
			return err
		},
	}
}
