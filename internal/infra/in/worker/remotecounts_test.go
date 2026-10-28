package worker_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/eval"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Evaluations the full counter had no room for are logged once with their
// count, and the round still fails when the database is down. The pool
// connects lazily, so it stands for a database the replica cannot reach.
func TestRemoteEvaluationsNotCountedAreLoggedWithTheirCount(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://flagnetic@127.0.0.1:1/flagnetic?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	counts := appdelivery.NewRemoteEvaluationCounts(pgevents.NewStore(pool), testkit.Clock())
	results := map[string]eval.Result{}
	for i := range delivery.MaxRemoteCountGroups + 3 {
		results[fmt.Sprintf("flag-%d", i)] = eval.Result{Reason: eval.ReasonOff}
	}
	counts.Add(delivery.RemoteEvaluator{SDK: delivery.SDKIdentity{Name: "unknown"}}, 1, results)

	logs := testkit.NewLogSink()
	r := worker.NewRunner(logs.Logger(), prometheus.NewRegistry())
	if err := r.Register(worker.RemoteEvaluationCounts(counts, logs.Logger())); err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(t.Context(), worker.RemoteEvaluationCountsName); err == nil {
		t.Error("a round that could not write its counts succeeded")
	}
	logged := logs.Find("remote evaluations not counted")
	if len(logged) != 1 || fmt.Sprint(logged[0].Attrs["dropped"]) != "3" {
		t.Errorf("logged %v, want one warning of 3 dropped", logged)
	}
}
