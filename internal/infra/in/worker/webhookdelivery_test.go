package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
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
	return appgovernance.Claim{Deliveries: []governance.DeliveryID{{}}}, nil
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

	got, err := testutil.GatherAndCount(reg, "flagnetic_webhook_deliveries_total")
	if err != nil || got != 1 {
		t.Errorf("flagnetic_webhook_deliveries_total series = %d (%v), want the failed delivery counted", got, err)
	}
	if len(logs.Find("webhook delivery attempt failed")) != 1 {
		t.Error("the failure after the attempt was not logged")
	}
	if len(logs.Find("webhook subscription disabled for failing persistently")) != 1 {
		t.Error("the disable was not logged")
	}
}
