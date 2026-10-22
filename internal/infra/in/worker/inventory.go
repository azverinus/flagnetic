package worker

import (
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
)

// Names of the SDK inventory's workers in logs, metrics and Tick.
const (
	SDKInventoryName          = "sdk_inventory"
	SDKInventoryRetentionName = "sdk_inventory_retention"
)

// SDKInventory returns the worker writing the SDKs the replica saw making
// requests, once a minute: a row is written at most that often anyway, and
// no request waits on the write.
func SDKInventory(i *appdelivery.SDKInventory) Worker {
	return Worker{Name: SDKInventoryName, Interval: time.Minute, Round: i.Flush}
}

// SDKInventoryRetention returns the worker forgetting the SDKs not seen for
// the retention, every hour: a row past it by an hour more costs nothing.
func SDKInventoryRetention(p *appdelivery.PurgeSDKInventory) Worker {
	return Worker{Name: SDKInventoryRetentionName, Interval: time.Hour, Timeout: 5 * time.Minute, Round: p.Handle}
}
