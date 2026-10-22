package delivery

import (
	"context"
	"sync"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// SummaryStore stores the rows of evaluation summaries.
type SummaryStore interface {
	SaveSummaries(ctx context.Context, rows []delivery.SummaryRecord) error
}

// RemoteEvaluationCounts counts the remote evaluations a replica answers and
// writes them in rounds as evaluation summaries of the server's own, so
// reports include the evaluations of apps that hold no ruleset, while no
// evaluation waits on a write.
type RemoteEvaluationCounts struct {
	store SummaryStore
	clock clock.Clock

	mu     sync.Mutex
	counts delivery.RemoteCounts
}

// NewRemoteEvaluationCounts returns the replica's counter, writing to store.
func NewRemoteEvaluationCounts(store SummaryStore, c clock.Clock) *RemoteEvaluationCounts {
	return &RemoteEvaluationCounts{store: store, clock: c}
}

// Add counts the results of one remote evaluation, by their true reason.
func (c *RemoteEvaluationCounts) Add(e delivery.RemoteEvaluator, rulesetVersion int64, results map[string]eval.Result) {
	if c == nil {
		return
	}
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.Add(e, rulesetVersion, results, now)
}

// Flush writes what was counted since the last round and returns how many
// evaluations went uncounted meanwhile because the counter was full. Counts
// that cannot be written are dropped rather than retried, so a database
// that is down never grows a replica's memory; it returns the failure.
func (c *RemoteEvaluationCounts) Flush(ctx context.Context) (int, error) {
	c.mu.Lock()
	counts := c.counts
	c.counts = delivery.RemoteCounts{}
	c.mu.Unlock()
	if counts.Len() == 0 {
		return counts.Dropped, nil
	}
	return counts.Dropped, c.store.SaveSummaries(ctx, counts.Records(c.clock.Now()))
}
