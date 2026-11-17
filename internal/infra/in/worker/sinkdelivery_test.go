package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// heldSinks claims sinks of two projects at once and holds every attempt until
// released, counting how many of each project run together.
type heldSinks struct {
	mu      sync.Mutex
	claimed bool
	claim   []appsinks.ClaimedSink
	running map[workspace.ProjectID]int
	peak    map[workspace.ProjectID]int
	release chan struct{}
	started chan workspace.ProjectID
}

func (h *heldSinks) Claim(context.Context, []sinks.ID) (appsinks.Claim, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.claimed {
		return appsinks.Claim{}, nil
	}
	h.claimed = true
	return appsinks.Claim{Sinks: h.claim}, nil
}

func (h *heldSinks) AttemptSink(_ context.Context, _ appsinks.LeaseToken, c appsinks.ClaimedSink,
) (map[sinks.EventKind]appsinks.Result, error) {
	project := c.Sink.Project
	h.mu.Lock()
	h.running[project]++
	h.peak[project] = max(h.peak[project], h.running[project])
	h.mu.Unlock()
	h.started <- project
	<-h.release
	h.mu.Lock()
	h.running[project]--
	h.mu.Unlock()
	return nil, nil
}

type noRisk struct{}

func (noRisk) Handle(context.Context) error { return nil }

// One project's sinks hold no more than its share of a replica's attempts, so
// another project's sink starts while they all wait.
func TestOneProjectsSinksCannotTakeEveryAttemptOfAReplica(t *testing.T) {
	ids := testkit.IDs(testkit.Clock())
	flooding, other := id.New[workspace.Project](ids), id.New[workspace.Project](ids)
	h := &heldSinks{
		running: map[workspace.ProjectID]int{}, peak: map[workspace.ProjectID]int{},
		release: make(chan struct{}), started: make(chan workspace.ProjectID, 64),
	}
	for range 40 {
		h.claim = append(h.claim, appsinks.ClaimedSink{Sink: sinks.Sink{ID: id.New[sinks.Sink](ids), Project: flooding}})
	}
	h.claim = append(h.claim, appsinks.ClaimedSink{Sink: sinks.Sink{ID: id.New[sinks.Sink](ids), Project: other}})
	w := worker.SinkDelivery(h, noRisk{}, time.Minute, testkit.NewLogSink().Logger())

	done := make(chan error, 1)
	go func() { done <- w.Round(t.Context()) }()
	deadline := time.After(10 * time.Second)
	for sawOther := false; !sawOther; {
		select {
		case p := <-h.started:
			sawOther = p == other
		case <-deadline:
			t.Fatal("the other project's sink never started")
		}
	}
	time.Sleep(100 * time.Millisecond)
	h.mu.Lock()
	got := h.running[flooding]
	h.mu.Unlock()
	if got > 4 {
		t.Errorf("%d sinks of one project attempted at once, want at most 4", got)
	}
	close(h.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if h.peak[flooding] > 4 {
		t.Errorf("peak %d sinks of one project at once, want at most 4", h.peak[flooding])
	}
}
