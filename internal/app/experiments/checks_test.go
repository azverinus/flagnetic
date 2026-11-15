package experiments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	pgexperiments "github.com/azverinus/flagnetic/internal/infra/out/postgres/experiments"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// failingExposures answers like the events store, except for one experiment
// whose last exposure cannot be read.
type failingExposures struct {
	*pgevents.Store
	failing experiments.ExperimentID
}

var errExposuresUnreadable = errors.New("exposures unreadable")

func (f failingExposures) LastExposureAt(ctx context.Context, env workspace.EnvironmentID, e experiments.ExperimentID,
	after, at time.Time,
) (*time.Time, error) {
	if e == f.failing {
		return nil, errExposuresUnreadable
	}
	return f.Store.LastExposureAt(ctx, env, e, after, at)
}

// An experiment whose checks fail rolls back alone: what its checks stored
// before the failure goes with it, the round still checks the others, and
// the round's error names the one that failed.
func TestAnExperimentWhoseChecksFailRollsBackAlone(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	started := func() management.Project {
		p := setUp(t, admin)
		res, err := admin.StartExperimentWithResponse(t.Context(), p.Key, "title_test",
			&management.StartExperimentParams{IfMatch: ptr(`"1"`)})
		if err != nil || res.JSON200 == nil {
			t.Fatalf("start: %v", err)
		}
		return p
	}
	broken, healthy := started(), started()

	pool := testkit.Pool(t)
	c := clock.System{}
	store, flagStore, eventStore := pgexperiments.NewStore(pool), pgflags.NewStore(pool), pgevents.NewStore(pool)
	failing := id.FromBytes[experiments.Experiment](experimentOf(t, admin, broken.Key).Id)
	err := appexperiments.NewRunChecks(appexperiments.RunChecksDeps{
		Tx: postgres.NewTransactor(pool), Lock: store, Running: store, Pending: store, Final: eventStore, Retention: 30 * 24 * time.Hour, Projects: store, Store: store, Holders: flagStore,
		FirstExposures: eventStore, Exposures: failingExposures{Store: eventStore, failing: failing}, Checks: store,
		Recorder: appexperiments.NewRecorder(testkit.Record(pool), c, id.NewSource(c)),
	}).Handle(t.Context())
	if !errors.Is(err, errExposuresUnreadable) || !strings.Contains(err.Error(), failing.String()) {
		t.Fatalf("round = %v, want the failure of experiment %s", err, failing)
	}

	sampleRatio := func(p management.Project) *management.SampleRatioCheck {
		res, err := admin.GetExperimentExposuresWithResponse(t.Context(), p.Key, "title_test",
			&management.GetExperimentExposuresParams{Environment: "staging"})
		if err != nil || res.JSON200 == nil {
			t.Fatalf("exposures: %v", err)
		}
		return res.JSON200.SampleRatio
	}
	if got := sampleRatio(broken); got != nil {
		t.Errorf("the failed experiment's sample-ratio check = %+v, want it rolled back", got)
	}
	if got := sampleRatio(healthy); got == nil {
		t.Errorf("the other experiment's sample-ratio check is missing, want it checked")
	}
}

// probingExposures answers like the events store, and while the final check
// counts an environment's exposures it takes the project's lock from another
// unit of work, as a write of the project would.
type probingExposures struct {
	*pgevents.Store
	probe  func(ctx context.Context) error
	probed []error
}

func (p *probingExposures) FirstExposureCountsReceivedBy(ctx context.Context, env workspace.EnvironmentID,
	e experiments.ExperimentID, by time.Time,
) (map[string]int64, error) {
	p.probed = append(p.probed, p.probe(ctx))
	return p.Store.FirstExposureCountsReceivedBy(ctx, env, e, by)
}

// The final check counts the stopped experiment's exposures without holding
// its project's lock, so a write of the project goes through meanwhile, and
// stores the counts made once it holds the lock.
func TestTheFinalCheckCountsWithoutHoldingTheProjectsWrites(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := setUp(t, admin)
	if res, err := admin.StartExperimentWithResponse(t.Context(), p.Key, "title_test",
		&management.StartExperimentParams{IfMatch: ptr(`"1"`)}); err != nil || res.JSON200 == nil {
		t.Fatalf("start: %v", err)
	}
	if res, err := admin.StopExperimentWithResponse(t.Context(), p.Key, "title_test",
		&management.StopExperimentParams{IfMatch: ptr(`"2"`)}, management.ExperimentStop{Outcome: "removed"}); err != nil || res.JSON200 == nil {
		t.Fatalf("stop: %v", err)
	}

	pool := testkit.Pool(t)
	c := clock.NewFake(time.Now().Add(2 * time.Minute))
	tx := postgres.NewTransactor(pool)
	store, flagStore := pgexperiments.NewStore(pool), pgflags.NewStore(pool)
	project := id.FromBytes[workspace.Project](p.Id)
	final := &probingExposures{Store: pgevents.NewStore(pool), probe: func(context.Context) error {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		return tx.Within(ctx, func(ctx context.Context) error { return flagStore.LockProject(ctx, project) })
	}}
	err := appexperiments.NewRunChecks(appexperiments.RunChecksDeps{
		Tx: tx, Lock: store, Running: store, Pending: store, Final: final, Retention: 30 * 24 * time.Hour, Projects: store, Store: store, Holders: flagStore,
		FirstExposures: final.Store, Exposures: final.Store, Checks: store,
		Recorder: appexperiments.NewRecorder(testkit.Record(pool), c, id.NewSource(c)),
	}).Handle(t.Context())
	if err != nil {
		t.Fatalf("round: %v", err)
	}
	if len(final.probed) == 0 {
		t.Fatal("the final check counted nothing from the raw exposures")
	}
	for _, err := range final.probed {
		if err != nil {
			t.Errorf("a write of the project while the final check counted = %v, want it through", err)
		}
	}
	res, err := admin.GetExperimentExposuresWithResponse(t.Context(), p.Key, "title_test",
		&management.GetExperimentExposuresParams{Environment: "staging"})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("exposures: %v", err)
	}
	if srm := res.JSON200.SampleRatio; srm == nil || !srm.Final {
		t.Errorf("sample_ratio = %+v, want final", srm)
	}
}
