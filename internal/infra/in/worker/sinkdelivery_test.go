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

func (h *heldSinks) Abandon(context.Context, appsinks.LeaseToken, appsinks.ClaimedSink) error {
	return nil
}

func (h *heldSinks) Claim(context.Context, []sinks.ID, appsinks.ClaimLimit) (appsinks.Claim, error) {
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

// endlessSinks has a new batch of one project's sinks due at every claim until
// stopped, as many as the claim allows, and holds every attempt until
// released. It records what the claims were allowed and what they leased, and
// which claimed sinks were given back unstarted.
type endlessSinks struct {
	ids     *id.Source
	project workspace.ProjectID
	mu      sync.Mutex
	stopped bool
	claimed int
	limits  []appsinks.ClaimLimit
	given   int
	started int
	release chan struct{}
}

func (e *endlessSinks) Claim(_ context.Context, _ []sinks.ID, limit appsinks.ClaimLimit) (appsinks.Claim, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limits = append(e.limits, limit)
	if e.stopped {
		return appsinks.Claim{}, nil
	}
	var c appsinks.Claim
	for range limit.Sinks {
		c.Sinks = append(c.Sinks, appsinks.ClaimedSink{Sink: sinks.Sink{ID: id.New[sinks.Sink](e.ids), Project: e.project}})
	}
	e.claimed += len(c.Sinks)
	return c, nil
}

func (e *endlessSinks) Abandon(context.Context, appsinks.LeaseToken, appsinks.ClaimedSink) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.given++
	return nil
}

func (e *endlessSinks) AttemptSink(context.Context, appsinks.LeaseToken, appsinks.ClaimedSink,
) (map[sinks.EventKind]appsinks.Result, error) {
	e.mu.Lock()
	e.started++
	e.mu.Unlock()
	<-e.release
	return nil, nil
}

// A replica claims no more sinks than it has free slots, and no more of one
// project than a project may run: every sink it claimed and cannot start yet
// is leased away from the other replicas, which would otherwise find nothing
// left to claim.
func TestAReplicaClaimsNoMoreSinksThanItHasFreeSlots(t *testing.T) {
	ids := testkit.IDs(testkit.Clock())
	e := &endlessSinks{ids: ids, project: id.New[workspace.Project](ids), release: make(chan struct{})}
	w := worker.SinkDelivery(e, noRisk{}, time.Minute, testkit.NewLogSink().Logger())

	done := make(chan error, 1)
	go func() { done <- w.Round(t.Context()) }()
	time.Sleep(300 * time.Millisecond)
	e.mu.Lock()
	claimed, limits := e.claimed, append([]appsinks.ClaimLimit(nil), e.limits...)
	e.stopped = true
	e.mu.Unlock()
	close(e.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if claimed > 16 {
		t.Errorf("%d sinks claimed while the replica attempts 16 at once", claimed)
	}
	for _, l := range limits {
		if l.PerProject != 4 {
			t.Errorf("claim allowed %d sinks of one project, want 4", l.PerProject)
		}
	}
}

// A claimed sink that was waiting for a slot when the round was cancelled
// gives its leases back instead of keeping them until they run out.
func TestSinksClaimedButNeverStartedGiveBackTheirLeasesOnCancel(t *testing.T) {
	ids := testkit.IDs(testkit.Clock())
	e := &endlessSinks{ids: ids, project: id.New[workspace.Project](ids), release: make(chan struct{})}
	w := worker.SinkDelivery(e, noRisk{}, time.Minute, testkit.NewLogSink().Logger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Round(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	close(e.release)
	<-done

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.claimed == 0 || e.started == 0 {
		t.Fatalf("claimed %d, started %d: the round did nothing", e.claimed, e.started)
	}
	if got, want := e.given, e.claimed-e.started; got != want {
		t.Errorf("%d claimed sinks given back, want %d (claimed %d, started %d)", got, want, e.claimed, e.started)
	}
}
