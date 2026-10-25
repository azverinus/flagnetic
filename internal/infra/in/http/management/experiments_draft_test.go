package management_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgexperiments "github.com/azverinus/flagnetic/internal/infra/out/postgres/experiments"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func patchExperiment(t *testing.T, c *management.ClientWithResponses, project, key string, version int64, body management.ExperimentPatch) *management.UpdateExperimentResponse {
	t.Helper()
	return do(c.UpdateExperimentWithResponse(t.Context(), project, key, &management.UpdateExperimentParams{IfMatch: ifMatch(version)}, body))
}

// putInStatus moves an experiment to status through the repository: these
// tests are about what each status allows, not how it is reached.
func putInStatus(t *testing.T, f experimentFixture, key string, status experiments.Status) {
	t.Helper()
	store := pgexperiments.NewStore(testkit.Pool(t))
	e, err := store.ExperimentByKey(t.Context(), projectIDOf(f.project), key)
	if err != nil {
		t.Fatal(err)
	}
	e.Status = status
	if err := store.UpdateExperiment(t.Context(), e); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyADraftIsEditedHasItsSaltsRegeneratedAndIsDeletedButAnyIsCloned(t *testing.T) {
	f := startExperiments(t)
	for _, status := range []experiments.Status{experiments.StatusRunning, experiments.StatusStopped, experiments.StatusArchived} {
		key := string(status)
		created := createExperiment(t, f.admin, f.project.Key, validExperiment(key))
		putInStatus(t, f, key, status)

		patch := patchExperiment(t, f.admin, f.project.Key, key, 1, management.ExperimentPatch{Flag: ptr("checkout_title"), Variations: &[]management.ExperimentWeight{{Variation: "a", Weight: 1000}, {Variation: "b", Weight: 9000}}})
		if patch.StatusCode() != http.StatusConflict {
			t.Errorf("PATCH of a %s experiment's weights = %d, want 409: %s", status, patch.StatusCode(), patch.Body)
		}
		// An archived experiment refuses every write as archived.
		code := "not_draft"
		if status == experiments.StatusArchived {
			code = "archived"
		}
		salts := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), f.project.Key, key, &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(1)}))
		assertProblem(t, "salts of a "+key+" experiment", salts.StatusCode(), salts.Body, http.StatusConflict, code)
		del := do(f.admin.DeleteExperimentWithResponse(t.Context(), f.project.Key, key, &management.DeleteExperimentParams{IfMatch: ifMatch(1)}))
		assertProblem(t, "delete of a "+key+" experiment", del.StatusCode(), del.Body, http.StatusConflict, code)

		got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, key))
		if got.JSON200 == nil || got.JSON200.Version != 1 || got.JSON200.InclusionSalt != created.InclusionSalt || !slices.Equal(got.JSON200.Variations, created.Variations) {
			t.Errorf("%s experiment after refused writes = %s", status, got.Body)
		}

		clone := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, key, management.ExperimentClone{Key: key + "_again"}))
		if clone.JSON201 == nil || clone.JSON201.Status != management.ExperimentStatusDraft || clone.JSON201.InclusionSalt == created.InclusionSalt {
			t.Errorf("clone of a %s experiment = %d %s", status, clone.StatusCode(), clone.Body)
		}
	}
}

func TestPatchingADraftChangesEveryFieldAndRecordsTheExperimentBeforeAndAfter(t *testing.T) {
	f := startExperiments(t)
	createFlag(t, f.admin, f.project.Key, management.FlagCreate{Key: "other_flag", Type: management.FlagTypeBoolean, Name: "Other"})
	created := createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	dev := testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(f.project), access.RoleDeveloper)
	devID := me(t, f.srv, dev).Member.Id
	before := rulesetVersions(t, f.project)

	res := patchExperiment(t, f.admin, f.project.Key, "draft", 1, management.ExperimentPatch{
		Name: ptr("Renamed"), Hypothesis: ptr("A new bet."), Owner: &devID, Tags: &[]string{"x"},
		Flag: ptr("other_flag"), UnitType: ptr("anonymous"), Variations: &[]management.ExperimentWeight{{Variation: "true", Weight: 3000}, {Variation: "false", Weight: 7000}},
		TrafficAllocation: ptr(2500), ExpectedEndAt: &openapi_types.Date{Time: time.Date(2027, time.January, 5, 0, 0, 0, 0, time.UTC)},
	})
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("PATCH = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	e := *res.JSON200
	if e.Name != "Renamed" || e.Hypothesis != "A new bet." || e.Owner != devID || !slices.Equal(e.Tags, []string{"x"}) || e.Flag != "other_flag" ||
		e.UnitType != "anonymous" || e.TrafficAllocation != 2500 || e.Version != 2 || e.ExpectedEndAt == nil || e.ExpectedEndAt.Format(time.DateOnly) != "2027-01-05" ||
		!slices.Equal(e.Variations, weights("true", 3000, "false", 7000)) {
		t.Errorf("patched experiment = %+v", e)
	}
	// What never changes: key, salts, creation.
	if e.Key != created.Key || e.InclusionSalt != created.InclusionSalt || e.VariationSalt != created.VariationSalt ||
		!e.CreatedAt.Equal(created.CreatedAt) || !e.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("patched experiment lost what never changes: %+v", e)
	}
	if got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "draft")); got.JSON200 == nil || !experimentEqual(*got.JSON200, e) {
		t.Errorf("GET after PATCH = %s, want %+v", got.Body, e)
	}
	if moved := bumped(t, "patch", before, rulesetVersions(t, f.project)); len(moved) != 0 {
		t.Errorf("patching a draft bumped %v, want nothing", moved)
	}

	entries := auditOf(t, f.srv, "experiment.updated", e.Id.String())
	if len(entries) != 1 {
		t.Fatalf("experiment.updated entries = %d, want 1", len(entries))
	}
	b, _ := entries[0].Before.(map[string]any)
	a, _ := entries[0].After.(map[string]any)
	if b["name"] != created.Name || b["version"] != float64(1) || a["name"] != "Renamed" || a["version"] != float64(2) || a["flag"] != "other_flag" {
		t.Errorf("audit before %v, after %v", entries[0].Before, entries[0].After)
	}
}

func TestPatchingAnExperimentWithValuesItHasChangesNothing(t *testing.T) {
	f := startExperiments(t)
	e := createExperiment(t, f.admin, f.project.Key, validExperiment("same"))
	entries := len(auditActions(t, f.admin, f.project.Key))

	res := patchExperiment(t, f.admin, f.project.Key, "same", 1, management.ExperimentPatch{
		Name: &e.Name, Owner: &e.Owner, Variations: &e.Variations, Flag: &e.Flag, TrafficAllocation: &e.TrafficAllocation,
	})
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` || !experimentEqual(*res.JSON200, e) {
		t.Errorf("no-op PATCH = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	if n := len(auditActions(t, f.admin, f.project.Key)); n != entries {
		t.Errorf("a no-op PATCH recorded %d entries", n-entries)
	}
}

func TestPatchingAnExperimentRefusesEveryViolationNamingTheField(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	archived := createFlag(t, f.admin, f.project.Key, management.FlagCreate{Key: "gone", Type: management.FlagTypeBoolean, Name: "Gone"})
	if res := do(f.admin.ArchiveFlagWithResponse(t.Context(), f.project.Key, archived.Key, &management.ArchiveFlagParams{IfMatch: ifMatch(1)})); res.JSON200 == nil {
		t.Fatalf("archive = %d %s", res.StatusCode(), res.Body)
	}
	stranger := openapi_types.UUID{}
	for _, c := range []struct {
		what  string
		patch management.ExperimentPatch
		want  []string
	}{
		{"the key", management.ExperimentPatch{Key: ptr("draft")}, []string{"/key immutable"}},
		{"a blank name", management.ExperimentPatch{Name: ptr(" ")}, []string{"/name invalid_name"}},
		{"an owner who is no member", management.ExperimentPatch{Owner: &stranger}, []string{"/owner invalid_owner"}},
		{"an undeclared unit type", management.ExperimentPatch{UnitType: ptr("device")}, []string{"/unit_type undeclared_unit_type"}},
		{"an unknown flag", management.ExperimentPatch{Flag: ptr("absent")}, []string{"/flag unknown_flag"}},
		{"an archived flag", management.ExperimentPatch{Flag: ptr("gone")}, []string{"/flag flag_archived"}},
		{"weights that do not sum", management.ExperimentPatch{Variations: &[]management.ExperimentWeight{{Variation: "a", Weight: 1}, {Variation: "b", Weight: 1}}},
			[]string{"/variations invalid_weights_sum"}},
		{"a traffic allocation over 10000", management.ExperimentPatch{TrafficAllocation: ptr(10001)}, []string{"/traffic_allocation out_of_range"}},
	} {
		res := patchExperiment(t, f.admin, f.project.Key, "draft", 1, c.patch)
		got := violationsOf(t, c.what, res.StatusCode(), res.Body, "invalid_experiment")
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: violations = %v, want %v", c.what, got, c.want)
		}
	}

	missing := patchExperiment(t, f.admin, f.project.Key, "absent", 1, management.ExperimentPatch{Name: ptr("x")})
	assertProblem(t, "an unknown experiment", missing.StatusCode(), missing.Body, http.StatusNotFound, "not_found")
}

func TestExperimentWritesNeedTheCurrentVersionInIfMatch(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	if res := patchExperiment(t, f.admin, f.project.Key, "draft", 1, management.ExperimentPatch{Name: ptr("Second")}); res.JSON200 == nil {
		t.Fatalf("PATCH = %d %s", res.StatusCode(), res.Body)
	}

	check := func(what string, status int, body []byte, wantStatus int, wantCode string) {
		t.Helper()
		pr := assertProblem(t, what, status, body, wantStatus, wantCode)
		if wantStatus == http.StatusPreconditionFailed && (pr.CurrentVersion == nil || *pr.CurrentVersion != 2) {
			t.Errorf("%s: current_version = %v, want 2", what, pr.CurrentVersion)
		}
	}
	p, k := f.project.Key, "draft"
	noHeader := do(f.admin.UpdateExperimentWithResponse(t.Context(), p, k, &management.UpdateExperimentParams{}, management.ExperimentPatch{Name: ptr("x")}))
	check("patch without If-Match", noHeader.StatusCode(), noHeader.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := patchExperiment(t, f.admin, p, k, 1, management.ExperimentPatch{Name: ptr("x")})
	check("patch at a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	malformed := do(f.admin.UpdateExperimentWithResponse(t.Context(), p, k, &management.UpdateExperimentParams{IfMatch: ptr(`W/"2"`)}, management.ExperimentPatch{Name: ptr("x")}))
	assertProblem(t, "a weak tag", malformed.StatusCode(), malformed.Body, http.StatusBadRequest, "invalid_request")

	saltsNone := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), p, k, &management.RegenerateExperimentSaltsParams{}))
	check("salts without If-Match", saltsNone.StatusCode(), saltsNone.Body, http.StatusPreconditionRequired, "precondition_required")
	saltsStale := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), p, k, &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(1)}))
	check("salts at a stale version", saltsStale.StatusCode(), saltsStale.Body, http.StatusPreconditionFailed, "version_mismatch")
	delNone := do(f.admin.DeleteExperimentWithResponse(t.Context(), p, k, &management.DeleteExperimentParams{}))
	check("delete without If-Match", delNone.StatusCode(), delNone.Body, http.StatusPreconditionRequired, "precondition_required")
	delStale := do(f.admin.DeleteExperimentWithResponse(t.Context(), p, k, &management.DeleteExperimentParams{IfMatch: ifMatch(1)}))
	check("delete at a stale version", delStale.StatusCode(), delStale.Body, http.StatusPreconditionFailed, "version_mismatch")

	// None of the refused writes changed anything.
	if got := do(f.admin.GetExperimentWithResponse(t.Context(), p, k)); got.JSON200 == nil || got.JSON200.Version != 2 || got.JSON200.Name != "Second" {
		t.Errorf("experiment after refused writes = %s", got.Body)
	}
}

func TestRegeneratingSaltsDrawsNewOnesApartFromTheProjects(t *testing.T) {
	f := startExperiments(t)
	e := createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	other := createExperiment(t, f.admin, f.project.Key, validExperiment("other"))

	res := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), f.project.Key, "draft", &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(1)}))
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("POST salts = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	got := *res.JSON200
	taken := []string{e.InclusionSalt, e.VariationSalt, other.InclusionSalt, other.VariationSalt}
	for _, salt := range []string{got.InclusionSalt, got.VariationSalt} {
		if !experimentSalt.MatchString(salt) || slices.Contains(taken, salt) {
			t.Errorf("new salt %q is malformed or already used (%v)", salt, taken)
		}
	}
	if got.InclusionSalt == got.VariationSalt || got.Version != 2 || got.Key != "draft" || got.Name != e.Name {
		t.Errorf("experiment after regenerating = %+v", got)
	}

	entries := auditOf(t, f.srv, "experiment.salts_regenerated", e.Id.String())
	if len(entries) != 1 {
		t.Fatalf("experiment.salts_regenerated entries = %d, want 1", len(entries))
	}
	b, _ := entries[0].Before.(map[string]any)
	a, _ := entries[0].After.(map[string]any)
	if b["inclusion_salt"] != e.InclusionSalt || a["inclusion_salt"] != got.InclusionSalt || a["version"] != float64(2) {
		t.Errorf("audit before %v, after %v", entries[0].Before, entries[0].After)
	}

	unknown := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), f.project.Key, "absent", &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(1)}))
	assertProblem(t, "an unknown experiment", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
}

func TestDeletingADraftRemovesItAndRecordsItAsItWas(t *testing.T) {
	f := startExperiments(t)
	e := createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	createExperiment(t, f.admin, f.project.Key, validExperiment("kept"))

	res := do(f.admin.DeleteExperimentWithResponse(t.Context(), f.project.Key, "draft", &management.DeleteExperimentParams{IfMatch: ifMatch(1)}))
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", res.StatusCode(), res.Body)
	}
	gone := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "draft"))
	assertProblem(t, "a deleted experiment", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	if keys := experimentKeys(t, f.admin, f.project.Key); !slices.Equal(keys, []string{"kept"}) {
		t.Errorf("experiments = %v, want only kept", keys)
	}
	// The key is free again.
	createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))

	entries := auditOf(t, f.srv, "experiment.deleted", e.Id.String())
	if len(entries) != 1 || entries[0].After != nil {
		t.Fatalf("experiment.deleted entries = %+v", entries)
	}
	if b, _ := entries[0].Before.(map[string]any); b["key"] != "draft" || b["version"] != float64(1) {
		t.Errorf("audit before = %v", entries[0].Before)
	}

	again := do(f.admin.DeleteExperimentWithResponse(t.Context(), f.project.Key, "absent", &management.DeleteExperimentParams{IfMatch: ifMatch(1)}))
	assertProblem(t, "an unknown experiment", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
}

func TestCloningCopiesTheComparisonIntoANewDraftWithNewSalts(t *testing.T) {
	f := startExperiments(t)
	dev := testkit.Client(t, f.srv, testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(f.project), access.RoleDeveloper))
	src := createExperiment(t, f.admin, f.project.Key, management.ExperimentCreate{
		Key: "source", Name: "Source", Hypothesis: ptr("It works."), Tags: &[]string{"a", "b"}, Flag: "checkout_title", UnitType: "anonymous",
		Variations: weights("b", 3000, "a", 7000), TrafficAllocation: ptr(4000),
		ExpectedEndAt: &openapi_types.Date{Time: time.Date(2027, time.February, 3, 0, 0, 0, 0, time.UTC)},
	})
	before := rulesetVersions(t, f.project)

	// A developer clones; the clone is theirs.
	res := do(dev.CloneExperimentWithResponse(t.Context(), f.project.Key, "source", management.ExperimentClone{Key: "rerun"}))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST clone = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	c := *res.JSON201
	if c.Key != "rerun" || c.Name != "Source" || c.Hypothesis != "It works." || !slices.Equal(c.Tags, []string{"a", "b"}) || c.Flag != "checkout_title" ||
		c.UnitType != "anonymous" || c.TrafficAllocation != 4000 || !slices.Equal(c.Variations, weights("b", 3000, "a", 7000)) ||
		c.ExpectedEndAt == nil || c.ExpectedEndAt.Format(time.DateOnly) != "2027-02-03" ||
		c.Status != management.ExperimentStatusDraft || c.Version != 1 || c.StartedAt != nil || c.StoppedAt != nil || c.ArchivedAt != nil {
		t.Errorf("clone = %+v", c)
	}
	if c.Id == src.Id || c.Owner == src.Owner || c.CreatedBy == nil || *c.CreatedBy != c.Owner {
		t.Errorf("clone id %v, owner %v (source %v), created_by %v", c.Id, c.Owner, src.Owner, c.CreatedBy)
	}
	for _, salt := range []string{c.InclusionSalt, c.VariationSalt} {
		if slices.Contains([]string{src.InclusionSalt, src.VariationSalt}, salt) || !experimentSalt.MatchString(salt) {
			t.Errorf("clone salt %q is malformed or the source's", salt)
		}
	}
	if got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "source")); got.JSON200 == nil || !experimentEqual(*got.JSON200, src) {
		t.Errorf("the source after cloning = %s, want it untouched", got.Body)
	}
	if moved := bumped(t, "clone", before, rulesetVersions(t, f.project)); len(moved) != 0 {
		t.Errorf("cloning bumped %v, want nothing", moved)
	}

	entries := auditOf(t, f.srv, "experiment.cloned", c.Id.String())
	if len(entries) != 1 || entries[0].Before != nil {
		t.Fatalf("experiment.cloned entries = %+v", entries)
	}
	if a, _ := entries[0].After.(map[string]any); a["key"] != "rerun" || a["cloned_from"] != "source" || a["inclusion_salt"] != c.InclusionSalt {
		t.Errorf("audit after = %v", entries[0].After)
	}

	// The name can be given; a project token that names no owner keeps the source's.
	named := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "source", management.ExperimentClone{Key: "named", Name: ptr("Second run")}))
	if named.JSON201 == nil || named.JSON201.Name != "Second run" || named.JSON201.Owner != src.Owner {
		t.Errorf("named clone = %d %s", named.StatusCode(), named.Body)
	}
	token := testkit.Client(t, f.srv, testkit.ProjectToken(t, testkit.Pool(t), projectIDOf(f.project), access.RoleDeveloper, access.ScopeExperimentsWrite))
	byToken := do(token.CloneExperimentWithResponse(t.Context(), f.project.Key, "source", management.ExperimentClone{Key: "by_token"}))
	if byToken.JSON201 == nil || byToken.JSON201.Owner != src.Owner || byToken.JSON201.CreatedBy != nil {
		t.Errorf("clone by a project token = %d %s", byToken.StatusCode(), byToken.Body)
	}
}

func TestCloningIsRefusedForAKeyInUseAndAnUnknownSource(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("old"))
	createExperiment(t, f.admin, f.project.Key, validExperiment("source"))

	taken := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "source", management.ExperimentClone{Key: "old"}))
	assertProblem(t, "a key in use", taken.StatusCode(), taken.Body, http.StatusConflict, "already_exists")
	unknown := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "absent", management.ExperimentClone{Key: "x"}))
	assertProblem(t, "an unknown source", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
	badKey := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "source", management.ExperimentClone{Key: "2fast"}))
	if got := violationsOf(t, "a key outside the grammar", badKey.StatusCode(), badKey.Body, "invalid_experiment"); !slices.Equal(got, []string{"/key invalid_key"}) {
		t.Errorf("violations = %v", got)
	}
	if keys := experimentKeys(t, f.admin, f.project.Key); !slices.Equal(keys, []string{"old", "source"}) {
		t.Errorf("experiments = %v, want the two created", keys)
	}
}

func TestListingExperimentsFiltersByStatusFlagOwnerTagAndArchived(t *testing.T) {
	f := startExperiments(t)
	createFlag(t, f.admin, f.project.Key, management.FlagCreate{Key: "other_flag", Type: management.FlagTypeBoolean, Name: "Other"})
	devToken := testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(f.project), access.RoleDeveloper)
	dev := testkit.Client(t, f.srv, devToken)
	adminID := me(t, f.srv, testkit.InstanceAdminToken(t)).Member.Id
	devID := me(t, f.srv, devToken).Member.Id

	createExperiment(t, f.admin, f.project.Key, management.ExperimentCreate{
		Key: "a_checkout", Name: "A", Tags: &[]string{"web", "checkout"}, Flag: "checkout_title", UnitType: "user", Variations: weights("a", 5000, "b", 5000),
	})
	createExperiment(t, dev, f.project.Key, management.ExperimentCreate{
		Key: "b_mobile", Name: "B", Tags: &[]string{"mobile"}, Flag: "checkout_title", UnitType: "user", Variations: weights("a", 5000, "b", 5000),
	})
	createExperiment(t, f.admin, f.project.Key, management.ExperimentCreate{
		Key: "c_other", Name: "C", Tags: &[]string{"web"}, Flag: "other_flag", UnitType: "user", Variations: weights("true", 5000, "false", 5000),
	})
	putInStatus(t, f, "c_other", experiments.StatusArchived)

	keys := func(what string, params management.ListExperimentsParams) []string {
		t.Helper()
		var all []string
		for {
			res := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &params))
			if res.JSON200 == nil {
				t.Fatalf("%s: GET experiments = %d: %s", what, res.StatusCode(), res.Body)
			}
			for _, e := range res.JSON200.Items {
				all = append(all, e.Key)
			}
			if params.Cursor = res.JSON200.NextCursor; params.Cursor == nil {
				return all
			}
		}
	}
	draft, running, archived := management.ExperimentStatusDraft, management.ExperimentStatusRunning, management.ExperimentStatusArchived
	for _, c := range []struct {
		what   string
		params management.ListExperimentsParams
		want   []string
	}{
		{"no filter hides the archived", management.ListExperimentsParams{}, []string{"a_checkout", "b_mobile"}},
		{"include_archived", management.ListExperimentsParams{IncludeArchived: ptr(true)}, []string{"a_checkout", "b_mobile", "c_other"}},
		{"status draft", management.ListExperimentsParams{Status: &draft}, []string{"a_checkout", "b_mobile"}},
		{"status running", management.ListExperimentsParams{Status: &running}, nil},
		{"status archived alone", management.ListExperimentsParams{Status: &archived}, []string{"c_other"}},
		{"flag", management.ListExperimentsParams{Flag: ptr("checkout_title")}, []string{"a_checkout", "b_mobile"}},
		{"a flag the project lacks", management.ListExperimentsParams{Flag: ptr("absent")}, nil},
		{"owner", management.ListExperimentsParams{Owner: &devID}, []string{"b_mobile"}},
		{"owner admin", management.ListExperimentsParams{Owner: &adminID}, []string{"a_checkout"}},
		{"tag", management.ListExperimentsParams{Tag: ptr("web")}, []string{"a_checkout"}},
		{"tag with archived", management.ListExperimentsParams{Tag: ptr("web"), IncludeArchived: ptr(true)}, []string{"a_checkout", "c_other"}},
		{"several filters", management.ListExperimentsParams{Flag: ptr("checkout_title"), Owner: &devID, Tag: ptr("mobile")}, []string{"b_mobile"}},
		{"a tag matches exactly", management.ListExperimentsParams{Tag: ptr("web ")}, nil},
		// Page by page, a filter narrows every page, not only the first.
		{"flag one per page", management.ListExperimentsParams{Flag: ptr("checkout_title"), Limit: ptr(1)}, []string{"a_checkout", "b_mobile"}},
		{"tag with archived one per page", management.ListExperimentsParams{Tag: ptr("web"), IncludeArchived: ptr(true), Limit: ptr(1)}, []string{"a_checkout", "c_other"}},
		{"owner one per page", management.ListExperimentsParams{Owner: &adminID, IncludeArchived: ptr(true), Limit: ptr(1)}, []string{"a_checkout", "c_other"}},
	} {
		if got := keys(c.what, c.params); !slices.Equal(got, c.want) {
			t.Errorf("%s: experiments = %v, want %v", c.what, got, c.want)
		}
	}

	// Paging keeps the filters: the same two pages whether or not they are given each time.
	first := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &management.ListExperimentsParams{Limit: ptr(1), IncludeArchived: ptr(true)}))
	if first.JSON200 == nil || len(first.JSON200.Items) != 1 || first.JSON200.NextCursor == nil {
		t.Fatalf("first page = %d %s", first.StatusCode(), first.Body)
	}
	rest := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &management.ListExperimentsParams{Limit: ptr(5), IncludeArchived: ptr(true), Cursor: first.JSON200.NextCursor}))
	if rest.JSON200 == nil || len(rest.JSON200.Items) != 2 || rest.JSON200.Items[0].Key != "b_mobile" || rest.JSON200.NextCursor != nil {
		t.Errorf("second page = %d %s", rest.StatusCode(), rest.Body)
	}

	bad := management.ExperimentStatus("paused")
	res := do(f.admin.ListExperimentsWithResponse(t.Context(), f.project.Key, &management.ListExperimentsParams{Status: &bad}))
	assertProblem(t, "an unknown status", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
}
