package worker

import (
	"context"
	"log/slog"
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
)

// RemoteEvaluationCountsName names the worker writing the remote evaluation
// counts in logs, metrics and Tick.
const RemoteEvaluationCountsName = "remote_evaluation_counts"

// RemoteEvaluationCounts returns the worker writing the remote evaluations
// the replica counted, once a minute, as evaluation summaries of the
// server's own. Evaluations the counter had no room for are logged as one
// warning with their count.
func RemoteEvaluationCounts(c *appdelivery.RemoteEvaluationCounts, log *slog.Logger) Worker {
	return Worker{Name: RemoteEvaluationCountsName, Interval: time.Minute, Round: func(ctx context.Context) error {
		dropped, err := c.Flush(ctx)
		if dropped > 0 {
			log.WarnContext(ctx, "remote evaluations not counted", "dropped", dropped)
		}
		return err
	}}
}
