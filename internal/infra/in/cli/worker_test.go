package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestAWorkerOfServeStopsBeforeThePoolCloses(t *testing.T) {
	var started, stoppedAt atomic.Int64
	w := cli.WithWorker(worker.Worker{Name: "test", Interval: time.Hour, Round: func(ctx context.Context) error {
		started.Store(1)
		<-ctx.Done()
		stoppedAt.Store(time.Now().UnixNano())
		return ctx.Err()
	}})
	r := serve(t, map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)}, w)
	if !waitFor(func() bool { return started.Load() == 1 }) {
		t.Fatal("the worker's first round did not run at once")
	}
	if code, _ := r.stop(); code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, r.stderr.String())
	}

	var poolClosed time.Time
	sc := bufio.NewScanner(strings.NewReader(r.stderr.String()))
	for sc.Scan() {
		var rec struct {
			Time time.Time
			Msg  string
		}
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Msg == "database pool closed" {
			poolClosed = rec.Time
		}
	}
	if poolClosed.IsZero() || stoppedAt.Load() == 0 {
		t.Fatalf("pool closed at %v, worker stopped at %d:\n%s", poolClosed, stoppedAt.Load(), r.stderr.String())
	}
	if stopped := time.Unix(0, stoppedAt.Load()); !stopped.Before(poolClosed) {
		t.Errorf("the worker stopped at %v, not before the pool closed at %v", stopped, poolClosed)
	}
}
