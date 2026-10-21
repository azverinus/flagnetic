package worker

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// AuthRetentionName names the auth retention worker in logs, metrics and
// Tick.
const AuthRetentionName = "auth_retention"

// AuthRetention returns the worker purging ended sessions and failed
// sign-ins that no longer count, once an hour: nothing waits on the purge,
// which only keeps the tables small. Its metric
// flagnetic_auth_purged_rows_total, registered on reg, counts the rows each
// kind lost.
func AuthRetention(h *appaccess.PurgeAuthRecords, reg prometheus.Registerer) Worker {
	purged := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "auth", Name: "purged_rows_total",
		Help: "Rows the auth retention worker deleted: ended sessions, email failures and source failures.",
	}, []string{"kind"})
	reg.MustRegister(purged)
	for _, kind := range []string{"sessions", "email_failures", "source_failures"} {
		purged.WithLabelValues(kind)
	}
	return Worker{
		Name: AuthRetentionName, Interval: time.Hour,
		// A backlog left by a long outage is purged a bounded slice per
		// round; the next round goes on where this one stopped.
		Timeout: 5 * time.Minute,
		Round: func(ctx context.Context) error {
			n, err := h.Handle(ctx)
			purged.WithLabelValues("sessions").Add(float64(n.Sessions))
			purged.WithLabelValues("email_failures").Add(float64(n.EmailFailures))
			purged.WithLabelValues("source_failures").Add(float64(n.SourceFailures))
			return err
		},
	}
}
