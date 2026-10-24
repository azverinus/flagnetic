package experiments_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/eval"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgexperiments "github.com/azverinus/flagnetic/internal/infra/out/postgres/experiments"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// errRollBack ends a caller's unit of work without committing it.
var errRollBack = errors.New("roll back")

// A caller applying a start together with its own write, such as an
// approved change, runs the handler inside its own unit of work: the start
// then commits and rolls back with that unit, its audit entry and its
// ruleset moves included.
func TestAStartInACallersUnitOfWorkCommitsAndRollsBackWithIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	project := setUp(t, admin)

	pool := testkit.Pool(t)
	c := clock.System{}
	ids := id.NewSource(c)
	tx, store, flagStore := postgres.NewTransactor(pool), pgexperiments.NewStore(pool), pgflags.NewStore(pool)
	start := appexperiments.NewStartExperiment(appexperiments.StartExperimentDeps{
		Tx: tx, Projects: store, Store: store, Flags: flagStore, UnitTypes: pgworkspace.NewStore(pool),
		Holders: flagStore, Rulesets: appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool), c),
		Recorder: appexperiments.NewRecorder(appgovernance.NewRecord(pggovernance.NewAuditLog(pool)), c, ids),
	})
	cmd := appexperiments.ExperimentCommand{
		Project: id.FromBytes[workspace.Project](project.Id), Key: "title_test", Expected: precondition.On(1),
		Request: governance.Request{Actor: governance.CLIActor("test")},
	}
	before := versions(t, project)["staging"]

	err := tx.Within(t.Context(), func(ctx context.Context) error {
		e, err := start.Handle(ctx, cmd)
		if err != nil {
			return err
		}
		if e.Status != experiments.StatusRunning {
			t.Errorf("started = %+v", e)
		}
		return errRollBack
	})
	if !errors.Is(err, errRollBack) {
		t.Fatalf("the caller's unit of work = %v", err)
	}
	if e := experimentOf(t, admin, project.Key); e.Status != management.ExperimentStatusDraft || e.Version != 1 {
		t.Errorf("after a rollback: %+v", e)
	}
	if got := versions(t, project)["staging"]; got != before {
		t.Errorf("after a rollback: staging at %d, want %d", got, before)
	}
	if n := startedEntries(t, admin, project); n != 0 {
		t.Errorf("after a rollback: %d experiment.started entries", n)
	}

	err = tx.Within(t.Context(), func(ctx context.Context) error {
		_, startErr := start.Handle(ctx, cmd)
		return startErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if e := experimentOf(t, admin, project.Key); e.Status != management.ExperimentStatusRunning || e.Version != 2 {
		t.Errorf("after a commit: %+v", e)
	}
	if got := versions(t, project)["staging"]; got == before {
		t.Errorf("after a commit: staging still at %d", got)
	}
	if n := startedEntries(t, admin, project); n != 1 {
		t.Errorf("after a commit: %d experiment.started entries, want 1", n)
	}
}

// A start reads the environments holding the experiment under its project's
// row lock, which a configuration write holds until it commits: a start
// beginning while production's configuration gains a rule serving the
// experiment waits for that write, then sees production among the holders,
// so production's SDKs are given the running experiment.
func TestAStartWaitsForAConfigurationWriteAddingARuleServingIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	project := setUp(t, admin)
	pool := testkit.Pool(t)
	c := clock.System{}
	ids := id.NewSource(c)
	tx, flagStore := postgres.NewTransactor(pool), pgflags.NewStore(pool)
	replace := appflags.NewReplaceConfiguration(appflags.ReplaceConfigurationDeps{
		Tx: tx, Projects: flagStore, Declarations: pgworkspace.NewStore(pool), Store: flagStore,
		Rulesets: appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool), c),
		Recorder: appflags.NewRecorder(appgovernance.NewRecord(pggovernance.NewAuditLog(pool)), c, ids),
	})

	written := make(chan struct{})
	release := make(chan struct{})
	writer := make(chan error, 1)
	go func() {
		writer <- tx.Within(context.Background(), func(ctx context.Context) error {
			_, err := replace.Handle(ctx, appflags.ReplaceConfigurationCommand{
				Configuration: appflags.ConfigurationCommand{
					Project: id.FromBytes[workspace.Project](project.Id), Flag: "checkout_title", Environment: "production",
					Expected: precondition.On(1), Request: governance.Request{Actor: governance.CLIActor("test")},
				},
				Input: flags.ConfigurationInput{On: true, OffVariation: "a", Fallthrough: "a",
					Rules: []flags.RuleInput{{Serve: flags.ServeInput{Kind: flags.ServeExperiment, Experiment: "title_test"}}}},
			})
			close(written)
			<-release
			return err
		})
	}()
	<-written
	started := make(chan *management.StartExperimentResponse, 1)
	go func() {
		res, _ := admin.StartExperimentWithResponse(context.Background(), project.Key, "title_test",
			&management.StartExperimentParams{IfMatch: ptr(`"1"`)})
		started <- res
	}()
	select {
	case res := <-started:
		t.Fatalf("the start answered %d while a configuration write held the project", res.StatusCode())
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	if res := <-started; res == nil || res.JSON200 == nil {
		t.Fatalf("the start after the write = %v", res)
	}
	rs := rulesetOf(t, admin, project, "production")
	i := slices.IndexFunc(rs.Flags, func(f eval.Flag) bool { return f.Key == "checkout_title" })
	if i < 0 {
		t.Fatal("production's ruleset has no checkout_title")
	}
	if rules := rs.Flags[i].Rules; len(rules) != 1 || rules[0].Serve.Experiment != "title_test" || rules[0].Serve.InclusionSalt == "" {
		t.Errorf("production serves %+v, want the running experiment", rules)
	}
}

// rulesetOf returns the ruleset of project's environment at its current
// version, the way an SDK receives it.
func rulesetOf(t *testing.T, c *management.ClientWithResponses, p management.Project, environment string) eval.Ruleset {
	t.Helper()
	want := versions(t, p)[environment]
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := c.GetEnvironmentRulesetWithResponse(t.Context(), p.Key, environment)
		if err != nil {
			t.Fatal(err)
		}
		var rs eval.Ruleset
		if res.StatusCode() == 200 && json.Unmarshal(res.Body, &rs) == nil && rs.Version >= want {
			return rs
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET ruleset = %d %s, want version %d", res.StatusCode(), res.Body, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// setUp creates a project with a flag and a ready draft whose rule staging
// holds.
func setUp(t *testing.T, c *management.ClientWithResponses) management.Project {
	t.Helper()
	key := "p" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	p, err := c.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Project " + key})
	if err != nil || p.JSON201 == nil {
		t.Fatalf("create project: %v", err)
	}
	vs := []management.VariationInput{{Key: "a", Value: []byte(`"A"`)}, {Key: "b", Value: []byte(`"B"`)}}
	if res, err := c.CreateFlagWithResponse(t.Context(), key, management.FlagCreate{
		Key: "checkout_title", Name: "Checkout title", Type: management.FlagTypeString, Variations: &vs,
	}); err != nil || res.JSON201 == nil {
		t.Fatalf("create flag: %v", err)
	}
	if res, err := c.CreateExperimentWithResponse(t.Context(), key, management.ExperimentCreate{
		Key: "title_test", Name: "Titles", Flag: "checkout_title", UnitType: "user",
		Hypothesis:    ptr("Short titles raise completed checkouts."),
		Variations:    []management.ExperimentWeight{{Variation: "a", Weight: 5000}, {Variation: "b", Weight: 5000}},
		ExpectedEndAt: &openapi_types.Date{Time: time.Date(2027, time.January, 31, 0, 0, 0, 0, time.UTC)},
	}); err != nil || res.JSON201 == nil {
		t.Fatalf("create experiment: %v", err)
	}
	rules := []management.RuleInput{{Serve: management.Serve{Kind: "experiment", Experiment: ptr("title_test")}}}
	if res, err := c.PutFlagConfigurationWithResponse(t.Context(), key, "checkout_title", "staging",
		&management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)},
		management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a", Rules: &rules},
	); err != nil || res.JSON200 == nil {
		t.Fatalf("put configuration: %v", err)
	}
	return *p.JSON201
}

// versions returns the ruleset version of every environment of p, by key.
func versions(t *testing.T, p management.Project) map[string]int64 {
	t.Helper()
	v, err := pgdelivery.NewStore(testkit.Pool(t)).RulesetVersions(t.Context(), id.FromBytes[workspace.Project](p.Id))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func experimentOf(t *testing.T, c *management.ClientWithResponses, project string) management.Experiment {
	t.Helper()
	res, err := c.GetExperimentWithResponse(t.Context(), project, "title_test")
	if err != nil || res.JSON200 == nil {
		t.Fatalf("get experiment: %v", err)
	}
	return *res.JSON200
}

func startedEntries(t *testing.T, c *management.ClientWithResponses, p management.Project) int {
	t.Helper()
	action, entity := "experiment.started", experimentOf(t, c, p.Key).Id.String()
	res, err := c.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action, EntityId: &entity})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("list audit entries: %v", err)
	}
	return len(res.JSON200.Items)
}

func ptr[T any](v T) *T { return &v }
