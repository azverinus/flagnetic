package worker

import (
	"context"
	"time"
)

// EventPartitionsName names the worker creating the raw event tables'
// partitions in logs, metrics and Tick.
const EventPartitionsName = "event_partitions"

// EventPartitions returns the worker creating the daily partitions of the
// raw event tables ahead of time, every hour: create makes those of today
// and the days configured after it that are missing. Replicas running it at
// once take turns in PostgreSQL, and a day already created is left as it is.
func EventPartitions(create func(ctx context.Context) error) Worker {
	return Worker{Name: EventPartitionsName, Interval: time.Hour, Timeout: time.Minute, Round: create}
}
