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
