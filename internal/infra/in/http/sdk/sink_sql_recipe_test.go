package sdk_test

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// recipeSteps are the markers of the sql blocks of the analyst's recipe on
// the exported events page, in the order an analyst runs them.
var recipeSteps = []string{"recipe:first-exposures", "recipe:sample-ratio", "recipe:buckets", "recipe:aliases"}

// recipeExamples are the example literals the page writes the recipe with
// that differ per installation: the project key and the experiment's two
// salts. The environment, experiment key, weights and traffic allocation are
// the page's own values, which the test's experiment is set up to have.
var recipeExamples = regexp.MustCompile(`'(shop|a3f9c2e17b4d0e58|7e1d4c90b2a6f3d1)'`)

// recipeTables are the tables and the view the recipe names unqualified;
// the test's own ClickHouse database holds them.
var recipeTables = regexp.MustCompile(`\b(flagnetic_exposures|flagnetic_aliases|exp_first_exposures)\b`)

// recipeInputs are the test's values for the page's example literals.
type recipeInputs struct {
	project, inclusionSalt, variationSalt, database string
}

// apply puts in's values in place of the page's example literals, failing
// when an example literal is left, and qualifies the tables with in's
// database.
func (in recipeInputs) apply(t *testing.T, sql string) string {
	t.Helper()
	values := map[string]string{
		"shop": in.project, "a3f9c2e17b4d0e58": in.inclusionSalt, "7e1d4c90b2a6f3d1": in.variationSalt,
	}
	sql = recipeExamples.ReplaceAllStringFunc(sql, func(literal string) string {
		return "'" + values[strings.Trim(literal, "'")] + "'"
	})
	for literal := range values {
		if strings.Contains(sql, "'"+literal+"'") {
			t.Fatalf("the recipe still writes the example %q:\n%s", literal, sql)
		}
	}
	return recipeTables.ReplaceAllString(sql, in.database+".$1")
}

// unitBuckets is one unit's buckets, whether it is included and the
// variation the buckets assign it.
type unitBuckets struct {
	inclusion, variation int
	included             bool
	assigned             string
}

// recipeAnswer is what the four steps of the recipe answer for the test's
// experiment, in a form two runs can be compared by.
type recipeAnswer struct {
	observed   map[string]int
	chiSquare  float64
	buckets    map[string]unitBuckets
	firstOf    map[string]string // unit ID: the variation of its first exposure
	aliasedTo  map[string]string // anonymous unit ID: the user it became
	aliasedVar map[string]string // anonymous unit ID: the variation of its first exposure
}

func chBool(t testing.TB, v any) bool {
	t.Helper()
	switch fmt.Sprint(v) {
	case "1", "true":
		return true
	case "0", "false":
		return false
	}
	t.Fatalf("read %#v as a boolean", v)
	return false
}

func runRecipe(t *testing.T, ch *testkit.ClickHouse, doc string, in recipeInputs, units []string) recipeAnswer {
	t.Helper()
	ch.Exec(t, in.apply(t, testkit.DocSQL(t, doc, "recipe:first-exposures")))
	a := recipeAnswer{
		observed: map[string]int{}, buckets: map[string]unitBuckets{}, firstOf: map[string]string{},
		aliasedTo: map[string]string{}, aliasedVar: map[string]string{},
	}
	var chi []float64
	for _, r := range ch.Query(t, in.apply(t, testkit.DocSQL(t, doc, "recipe:sample-ratio"))) {
		a.observed[r["variation_key"].(string)] = chInt(t, r["observed"])
		f, err := strconv.ParseFloat(fmt.Sprint(r["chi_square"]), 64)
		if err != nil {
			t.Fatalf("chi_square %v: %v", r["chi_square"], err)
		}
		chi = append(chi, f)
	}
	if len(chi) == 0 || slices.Min(chi) != slices.Max(chi) {
		t.Fatalf("chi_square per row %v, want one value on every row", chi)
	}
	a.chiSquare = chi[0]

	quoted := make([]string, len(units))
	for i, u := range units {
		quoted[i] = "'" + u + "'"
	}
	buckets := "SELECT * FROM (" + in.apply(t, testkit.DocSQL(t, doc, "recipe:buckets")) + ") WHERE unit_id IN (" + strings.Join(quoted, ", ") + ")"
	for _, r := range ch.Query(t, buckets) {
		u := r["unit_id"].(string)
		a.buckets[u] = unitBuckets{
			inclusion: chInt(t, r["inclusion_bucket"]), variation: chInt(t, r["variation_bucket"]),
			included: chBool(t, r["included"]), assigned: r["assigned"].(string),
		}
		a.firstOf[u] = r["variation_key"].(string)
	}
	for _, r := range ch.Query(t, in.apply(t, testkit.DocSQL(t, doc, "recipe:aliases"))) {
		u := r["unit_id"].(string)
		a.aliasedTo[u] = r["user_id"].(string)
		a.aliasedVar[u] = r["variation_key"].(string)
	}
	return a
}

// TestTheSQLRecipeReproducesThePlatformsNumbersFromAClickHouseSink runs the
// analyst's recipe, as the exported events page prints it, on the rows a
// ClickHouse sink delivered, and holds it to the platform's own answers: the
// first exposures and the sample-ratio check of the exposures read, and the
// buckets of the assignment tool. The exposures include retried event IDs,
// repeat and override exposures and a late, earlier exposure that changes a
// unit's first exposure; a replay then doubles every row, and the recipe,
// which never uses FINAL, answers the same.
func TestTheSQLRecipeReproducesThePlatformsNumbersFromAClickHouseSink(t *testing.T) {
	doc := testkit.Doc(t, "exported-events.md")
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)

	// The page's experiment: checkout_v2 in production, control and
	// treatment at 5000 each, every unit included, on anonymous units that
	// aliases link to users.
	f.p.flagWithVariations(t, "checkout_v2", "control", "control", "treatment", "treatment")
	f.p.experiment(t, "checkout_v2", "checkout_v2", "anonymous", "control", 5000, "treatment", 5000)
	// The sample-ratio check runs in the environments whose rules serve the
	// experiment.
	rules := []management.RuleInput{{Serve: management.Serve{Kind: "experiment", Experiment: ptr("checkout_v2")}}}
	if res := do(f.p.admin.PutFlagConfigurationWithResponse(t.Context(), f.p.key, "checkout_v2", "production",
		&management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)},
		management.FlagConfigurationPut{On: true, OffVariation: "control", Fallthrough: "control", Rules: &rules})); res.JSON200 == nil {
		t.Fatalf("PUT …/configurations/production = %d: %s", res.StatusCode(), res.Body)
	}
	exp := do(f.p.admin.GetExperimentWithResponse(t.Context(), f.p.key, "checkout_v2"))
	if exp.JSON200 == nil {
		t.Fatalf("GET checkout_v2 = %d: %s", exp.StatusCode(), exp.Body)
	}
	if exp.JSON200.TrafficAllocation != 10000 {
		t.Fatalf("traffic allocation %d, want the page's 10000", exp.JSON200.TrafficAllocation)
	}
	in := recipeInputs{project: f.p.key, inclusionSalt: exp.JSON200.InclusionSalt, variationSalt: exp.JSON200.VariationSalt, database: db}

	body := clickhouse(ch, db, true)
	body.BatchSize = ptr(20000)
	created, _ := f.create(t, body)
	f.enable(t, created, management.SinkEnable{})
	start := f.clock.Now()

	ofV2 := func(e map[string]any) map[string]any {
		return with(e, map[string]any{"experiment_key": "checkout_v2", "flag_key": "checkout_v2", "unit_type": "anonymous"})
	}
	exposure := func(unit, variation string, at time.Time) any { return ofV2(f.exposure(unit, variation, at)) }
	override := func(unit, variation string, at time.Time) any { return ofV2(f.override(unit, variation, at)) }
	send := func(evs []any) {
		t.Helper()
		for len(evs) > 0 {
			chunk := evs[:min(len(evs), 500)]
			evs = evs[len(chunk):]
			f.post(t, chunk...)
		}
	}

	// 5199 units see control first and 4801 treatment; one of the latter
	// later turns out to have seen control an hour earlier, which makes the
	// sample-ratio check's worked example: 5200 and 4800 at 5000/5000, X² 16.
	var first []any
	for i := 1; i <= 10000; i++ {
		variation := "control"
		if i > 5199 {
			variation = "treatment"
		}
		first = append(first, exposure("unit-"+strconv.Itoa(i), variation, start))
	}
	send(first)
	// An SDK retrying a batch sends the same event IDs again; they are
	// duplicates and stored once.
	if res := batchResult(t, f.srv, f.server, f.envelope(t, first[:500]...)); res.Accepted != 0 || res.Duplicates != 500 {
		t.Fatalf("retried batch = %+v, want 500 duplicates and none accepted", res)
	}
	var more []any
	for i := 1; i <= 100; i++ {
		more = append(more, exposure("unit-"+strconv.Itoa(i), "control", start.Add(time.Minute)))
	}
	for i := 1; i <= 300; i++ {
		more = append(more, override("qa-"+strconv.Itoa(i), "treatment", start))
	}
	more = append(more, override("unit-2", "treatment", start.Add(-2*time.Hour)))
	send(more)
	check(t, f.srv)

	late := exposure("unit-10000", "control", start.Add(-time.Hour))
	alias := aliasOf(900_000, start, "anonymous", "unit-1", "user", "user-1")
	alias["event_id"] = eventID(start, 900_000)
	f.post(t, late, alias)
	check(t, f.srv)

	const stored = 10000 + 100 + 300 + 1 + 1
	deliver := func(want int) {
		t.Helper()
		for range 20 {
			tick(t, f.srv)
			if chCount(t, ch, db, "flagnetic_exposures", false) >= want {
				break
			}
			f.clock.Advance(11 * time.Second)
		}
		if got := chCount(t, ch, db, "flagnetic_exposures", false); got != want {
			t.Fatalf("exposure rows in ClickHouse = %d, want %d", got, want)
		}
	}
	deliver(stored)

	platform := do(f.p.admin.GetExperimentExposuresWithResponse(t.Context(), f.p.key, "checkout_v2",
		&management.GetExperimentExposuresParams{Environment: "production"}))
	if platform.JSON200 == nil {
		t.Fatalf("GET …/exposures = %d: %s", platform.StatusCode(), platform.Body)
	}
	wantObserved := map[string]int{}
	for _, v := range platform.JSON200.Variations {
		wantObserved[v.Variation] = int(v.FirstExposures)
	}
	if !maps.Equal(wantObserved, map[string]int{"control": 5200, "treatment": 4800}) {
		t.Fatalf("platform first exposures %v, want the worked example's 5200/4800", wantObserved)
	}
	srm := platform.JSON200.SampleRatio
	if srm == nil || srm.ChiSquare == nil || !near(*srm.ChiSquare, 16, 1e-9) {
		t.Fatalf("platform sample-ratio check %+v, want X² 16", srm)
	}

	units := []string{"unit-1", "unit-2", "unit-5555", "unit-10000"}
	want := recipeAnswer{
		observed: wantObserved, chiSquare: *srm.ChiSquare,
		buckets: map[string]unitBuckets{}, firstOf: map[string]string{
			"unit-1": "control", "unit-2": "control", "unit-5555": "treatment", "unit-10000": "control",
		},
		aliasedTo: map[string]string{"unit-1": "user-1"}, aliasedVar: map[string]string{"unit-1": "control"},
	}
	for _, u := range units {
		res := do(f.p.admin.GetExperimentAssignmentWithResponse(t.Context(), f.p.key, "checkout_v2",
			&management.GetExperimentAssignmentParams{UnitId: u}))
		if res.JSON200 == nil {
			t.Fatalf("GET …/assignment of %s = %d: %s", u, res.StatusCode(), res.Body)
		}
		a := res.JSON200
		if a.Variation == nil {
			t.Fatalf("assignment of %s: not included at the default traffic allocation", u)
		}
		want.buckets[u] = unitBuckets{inclusion: a.InclusionBucket, variation: a.VariationBucket, included: a.Included, assigned: *a.Variation}
	}

	assertAnswer := func(when string, got recipeAnswer) {
		t.Helper()
		if !maps.Equal(got.observed, want.observed) {
			t.Errorf("%s: step 2 observed %v, want the exposures read's %v", when, got.observed, want.observed)
		}
		if !near(got.chiSquare, want.chiSquare, 1e-9) {
			t.Errorf("%s: step 2 chi_square %v, want the sample-ratio check's %v", when, got.chiSquare, want.chiSquare)
		}
		for _, u := range units {
			if got.buckets[u] != want.buckets[u] {
				t.Errorf("%s: step 3 buckets of %s %+v, want the assignment tool's %+v", when, u, got.buckets[u], want.buckets[u])
			}
			if got.firstOf[u] != want.firstOf[u] {
				t.Errorf("%s: step 1 first exposure of %s = %q, want %q", when, u, got.firstOf[u], want.firstOf[u])
			}
		}
		if !maps.Equal(got.aliasedTo, want.aliasedTo) || !maps.Equal(got.aliasedVar, want.aliasedVar) {
			t.Errorf("%s: step 4 %v %v, want %v %v", when, got.aliasedTo, got.aliasedVar, want.aliasedTo, want.aliasedVar)
		}
	}
	assertAnswer("after delivery", runRecipe(t, ch, doc, in, units))

	// A replay from before the first row doubles every row; merges are held
	// off so that nothing folds the copies away before the recipe reads them.
	ch.Exec(t, fmt.Sprintf("SYSTEM STOP MERGES %s.flagnetic_exposures", db))
	ch.Exec(t, fmt.Sprintf("SYSTEM STOP MERGES %s.flagnetic_aliases", db))
	f.replay(t, created, management.SinkReplay{From: start.Add(-time.Minute)})
	f.clock.Advance(11 * time.Second)
	deliver(2 * stored)
	if got := chCount(t, ch, db, "flagnetic_aliases", false); got != 2 {
		t.Fatalf("alias rows after the replay = %d, want 2", got)
	}
	assertAnswer("after a replay doubled the rows", runRecipe(t, ch, doc, in, units))

	final := regexp.MustCompile(`(?i)\bFINAL\b`)
	for _, step := range recipeSteps {
		if final.MatchString(testkit.DocSQL(t, doc, step)) {
			t.Errorf("%s uses FINAL", step)
		}
	}
}
