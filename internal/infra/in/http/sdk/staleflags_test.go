package sdk_test

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// staleFlags reads every page of the project's stale flags listed under
// params, limit included, and returns them by key.
func staleFlags(t *testing.T, p project, params management.ListStaleFlagsParams) map[string]management.StaleFlag {
	t.Helper()
	out := map[string]management.StaleFlag{}
	for {
		res := do(p.admin.ListStaleFlagsWithResponse(t.Context(), p.key, &params))
		if res.JSON200 == nil {
			t.Fatalf("GET …/stale-flags = %d: %s", res.StatusCode(), res.Body)
		}
		for _, f := range res.JSON200.Items {
			out[f.Key] = f
		}
		if res.JSON200.NextCursor == nil {
			return out
		}
		params.Cursor = res.JSON200.NextCursor
	}
}

// staleReasons returns the reasons of every stale flag of the project, by
// key, as "reason,reason".
func staleReasons(t *testing.T, p project) map[string]string {
	t.Helper()
	out := map[string]string{}
	for key, f := range staleFlags(t, p, management.ListStaleFlagsParams{}) {
		reasons := make([]string, len(f.Reasons))
		for i, r := range f.Reasons {
			reasons[i] = string(r)
		}
		out[key] = strings.Join(reasons, ",")
	}
	return out
}

// lastEvaluatedIn returns when f was last evaluated by environment, "" for
// never.
func lastEvaluatedIn(f management.StaleFlag) map[string]string {
	out := map[string]string{}
	for _, l := range f.LastEvaluated {
		out[l.Environment] = ""
		if l.LastEvaluatedAt != nil {
			out[l.Environment] = l.LastEvaluatedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	return out
}

// createFlag creates the boolean flag keyed key as create, filling in its key,
// name and type.
func (p project) createFlag(t *testing.T, key string, create management.FlagCreate) {
	t.Helper()
	create.Key, create.Name, create.Type = key, "Flag "+key, "boolean"
	res := do(p.admin.CreateFlagWithResponse(t.Context(), p.key, create))
	if res.JSON201 == nil {
		t.Fatalf("POST …/flags = %d: %s", res.StatusCode(), res.Body)
	}
}

// temporary is a temporary flag expected to be removed at removal.
func temporary(removal time.Time) management.FlagCreate {
	return management.FlagCreate{Lifetime: ptr(management.LifetimeTemporary), ExpectedRemovalAt: &removal}
}

// configure writes flag's configuration in environment, taken at version 1.
func (p project) configure(t *testing.T, flag, environment string, put management.FlagConfigurationPut) {
	t.Helper()
	res := do(p.admin.PutFlagConfigurationWithResponse(t.Context(), p.key, flag, environment,
		&management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)}, put))
	if res.JSON200 == nil {
		t.Fatalf("PUT …/%s/environments/%s = %d: %s", flag, environment, res.StatusCode(), res.Body)
	}
}

// configureEverywhere writes flag's configuration in every environment.
func (p project) configureEverywhere(t *testing.T, flag string, put management.FlagConfigurationPut) {
	t.Helper()
	for _, env := range []string{"development", "staging", "production"} {
		p.configure(t, flag, env, put)
	}
}

// servingTrue is a configuration switched on, serving true to everyone
// through no rule.
func servingTrue() management.FlagConfigurationPut {
	return management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"}
}

// rollingOut is a configuration switched on serving true to percentage
// basis points of the users and false to the others.
func rollingOut(percentage int) management.FlagConfigurationPut {
	return management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false", Rules: &[]management.RuleInput{{
		Serve: management.Serve{Kind: "rollout", Variation: ptr("true"), UnitType: ptr("user"), Percentage: ptr(percentage)},
	}}}
}

func TestATemporaryFlagPastItsExpectedRemovalIsStale(t *testing.T) {
	f := startSummaries(t)
	now := f.clock.Now()
	removal := now.Add(-time.Hour)
	f.p.createFlag(t, "expired", temporary(removal))
	f.p.createFlag(t, "due_later", temporary(now.Add(time.Hour)))

	got := staleFlags(t, f.p, management.ListStaleFlagsParams{})
	if !slices.Equal(slices.Sorted(maps.Keys(got)), []string{"expired"}) {
		t.Fatalf("stale flags = %v, want expired alone", slices.Sorted(maps.Keys(got)))
	}
	e := got["expired"]
	if !slices.Equal(e.Reasons, []management.StaleReason{management.StaleReasonLifetimeExceeded}) {
		t.Errorf("reasons = %v, want [lifetime_exceeded]", e.Reasons)
	}
	if e.Name != "Flag expired" || e.Lifetime != management.LifetimeTemporary || e.ExpectedRemovalAt == nil ||
		!e.ExpectedRemovalAt.Equal(removal) || e.Experiment != nil {
		t.Errorf("stale flag = %+v, want its name, lifetime and removal, no experiment", e)
	}
	if got := lastEvaluatedIn(e); !maps.Equal(got, map[string]string{"development": "", "staging": "", "production": ""}) {
		t.Errorf("last_evaluated = %v, want every environment, never evaluated", got)
	}

	// A flag past its removal in the summaries' fixture is temporary only
	// when created so: the fixture's permanent flags never are.
	if _, ok := got["checkout"]; ok {
		t.Errorf("permanent flag checkout listed")
	}
}

func TestNotEvaluatedIsJudgedOnlyOnceTheProjectReceivesSummaries(t *testing.T) {
	f := startSummaries(t)
	f.clock.Set(f.clock.Now().AddDate(0, 0, 31))
	if got := staleReasons(t, f.p); len(got) != 0 {
		t.Fatalf("stale flags without any summary = %v, want none", got)
	}

	now := f.clock.Now()
	f.post(t, f.summary(now, counted("checkout", "true", "fallthrough", 3)))
	f.rollUp(t)
	if got := staleReasons(t, f.p); !maps.Equal(got, map[string]string{"banner": "not_evaluated"}) {
		t.Fatalf("stale flags = %v, want banner not_evaluated", got)
	}
	// A flag created within the window may not be deployed yet.
	f.p.flag(t, "fresh")
	if got := staleReasons(t, f.p); !maps.Equal(got, map[string]string{"banner": "not_evaluated"}) {
		t.Errorf("stale flags with a flag created today = %v, want banner alone", got)
	}
}

func TestAFlagNoSDKEvaluatedForThirtyDaysIsNotEvaluated(t *testing.T) {
	f := startSummaries(t)
	f.clock.Set(f.clock.Now().AddDate(0, 0, 31))
	evaluated := f.clock.Now()
	f.post(t, f.summary(evaluated, counted("checkout", "true", "fallthrough", 3), counted("banner", "true", "fallthrough", 1)))
	f.rollUp(t)

	// Another flag keeps the project receiving summaries.
	keepActive := func(at time.Time) {
		f.clock.Set(at)
		f.post(t, f.summary(at, counted("banner", "true", "fallthrough", 1)))
		f.rollUp(t)
	}
	keepActive(evaluated.AddDate(0, 0, 30))
	if got := staleReasons(t, f.p); len(got) != 0 {
		t.Fatalf("stale flags exactly 30 days after the evaluation = %v, want none", got)
	}
	f.clock.Set(evaluated.AddDate(0, 0, 30).Add(time.Second))
	got := staleFlags(t, f.p, management.ListStaleFlagsParams{})
	if c, ok := got["checkout"]; !ok || !slices.Equal(c.Reasons, []management.StaleReason{management.StaleReasonNotEvaluated}) {
		t.Fatalf("stale flags a second later = %+v, want checkout not_evaluated", got)
	}
	if at := lastEvaluatedIn(got["checkout"])["production"]; at != evaluated.Format(time.RFC3339Nano) {
		t.Errorf("checkout last evaluated in production at %q, want %v", at, evaluated)
	}
}

func TestAnArchivedFlagIsNeverStale(t *testing.T) {
	f := startSummaries(t)
	f.p.createFlag(t, "retired", temporary(f.clock.Now().Add(-time.Hour)))
	archived := do(f.p.admin.ArchiveFlagWithResponse(t.Context(), f.p.key, "retired", &management.ArchiveFlagParams{IfMatch: ptr(`"1"`)}))
	if archived.JSON200 == nil {
		t.Fatalf("archive = %d: %s", archived.StatusCode(), archived.Body)
	}
	if got := staleReasons(t, f.p); len(got) != 0 {
		t.Errorf("stale flags = %v, want none", got)
	}
}

func TestARolloutIsFinishedWhenEveryEnvironmentServesTheSameVariationToEveryone(t *testing.T) {
	srv, _ := startSampleRatioServer(t)
	p := newProject(t, srv)
	for flag, put := range map[string]func() management.FlagConfigurationPut{
		"no_rule": servingTrue,
		"full":    func() management.FlagConfigurationPut { return rollingOut(10_000) },
		"half":    func() management.FlagConfigurationPut { return rollingOut(5_000) },
		"switched": func() management.FlagConfigurationPut {
			return management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true"}
		},
		"overridden": servingTrue,
	} {
		p.flag(t, flag)
		p.configureEverywhere(t, flag, put())
	}
	p.flag(t, "never_on")
	p.flag(t, "split")
	p.configure(t, "split", "development", servingTrue())
	p.configure(t, "split", "production", servingTrue())
	p.configure(t, "split", "staging", management.FlagConfigurationPut{On: true, OffVariation: "true", Fallthrough: "false"})
	res := do(p.admin.PutOverrideWithResponse(t.Context(), p.key, "overridden", "production", "user", "qa-1",
		management.OverridePut{Variation: "false"}))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("override = %d: %s", res.StatusCode(), res.Body)
	}

	want := map[string]string{"full": "finished_rollout", "no_rule": "finished_rollout"}
	if got := staleReasons(t, p); !maps.Equal(got, want) {
		t.Errorf("stale flags = %v, want %v", got, want)
	}
}

func TestSeveralReasonsAreListedFilteredAndPaged(t *testing.T) {
	f := startSummaries(t)
	now := f.clock.Now()
	for _, key := range []string{"e1", "e2", "e3", "e4", "e5"} {
		f.p.createFlag(t, key, temporary(now.AddDate(0, 0, 10)))
	}
	f.p.configureEverywhere(t, "e2", servingTrue())
	f.p.configureEverywhere(t, "e4", servingTrue())
	f.clock.Set(now.AddDate(0, 0, 31))
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "true", "fallthrough", 1), counted("banner", "true", "fallthrough", 1),
		counted("e2", "true", "fallthrough", 1), counted("e4", "true", "fallthrough", 1)))
	f.rollUp(t)

	want := map[string]string{
		"e1": "lifetime_exceeded,not_evaluated", "e2": "lifetime_exceeded,finished_rollout",
		"e3": "lifetime_exceeded,not_evaluated", "e4": "lifetime_exceeded,finished_rollout", "e5": "lifetime_exceeded,not_evaluated",
	}
	if got := staleReasons(t, f.p); !maps.Equal(got, want) {
		t.Fatalf("stale flags = %v, want %v", got, want)
	}

	first := do(f.p.admin.ListStaleFlagsWithResponse(t.Context(), f.p.key, &management.ListStaleFlagsParams{Limit: ptr(2)}))
	if first.JSON200 == nil || len(first.JSON200.Items) != 2 || first.JSON200.NextCursor == nil ||
		first.JSON200.Items[0].Key != "e1" || first.JSON200.Items[1].Key != "e2" {
		t.Fatalf("first page of 2 = %d: %s", first.StatusCode(), first.Body)
	}
	if got := staleFlags(t, f.p, management.ListStaleFlagsParams{Limit: ptr(2)}); len(got) != 5 {
		t.Errorf("following pages of 2 = %d flags, want 5", len(got))
	}

	rolledOut := staleFlags(t, f.p, management.ListStaleFlagsParams{
		Reason: &[]management.StaleReason{management.StaleReasonFinishedRollout}, Limit: ptr(1),
	})
	if keys := slices.Sorted(maps.Keys(rolledOut)); !slices.Equal(keys, []string{"e2", "e4"}) {
		t.Errorf("finished_rollout = %v, want e2 and e4", keys)
	}
	either := staleFlags(t, f.p, management.ListStaleFlagsParams{
		Reason: &[]management.StaleReason{management.StaleReasonFinishedRollout, management.StaleReasonOwnerInactive},
	})
	if keys := slices.Sorted(maps.Keys(either)); !slices.Equal(keys, []string{"e2", "e4"}) {
		t.Errorf("finished_rollout or owner_inactive = %v, want e2 and e4", keys)
	}
	if got := staleFlags(t, f.p, management.ListStaleFlagsParams{
		Reason: &[]management.StaleReason{management.StaleReasonExperimentOverdue},
	}); len(got) != 0 {
		t.Errorf("experiment_overdue = %v, want none", slices.Sorted(maps.Keys(got)))
	}

	for name, params := range map[string]management.ListStaleFlagsParams{
		"unknown reason": {Reason: &[]management.StaleReason{"forgotten"}},
		"limit 0":        {Limit: ptr(0)},
		"limit 201":      {Limit: ptr(201)},
		"cursor":         {Cursor: ptr("not base64!")},
	} {
		res := do(f.p.admin.ListStaleFlagsWithResponse(t.Context(), f.p.key, &params))
		refused(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
}

func TestAFlagWithAnOverdueExperimentIsStaleUntilTheEndMovesOrTheExperimentStops(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	end := c.Now()
	f.patch(t, management.ExperimentPatch{ExpectedEndAt: date(end)})
	overdue := func() map[string]management.StaleFlag {
		return staleFlags(t, f.p, management.ListStaleFlagsParams{Reason: &[]management.StaleReason{management.StaleReasonExperimentOverdue}})
	}
	if got := overdue(); len(got) != 0 {
		t.Fatalf("overdue on the expected end day = %v, want none", slices.Sorted(maps.Keys(got)))
	}

	c.Advance(24 * time.Hour)
	got := overdue()
	e, ok := got["checkout"]
	if !ok || e.Experiment == nil || e.Experiment.Key != "checkout_test" ||
		e.Experiment.ExpectedEndAt.Format(time.DateOnly) != end.Format(time.DateOnly) {
		t.Fatalf("overdue = %+v, want checkout naming checkout_test due %s", got, end.Format(time.DateOnly))
	}

	f.patch(t, management.ExperimentPatch{ExpectedEndAt: date(c.Now())})
	if got := overdue(); len(got) != 0 {
		t.Fatalf("overdue once the end moves to today = %v, want none", slices.Sorted(maps.Keys(got)))
	}
	c.Advance(24 * time.Hour)
	if got := overdue(); len(got) != 1 {
		t.Fatalf("overdue a day later = %v, want checkout", slices.Sorted(maps.Keys(got)))
	}
	f.stop(t, management.ExperimentStop{Outcome: management.ExperimentOutcomeRemoved})
	if got := overdue(); len(got) != 0 {
		t.Errorf("overdue once stopped = %v, want none", slices.Sorted(maps.Keys(got)))
	}
}

func TestAFlagWhoseOwnerIsDeactivatedOrHoldsNoGrantHasAnInactiveOwner(t *testing.T) {
	f := startSummaries(t)
	pool := testkit.Pool(t)
	project := f.p.projectID(t)
	owned := func(flag string) testkit.Member {
		m := testkit.NewMember(t, pool)
		testkit.Grant(t, pool, project, m.ID, access.RoleDeveloper)
		owner := openapi_types.UUID(m.ID.Bytes())
		f.p.createFlag(t, flag, management.FlagCreate{Owner: &owner})
		return m
	}
	leaver := owned("orphaned")
	mover := owned("left_behind")
	owned("kept")
	if got := staleReasons(t, f.p); len(got) != 0 {
		t.Fatalf("stale flags while every owner is active = %v, want none", got)
	}

	if res := do(f.p.admin.DeactivateMemberWithResponse(t.Context(), leaver.ID.String())); res.StatusCode() >= 300 {
		t.Fatalf("deactivate = %d: %s", res.StatusCode(), res.Body)
	}
	grants := do(f.p.admin.ListGrantsWithResponse(t.Context(), f.p.key))
	if grants.JSON200 == nil {
		t.Fatalf("grants = %d: %s", grants.StatusCode(), grants.Body)
	}
	for _, g := range grants.JSON200.Items {
		if g.Member == openapi_types.UUID(mover.ID.Bytes()) {
			if res := do(f.p.admin.DeleteGrantWithResponse(t.Context(), f.p.key, g.Id.String())); res.StatusCode() >= 300 {
				t.Fatalf("revoke = %d: %s", res.StatusCode(), res.Body)
			}
		}
	}

	// The fixture's flags are owned by the instance admin, who may own a
	// flag of any project without a grant.
	want := map[string]string{"left_behind": "owner_inactive", "orphaned": "owner_inactive"}
	if got := staleReasons(t, f.p); !maps.Equal(got, want) {
		t.Errorf("stale flags = %v, want %v", got, want)
	}
}
