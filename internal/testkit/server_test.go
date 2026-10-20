package testkit_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestWorkersOfTheInProcessServerRunOnlyWhenTicked(t *testing.T) {
	rounds := 0
	srv := testkit.StartServer(t, testkit.WithWorker(worker.Worker{Name: "rollup", Interval: time.Millisecond, Round: func(context.Context) error {
		rounds++
		return nil
	}}))
	time.Sleep(20 * time.Millisecond)
	if rounds != 0 {
		t.Fatalf("%d rounds ran on their own", rounds)
	}
	if err := srv.Tick(t, "rollup"); err != nil || rounds != 1 {
		t.Errorf("Tick = %v after %d rounds, want one round", err, rounds)
	}
	if got := testkit.Metric(t, srv.Scrape(t), `flagnetic_worker_rounds_total{worker="rollup"}`); got != 1 {
		t.Errorf("flagnetic_worker_rounds_total = %v, want the round counted on the server's registry", got)
	}
}
