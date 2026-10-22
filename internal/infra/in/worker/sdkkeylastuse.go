package worker

import (
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
)

// SDKKeyLastUseName names the worker writing SDK keys' last use in logs,
// metrics and Tick.
const SDKKeyLastUseName = "sdk_key_last_use"

// SDKKeyLastUse returns the worker writing the uses of SDK keys the replica
// noted, once a minute: a key's last use is recorded at most that often
// anyway, and an SDK request never waits on the write.
func SDKKeyLastUse(uses *appdelivery.SDKKeyUses) Worker {
	return Worker{Name: SDKKeyLastUseName, Interval: time.Minute, Round: uses.Flush}
}
