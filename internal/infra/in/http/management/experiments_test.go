package management_test

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// experimentSalt is the form the API promises both of an experiment's salts:
// 16 lower-case hex characters.
var experimentSalt = regexp.MustCompile(`^[0-9a-f]{16}$`)

// experimentFixture is a project with a flag to compare.
type experimentFixture struct {
	srv     *testkit.Server
	admin   *management.ClientWithResponses
	project management.Project
}

func startExperiments(t *testing.T) experimentFixture {
	t.Helper()
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "checkout_title")
	return experimentFixture{srv: srv, admin: admin, project: p}
}

func weights(kv ...any) []management.ExperimentWeight {
	out := make([]management.ExperimentWeight, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, management.ExperimentWeight{Variation: kv[i].(string), Weight: kv[i+1].(int)})
	}
	return out
}

func validExperiment(key string) management.ExperimentCreate {
	return management.ExperimentCreate{
		Key: key, Name: "Experiment " + key, Flag: "checkout_title", UnitType: "user",
		Variations: weights("a", 5000, "b", 5000),
	}
}

func createExperiment(t *testing.T, c *management.ClientWithResponses, project string, body management.ExperimentCreate) management.Experiment {
	t.Helper()
	res := do(c.CreateExperimentWithResponse(t.Context(), project, body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST experiments %s = %d, ETag %q: %s", body.Key, res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

func experimentKeys(t *testing.T, c *management.ClientWithResponses, project string) []string {
	t.Helper()
	var keys []string
	var cursor *string
	for {
		res := do(c.ListExperimentsWithResponse(t.Context(), project, &management.ListExperimentsParams{Cursor: cursor}))
		if res.JSON200 == nil {
			t.Fatalf("GET experiments = %d: %s", res.StatusCode(), res.Body)
		}
		for _, e := range res.JSON200.Items {
			keys = append(keys, e.Key)
		}
		if cursor = res.JSON200.NextCursor; cursor == nil {
			return keys
		}
	}
}

func TestCreatingAnExperimentAnswersADraftWithGeneratedSalts(t *testing.T) {
	f := startExperiments(t)
	adminID := me(t, f.srv, testkit.InstanceAdminToken(t)).Member.Id
	before := rulesetVersions(t, f.project)
	started := time.Now()

	body := management.ExperimentCreate{
		Key: "checkout_v2", Name: "Checkout v2", Hypothesis: ptr("The short title raises completed checkouts."),
		Tags: &[]string{"checkout", "mobile"}, Flag: "checkout_title", UnitType: "anonymous",
		Variations: weights("b", 4000, "a", 6000), TrafficAllocation: ptr(7000),
		ExpectedEndAt: &openapi_types.Date{Time: time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)},
	}
	e := createExperiment(t, f.admin, f.project.Key, body)

	if e.Key != "checkout_v2" || e.Name != "Checkout v2" || e.Hypothesis != "The short title raises completed checkouts." ||
		!slices.Equal(e.Tags, []string{"checkout", "mobile"}) || e.Flag != "checkout_title" || e.UnitType != "anonymous" ||
		e.TrafficAllocation != 7000 || e.Status != management.ExperimentStatusDraft || e.Version != 1 {
		t.Errorf("created experiment = %+v", e)
	}
	// The order of the weights is kept: it fixes the cumulative bucket ranges.
	if !slices.Equal(e.Variations, weights("b", 4000, "a", 6000)) {
		t.Errorf("variations = %v", e.Variations)
	}
	if e.ExpectedEndAt == nil || e.ExpectedEndAt.Format(time.DateOnly) != "2026-12-01" {
		t.Errorf("expected_end_at = %v", e.ExpectedEndAt)
	}
	if e.StartedAt != nil || e.StoppedAt != nil || e.ArchivedAt != nil {
		t.Errorf("a draft has started, stopped or archived: %+v", e)
	}
	// The owner defaults to the creator.
	if e.Owner != adminID || e.CreatedBy == nil || *e.CreatedBy != adminID {
		t.Errorf("owner %v, created_by %v, want %v", e.Owner, e.CreatedBy, adminID)
	}
	if e.CreatedAt.Before(started.Add(-time.Minute)) || !e.UpdatedAt.Equal(e.CreatedAt) {
		t.Errorf("created_at %v, updated_at %v", e.CreatedAt, e.UpdatedAt)
	}
	// Both salts are generated, of the published form, and distinct.
	if !experimentSalt.MatchString(e.InclusionSalt) || !experimentSalt.MatchString(e.VariationSalt) || e.InclusionSalt == e.VariationSalt {
		t.Errorf("salts = %q and %q", e.InclusionSalt, e.VariationSalt)
	}

	// The full resource comes back, both salts included, with its version
	// as the ETag.
	got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "checkout_v2"))
	if got.JSON200 == nil || got.HTTPResponse.Header.Get("ETag") != `"1"` || !experimentEqual(*got.JSON200, e) {
		t.Errorf("GET experiment = %d %s, ETag %q, want %+v", got.StatusCode(), got.Body, got.HTTPResponse.Header.Get("ETag"), e)
	}

	// A draft is invisible to SDKs, so no ruleset moves.
	if moved := bumped(t, "create", before, rulesetVersions(t, f.project)); len(moved) != 0 {
		t.Errorf("a draft bumped %v, want nothing", moved)
	}

	// Defaults: no hypothesis, no tags, all of the traffic, no end date.
	bare := createExperiment(t, f.admin, f.project.Key, validExperiment("bare"))
	if bare.Hypothesis != "" || len(bare.Tags) != 0 || bare.TrafficAllocation != 10000 || bare.ExpectedEndAt != nil {
		t.Errorf("bare experiment = %+v", bare)
	}
}

// experimentEqual compares two answers of the same experiment.
func experimentEqual(a, b management.Experiment) bool {
	return fmt.Sprintf("%+v", a) == fmt.Sprintf("%+v", b)
}

func TestCreatingAnExperimentRefusesEveryViolationNamingTheField(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("taken"))
	archived := createFlag(t, f.admin, f.project.Key, management.FlagCreate{Key: "gone", Type: management.FlagTypeBoolean, Name: "Gone"})
	if res := do(f.admin.ArchiveFlagWithResponse(t.Context(), f.project.Key, archived.Key, &management.ArchiveFlagParams{IfMatch: ifMatch(1)})); res.JSON200 == nil {
		t.Fatalf("archive = %d %s", res.StatusCode(), res.Body)
	}
	stranger := openapi_types.UUID{}
	with := func(change func(*management.ExperimentCreate)) management.ExperimentCreate {
		b := validExperiment("fresh")
		change(&b)
		return b
	}

	for _, c := range []struct {
		what string
		body management.ExperimentCreate
		want []string
	}{
		{"a key outside the grammar", with(func(b *management.ExperimentCreate) { b.Key = "2fast" }), []string{"/key invalid_key"}},
		{"a blank name", with(func(b *management.ExperimentCreate) { b.Name = "  " }), []string{"/name invalid_name"}},
		{"a hypothesis over 2000 characters", with(func(b *management.ExperimentCreate) { b.Hypothesis = ptr(strings.Repeat("x", 2001)) }),
			[]string{"/hypothesis too_long"}},
		{"a blank tag", with(func(b *management.ExperimentCreate) { b.Tags = &[]string{" "} }), []string{"/tags/0 invalid_value"}},
		{"an owner who is no member", with(func(b *management.ExperimentCreate) { b.Owner = &stranger }), []string{"/owner invalid_owner"}},
		{"a flag the project does not have", with(func(b *management.ExperimentCreate) { b.Flag = "absent" }), []string{"/flag unknown_flag"}},
		{"an archived flag", with(func(b *management.ExperimentCreate) {
			b.Flag, b.Variations = "gone", weights("true", 5000, "false", 5000)
		}), []string{"/flag flag_archived"}},
		{"an undeclared unit type", with(func(b *management.ExperimentCreate) { b.UnitType = "device" }), []string{"/unit_type undeclared_unit_type"}},
		{"one variation", with(func(b *management.ExperimentCreate) { b.Variations = weights("a", 10000) }), []string{"/variations invalid_count"}},
		{"the same variation twice", with(func(b *management.ExperimentCreate) { b.Variations = weights("a", 5000, "a", 5000) }),
			[]string{"/variations/1/variation duplicate"}},
		{"a variation the flag does not have", with(func(b *management.ExperimentCreate) { b.Variations = weights("a", 5000, "huge", 5000) }),
			[]string{"/variations/1/variation unknown_variation"}},
		{"a negative weight", with(func(b *management.ExperimentCreate) { b.Variations = weights("a", 10000, "b", -100) }),
			[]string{"/variations/1/weight invalid_weight"}},
		{"weights that would overflow to 10000", with(func(b *management.ExperimentCreate) {
			b.Variations = weights("a", math.MaxInt, "b", math.MaxInt, "c", 10002)
		}), []string{"/variations/0/weight invalid_weight", "/variations/1/weight invalid_weight", "/variations/2/weight invalid_weight"}},
		{"weights summing to 9999", with(func(b *management.ExperimentCreate) { b.Variations = weights("a", 5000, "b", 4999) }),
			[]string{"/variations invalid_weights_sum"}},
		{"a traffic allocation over 10000", with(func(b *management.ExperimentCreate) { b.TrafficAllocation = ptr(10001) }),
			[]string{"/traffic_allocation out_of_range"}},
		{"a negative traffic allocation", with(func(b *management.ExperimentCreate) { b.TrafficAllocation = ptr(-1) }),
			[]string{"/traffic_allocation out_of_range"}},
		{"several at once", with(func(b *management.ExperimentCreate) { b.Name, b.UnitType = "", "device" }),
			[]string{"/name invalid_name", "/unit_type undeclared_unit_type"}},
	} {
		res := do(f.admin.CreateExperimentWithResponse(t.Context(), f.project.Key, c.body))
		if got := violationsOf(t, c.what, res.StatusCode(), res.Body, "invalid_experiment"); !slices.Equal(got, c.want) {
			t.Errorf("%s: violations = %v, want %v", c.what, got, c.want)
		}
	}

	taken := do(f.admin.CreateExperimentWithResponse(t.Context(), f.project.Key, validExperiment("taken")))
	assertProblem(t, "a key in use", taken.StatusCode(), taken.Body, http.StatusConflict, "already_exists")
	unknownProject := do(f.admin.CreateExperimentWithResponse(t.Context(), "no-such-project", validExperiment("x")))
	assertProblem(t, "an unknown project", unknownProject.StatusCode(), unknownProject.Body, http.StatusNotFound, "not_found")
	unknown := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "absent"))
	assertProblem(t, "an unknown experiment", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")

	// Nothing but the one valid experiment was created.
	if keys := experimentKeys(t, f.admin, f.project.Key); !slices.Equal(keys, []string{"taken"}) {
		t.Errorf("experiments = %v, want only taken", keys)
	}
}

func TestEverySaltOfAProjectIsDistinctFromTheRolloutSalts(t *testing.T) {
	f := startExperiments(t)

	// A rollout in every environment, each with a server-generated salt.
	// salts is keyed by the salt itself: what holds it is the value.
	salts := map[string]string{}
	for _, env := range everyEnvironment {
		c := getConfiguration(t, f.admin, f.project.Key, "checkout_title", env)
		c = mustPut(t, f.admin, f.project.Key, "checkout_title", env, c.Version, management.FlagConfigurationPut{
			On: true, OffVariation: "a", Fallthrough: "a", Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 3000)}},
		})
		if len(c.Rules) != 1 || c.Rules[0].Serve.Salt == nil {
			t.Fatalf("rollout in %s has no salt: %+v", env, c.Rules)
		}
		salts[*c.Rules[0].Serve.Salt] = "rollout in " + env
	}
	// Without the rollout salts in hand there is nothing to be distinct
	// from, and the loop below would pass vacuously.
	if len(salts) != len(everyEnvironment) {
		t.Fatalf("collected %d rollout salts, want one per environment (%d)", len(salts), len(everyEnvironment))
	}

	for i := range 5 {
		e := createExperiment(t, f.admin, f.project.Key, validExperiment(fmt.Sprintf("e%d", i)))
		for what, salt := range map[string]string{"inclusion": e.InclusionSalt, "variation": e.VariationSalt} {
			if !experimentSalt.MatchString(salt) {
				t.Errorf("%s salt of %s = %q, want 16 lower-case hex characters", what, e.Key, salt)
			}
			if owner, taken := salts[salt]; taken {
				t.Errorf("%s salt of %s is %q, already the %s", what, e.Key, salt, owner)
			}
			salts[salt] = what + " salt of " + e.Key
		}
	}

	// Regenerated and cloned salts are drawn apart from them as well.
	regenerated := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), f.project.Key, "e0", &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(1)}))
	cloned := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "e1", management.ExperimentClone{Key: "e1_clone"}))
	if regenerated.JSON200 == nil || cloned.JSON201 == nil {
		t.Fatalf("salts = %d %s, clone = %d %s", regenerated.StatusCode(), regenerated.Body, cloned.StatusCode(), cloned.Body)
	}
	for _, e := range []management.Experiment{*regenerated.JSON200, *cloned.JSON201} {
		for what, salt := range map[string]string{"inclusion": e.InclusionSalt, "variation": e.VariationSalt} {
			if owner, taken := salts[salt]; taken {
				t.Errorf("new %s salt of %s is %q, already the %s", what, e.Key, salt, owner)
			}
			salts[salt] = what + " salt of " + e.Key
		}
	}
}

func TestEveryExperimentWriteRecordsOneAuditEntryWithTheExperimentAfter(t *testing.T) {
	f := startExperiments(t)
	e := createExperiment(t, f.admin, f.project.Key, validExperiment("audited"))

	entries := auditOf(t, f.srv, "experiment.created", e.Id.String())
	if len(entries) != 1 {
		t.Fatalf("experiment.created entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Entity.Kind != "experiment" || entry.Entity.Key != "audited" || entry.Before != nil ||
		entry.ProjectId == nil || *entry.ProjectId != f.project.Id {
		t.Errorf("entry = %+v", entry)
	}
	after, ok := entry.After.(map[string]any)
	if !ok || after["key"] != "audited" || after["status"] != "draft" || after["flag"] != "checkout_title" ||
		after["inclusion_salt"] != e.InclusionSalt || after["variation_salt"] != e.VariationSalt || after["version"] != float64(1) {
		t.Errorf("after = %v, want the experiment as created", entry.After)
	}

	// A refused write records nothing.
	before := len(auditActions(t, f.admin, f.project.Key))
	do(f.admin.CreateExperimentWithResponse(t.Context(), f.project.Key, validExperiment("audited")))
	if n := len(auditActions(t, f.admin, f.project.Key)); n != before {
		t.Errorf("a refused create recorded %d entries", n-before)
	}
}

func TestListingExperimentsPagesByKeyInByteOrder(t *testing.T) {
	f := startExperiments(t)
	want := []string{"Alpha", "beta", "delta", "gamma", "zulu"}
	for _, k := range []string{"gamma", "Alpha", "zulu", "beta", "delta"} {
		createExperiment(t, f.admin, f.project.Key, validExperiment(k))
	}

	if got := experimentKeys(t, f.admin, f.project.Key); !slices.Equal(got, want) {
		t.Errorf("experiments = %v, want %v", got, want)
	}
	page := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &management.ListExperimentsParams{Limit: ptr(2)}))
	if page.JSON200 == nil || len(page.JSON200.Items) != 2 || page.JSON200.NextCursor == nil {
		t.Fatalf("first page = %d %s", page.StatusCode(), page.Body)
	}
	last := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &management.ListExperimentsParams{Limit: ptr(5)}))
	if last.JSON200 == nil || len(last.JSON200.Items) != 5 || last.JSON200.NextCursor != nil {
		t.Errorf("a page holding every experiment = %d %s, want no next cursor", last.StatusCode(), last.Body)
	}

	for what, params := range map[string]management.ListExperimentsParams{
		"a cursor this API never returned": {Cursor: ptr("!!")},
		"a limit of 0":                     {Limit: ptr(0)},
		"a limit of 201":                   {Limit: ptr(201)},
	} {
		res := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &params))
		assertProblem(t, what, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
}
