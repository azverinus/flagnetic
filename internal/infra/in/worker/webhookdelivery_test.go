package worker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// disablingDeliverer hands out one delivery whose failed attempt is recorded
// and disables its subscription, after which telling those who manage it
// fails.
type disablingDeliverer struct{ claimed bool }

func (d *disablingDeliverer) Claim(context.Context) (appgovernance.Claim, error) {
	if d.claimed {
		return appgovernance.Claim{}, nil
	}
	d.claimed = true
	return appgovernance.Claim{Deliveries: []appgovernance.ClaimedDelivery{{}}}, nil
}

func (d *disablingDeliverer) Attempt(context.Context, appgovernance.LeaseToken, governance.DeliveryID,
) (appgovernance.Attempted, error) {
	return appgovernance.Attempted{Status: governance.DeliveryFailed, Disabled: true},
		errors.New("telling those who manage the disabled webhook subscription: the database is gone")
}

// A failure after the attempt was recorded leaves it settled: the delivery
// is counted, the disable reported and the failure logged.
func TestAnAttemptRecordedIsCountedWhenWhatFollowedItFailed(t *testing.T) {
	reg := prometheus.NewRegistry()
	logs := testkit.NewLogSink()
	w := worker.WebhookDelivery(&disablingDeliverer{}, time.Minute, logs.Logger(), reg)

	if err := w.Round(t.Context()); err != nil {
		t.Fatal(err)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, f := range families {
		if f.GetName() == "flagnetic_webhook_deliveries_total" {
			got = len(f.GetMetric())
		}
	}
	if got != 1 {
		t.Errorf("flagnetic_webhook_deliveries_total series = %d, want the failed delivery counted", got)
	}
	if len(logs.Find("webhook delivery attempt failed")) != 1 {
		t.Error("the failure after the attempt was not logged")
	}
	if len(logs.Find("webhook subscription disabled for failing persistently")) != 1 {
		t.Error("the disable was not logged")
	}
}

// busyDeliverer hands out deliveries of two projects in one claim, and holds
// every attempt until released, counting how many of each project run at
// once.
type busyDeliverer struct {
	mu      sync.Mutex
	claimed bool
	deliver []appgovernance.ClaimedDelivery
	running map[workspace.ProjectID]int
	peak    map[workspace.ProjectID]int
	release chan struct{}
	started chan workspace.ProjectID
}

func (d *busyDeliverer) Claim(context.Context) (appgovernance.Claim, error) {
	if d.claimed {
		return appgovernance.Claim{}, nil
	}
	d.claimed = true
	return appgovernance.Claim{Deliveries: d.deliver}, nil
}

func (d *busyDeliverer) Attempt(_ context.Context, _ appgovernance.LeaseToken, delivery governance.DeliveryID,
) (appgovernance.Attempted, error) {
	var project workspace.ProjectID
	for _, c := range d.deliver {
		if c.ID == delivery {
			project = c.Project
		}
	}
	d.mu.Lock()
	d.running[project]++
	d.peak[project] = max(d.peak[project], d.running[project])
	d.mu.Unlock()
	d.started <- project
	<-d.release
	d.mu.Lock()
	d.running[project]--
	d.mu.Unlock()
	return appgovernance.Attempted{Status: governance.DeliverySucceeded}, nil
}

// One project's slow receivers hold no more than its share of a replica's
// attempts, so another project's delivery starts while they all wait.
func TestOneProjectCannotTakeEveryAttemptOfAReplica(t *testing.T) {
	ids := testkit.IDs(testkit.Clock())
	flooding, other := id.New[workspace.Project](ids), id.New[workspace.Project](ids)
	d := &busyDeliverer{
		running: map[workspace.ProjectID]int{}, peak: map[workspace.ProjectID]int{},
		release: make(chan struct{}), started: make(chan workspace.ProjectID, 64),
	}
	for range 40 {
		d.deliver = append(d.deliver, appgovernance.ClaimedDelivery{ID: id.New[governance.Delivery](ids), Project: flooding})
	}
	d.deliver = append(d.deliver, appgovernance.ClaimedDelivery{ID: id.New[governance.Delivery](ids), Project: other})
	w := worker.WebhookDelivery(d, time.Minute, testkit.NewLogSink().Logger(), prometheus.NewRegistry())

	done := make(chan error, 1)
	go func() { done <- w.Round(t.Context()) }()
	// The other project's delivery starts although the flooding project has
	// more waiting than the replica has slots.
	deadline := time.After(10 * time.Second)
	for sawOther := false; !sawOther; {
		select {
		case p := <-d.started:
			sawOther = p == other
		case <-deadline:
			t.Fatal("the other project's delivery never started")
		}
	}
	time.Sleep(100 * time.Millisecond)
	d.mu.Lock()
	got := d.running[flooding]
	d.mu.Unlock()
	if got > 4 {
		t.Errorf("%d attempts of one project at once, want at most 4", got)
	}
	close(d.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d.peak[flooding] > 4 {
		t.Errorf("peak %d attempts of one project at once, want at most 4", d.peak[flooding])
	}
}
