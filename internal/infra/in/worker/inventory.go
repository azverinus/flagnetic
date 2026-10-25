package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// SDKInventoryName names the worker writing the SDK inventory in logs,
// metrics and Tick.
const SDKInventoryName = "sdk_inventory"

// SDKInventory returns the worker writing the SDKs the replica saw making
// requests, once a minute: a row is written at most that often anyway, and
// no request waits on the write. The sightings the round dropped, because
// the replica held too many, their key held as many rows as it may, or
// writing them failed, are counted in flagnetic_sdk_inventory_dropped_total,
// registered on reg, and logged as one warning with their count.
func SDKInventory(i *appdelivery.SDKInventory, log *slog.Logger, reg prometheus.Registerer) Worker {
	dropped := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "sdk_inventory", Name: "dropped_total",
		Help: "Sightings of SDKs not recorded in the SDK inventory, by reason: queue_full, key_full, write_failed.",
	}, []string{"reason"})
	reg.MustRegister(dropped)
	for _, reason := range []string{"queue_full", "key_full", "write_failed"} {
		dropped.WithLabelValues(reason)
	}
	return Worker{Name: SDKInventoryName, Interval: time.Minute, Round: func(ctx context.Context) error {
		err := i.Flush(ctx)
		if n := i.Dropped(); n.Total() > 0 {
			dropped.WithLabelValues("queue_full").Add(float64(n.QueueFull))
			dropped.WithLabelValues("key_full").Add(float64(n.KeyFull))
			dropped.WithLabelValues("write_failed").Add(float64(n.WriteFailed))
			log.WarnContext(ctx, "SDK inventory sightings dropped", "dropped", n.Total(),
				"queue_full", n.QueueFull, "key_full", n.KeyFull, "write_failed", n.WriteFailed)
		}
		return err
	}}
}
