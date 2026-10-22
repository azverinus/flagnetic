package worker

import (
	"context"
	"log/slog"
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
)

// DiscoveredAttributesName names the worker writing discovered attributes in
// logs, metrics and Tick.
const DiscoveredAttributesName = "discovered_attributes"

// DiscoveredAttributes returns the worker writing the undeclared attributes
// the replica's evaluations noted, every second, so they show up for a
// developer at once while no evaluation waits on a write. The sightings the
// round dropped, because the replica held too many or because writing them
// failed, are logged as one warning with their count: discovery lags, and
// the next contexts carrying them are seen again.
func DiscoveredAttributes(d *appdelivery.Discoveries, log *slog.Logger) Worker {
	return Worker{Name: DiscoveredAttributesName, Interval: time.Second, Round: func(ctx context.Context) error {
		err := d.Flush(ctx)
		if n := d.Dropped(); n.Total() > 0 {
			log.WarnContext(ctx, "discovered attributes dropped", "dropped", n.Total(),
				"queue_full", n.QueueFull, "project_full", n.ProjectFull, "write_failed", n.WriteFailed)
		}
		return err
	}}
}
