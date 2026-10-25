package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// EventRetentionName names the worker keeping the raw events bounded in
// logs, metrics and Tick.
const EventRetentionName = "event_retention"

// Retention is the use case keeping the raw event tables bounded.
type Retention interface {
	Handle(ctx context.Context) (appevents.RetentionReport, error)
}

// Purge is a use case forgetting what is past its own retention.
type Purge interface {
	Handle(ctx context.Context) error
}

// EventRetention returns the worker keeping what SDKs report bounded, every
// hour: r creates the raw event tables' partitions ahead of time, drops those
// past retention and purges the event IDs past the deduplication window,
// then inventory forgets the SDKs not seen for its own retention. Replicas
// running it at once take turns in PostgreSQL. The rows a drop took from a
// rollup that had not read them are counted in
// flagnetic_event_gap_rows_total, registered on reg, and each gap is logged
// as a warning.
func EventRetention(r Retention, inventory Purge, log *slog.Logger, reg prometheus.Registerer) Worker {
	gapRows := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "event", Name: "gap_rows_total",
		Help: "Raw events retention dropped before the stream reading them had read them, by stream.",
	}, []string{"stream"})
	reg.MustRegister(gapRows)
	for _, s := range []events.Stream{events.StreamExposures, events.StreamEvaluationSummaries} {
		gapRows.WithLabelValues(string(s))
	}
	return Worker{
		Name: EventRetentionName, Interval: time.Hour,
		// Each table drops in a transaction of its own and the event IDs go
		// in bounded batches, so a round cut short leaves the rest to the
		// next.
		Timeout: 5 * time.Minute,
		Round: func(ctx context.Context) error {
			report, err := r.Handle(ctx)
			for _, g := range report.Gaps {
				gapRows.WithLabelValues(string(g.Stream)).Add(float64(g.Rows))
				log.WarnContext(ctx, "raw events dropped before a stream read them", "stream", string(g.Stream),
					"event_type", string(g.EventType), "rows", g.Rows, "received_from", g.PeriodStart, "received_to", g.PeriodEnd)
			}
			return errors.Join(err, inventory.Handle(ctx))
		},
	}
}
