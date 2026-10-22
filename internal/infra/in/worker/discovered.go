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
// developer at once while no evaluation waits on a write. Sightings dropped
// because the replica held too many are logged as a warning: discovery
// lags, and the next contexts carrying them are seen again.
func DiscoveredAttributes(d *appdelivery.Discoveries, log *slog.Logger) Worker {
	return Worker{Name: DiscoveredAttributesName, Interval: time.Second, Round: func(ctx context.Context) error {
		if n := d.Dropped(); n > 0 {
			log.WarnContext(ctx, "discovered attributes dropped: too many pending", "dropped", n)
		}
		return d.Flush(ctx)
	}}
}
