package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// replaceInto sends doc as JSON to replace the configuration of project with it.
func replaceInto(t *testing.T, c *management.ClientWithResponses, project string, doc any, opts ...mergeOptions,
) *management.ImportProjectResponse {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return importBody(t, c, project, management.ImportProjectParamsModeReplace, "application/json", body, opts...)
}

// mustReplace replaces, failing unless the answer is 200.
func mustReplace(t *testing.T, c *management.ClientWithResponses, project string, doc any, opts ...mergeOptions) management.ImportResult {
	t.Helper()
	res := replaceInto(t, c, project, doc, opts...)
	if res.JSON200 == nil {
		t.Fatalf("replace = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// without removes the entries keyed keys from the list section of the document.
func without(d map[string]any, section string, keys ...string) {
	kept := []any{}
	for _, e := range d[section].([]any) {
		if !slices.Contains(keys, e.(map[string]any)["key"].(string)) {
			kept = append(kept, e)
		}
	}
	d[section] = kept
}

// withoutEnvironment removes an environment and every configuration in it.
func withoutEnvironment(d map[string]any, env string) {
	without(d, "environments", env)
	for _, f := range d["flags"].([]any) {
		delete(f.(map[string]any)["configurations"].(map[string]any), env)
	}
}

func TestAReplaceDeletesWhatTheDocumentLacksAndLeavesTheStandardAttributes(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	before := rulesetVersions(t, f.project)

	without(doc, "flags", "banner_text", "new_matching", "old_flag")
	without(doc, "segments", "employees", "pro")
	without(doc, "attributes", "plan")
	without(doc, "unit_types", "device")
	withoutEnvironment(doc, "qa")

	r := mustReplace(t, f.admin, k, doc)
	if r.Mode != management.ImportResultModeReplace || r.DryRun {
		t.Errorf("result = %+v", r)
	}
	assertJSON(t, "deleted", map[string]any{
		"environments": r.Changes.Environments.Deleted, "unit types": r.Changes.UnitTypes.Deleted,
		"attributes": r.Changes.Attributes.Deleted, "segments": r.Changes.Segments.Deleted, "flags": r.Changes.Flags.Deleted,
		"experiments": r.Changes.Experiments.Deleted,
	}, `{"environments": ["qa"], "unit types": ["device"], "attributes": ["plan"], "segments": ["employees", "pro"],
		"flags": ["banner_text", "new_matching", "old_flag"], "experiments": []}`)
	assertJSON(t, "nothing created or updated", []any{
		r.Changes.Flags.Created, keysOfUpdates(r.Changes.Flags.Updated), r.Changes.Environments.Created,
	}, `[[], [], []]`)

	// The project equals the document, and the standard attributes every
	// project has are still declared.
	assertJSON(t, "export after", exported(t, f.admin, k), asJSON(t, doc))
	res := do(f.admin.ListAttributesWithResponse(t.Context(), k))
	if res.JSON200 == nil {
		t.Fatalf("GET attributes = %d: %s", res.StatusCode(), res.Body)
	}
	var declared []string
	for _, a := range res.JSON200.Items {
		declared = append(declared, a.Key)
	}
	for _, standard := range []string{"app_version", "platform"} {
		if !slices.Contains(declared, standard) {
			t.Errorf("attributes = %v, want the standard attribute %s", declared, standard)
		}
	}
	if slices.Contains(declared, "plan") {
		t.Errorf("attributes = %v, want plan deleted", declared)
	}

	// Each deletion is recorded as the ordinary one is, then the import.
	actions := auditActions(t, f.admin, k)
	for _, want := range []string{
		"flag.archived", "flag.deleted", "segment.deleted", "attribute.deleted", "unit_type.deleted", "environment.deleted",
	} {
		if !slices.Contains(actions[:12], want) {
			t.Errorf("audit = %v, want %s recorded", actions[:12], want)
		}
	}
	if actions[0] != "project.imported" {
		t.Errorf("audit starts with %s, want project.imported", actions[0])
	}
	log := do(f.admin.ListProjectAuditEntriesWithResponse(t.Context(), k, &management.ListProjectAuditEntriesParams{Limit: ptr(1)}))
	if log.JSON200 == nil {
		t.Fatalf("GET audit = %d: %s", log.StatusCode(), log.Body)
	}
	after, _ := json.Marshal(log.JSON200.Items[0].After)
	assertJSON(t, "summary", json.RawMessage(after), `{
		"mode": "replace", "source": "document",
		"counts": {
			"environments": {"created": 0, "updated": 0, "deleted": 1}, "unit_types": {"created": 0, "updated": 0, "deleted": 1},
			"attributes": {"created": 0, "updated": 0, "deleted": 1}, "segments": {"created": 0, "updated": 0, "deleted": 2},
			"flags": {"created": 0, "updated": 0, "deleted": 3}, "experiments": {"created": 0, "updated": 0, "deleted": 0}
		}}`)

	// Every environment that is left moved on; the deleted one has no ruleset.
	if got := bumped(t, "a replace", before, rulesetVersions(t, f.project)); !slices.Equal(got, []string{"development", "production", "staging"}) {
		t.Errorf("rulesets moved on: %v", got)
	}

	// Replacing with the same document again does nothing.
	again := mustReplace(t, f.admin, k, doc)
	assertNothingChanged(t, again)
}

func TestADryRunOfAReplaceAnswersTheDeletionsAndWritesNothing(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	without(doc, "flags", "old_flag")
	withoutEnvironment(doc, "qa")
	exportBefore, actions := exported(t, f.admin, k), auditActions(t, f.admin, k)

	r := mustReplace(t, f.admin, k, doc, mergeOptions{dryRun: true})
	if !r.DryRun {
		t.Error("dry_run = false")
	}
	assertJSON(t, "deleted", []any{r.Changes.Flags.Deleted, r.Changes.Environments.Deleted}, `[["old_flag"], ["qa"]]`)
	assertJSON(t, "export", exported(t, f.admin, k), asJSON(t, exportBefore))
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("audit grew from %v to %v", actions, got)
	}
}

func TestAReplaceRefusesToDeleteWhatIsStillInUseListingAllOfIt(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	exportBefore, actions := exported(t, f.admin, k), auditActions(t, f.admin, k)

	for name, c := range map[string]struct {
		drop func(d map[string]any)
		want []string
	}{
		"a segment a rule serves": {
			func(d map[string]any) { without(d, "segments", "employees") },
			[]string{"flag new_matching production"},
		},
		"an attribute a segment names": {
			func(d map[string]any) { without(d, "attributes", "plan") },
			[]string{"segment pro "},
		},
		"a flag another requires": {
			func(d map[string]any) { without(d, "flags", "matching_v2") },
			[]string{"flag new_matching production"},
		},
		"a unit type an override names, and a segment, together": {
			func(d map[string]any) { without(d, "unit_types", "user") },
			nil,
		},
	} {
		doc := exported(t, f.admin, k)
		c.drop(doc)
		for _, dry := range []bool{true, false} {
			res := replaceInto(t, f.admin, k, doc, mergeOptions{dryRun: dry})
			refs := kindedReferencesOf(t, name, res.StatusCode(), res.Body)
			if len(refs) == 0 {
				t.Errorf("%s (dry run %v): no reference listed", name, dry)
			}
			for _, want := range c.want {
				if !slices.Contains(refs, want) {
					t.Errorf("%s (dry run %v): references = %v, want %q among them", name, dry, refs, want)
				}
			}
		}
	}
	assertJSON(t, "export", exported(t, f.admin, k), asJSON(t, exportBefore))
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("audit grew from %v to %v, want a refusal to record nothing", actions, got)
	}
}

func TestAReplaceNamesEveryReferenceOfEveryEntityItCannotDelete(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	without(doc, "segments", "employees", "pro")

	res := replaceInto(t, f.admin, k, doc)
	refs := kindedReferencesOf(t, "two segments in use", res.StatusCode(), res.Body)
	for _, want := range []string{"flag new_matching production", "flag banner_text staging"} {
		if !slices.Contains(refs, want) {
			t.Errorf("references = %v, want %q among them", refs, want)
		}
	}
}

func TestAReplaceIsRefusedWhileAnExperimentHoldsAnAbsentFlagInADryRunToo(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	without(doc, "flags", "checkout_title")
	exportBefore := exported(t, f.admin, k)

	for _, dry := range []bool{true, false} {
		res := replaceInto(t, f.admin, k, doc, mergeOptions{dryRun: dry})
		refs := kindedReferencesOf(t, "an absent flag held by experiments", res.StatusCode(), res.Body)
		for _, want := range transferExperimentKeys {
			if !slices.Contains(refs, "experiment "+want+" ") {
				t.Errorf("dry run %v: references = %v, want experiment %s among them", dry, refs, want)
			}
		}
	}
	assertJSON(t, "export", exported(t, f.admin, k), asJSON(t, exportBefore))
}

func TestAReplaceDeletesADraftExperimentTheDocumentLacks(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	without(doc, "experiments", "a_draft")

	r := mustReplace(t, f.admin, k, doc)
	assertJSON(t, "experiments", []any{r.Changes.Experiments.Deleted, r.Changes.Experiments.Created, keysOfUpdates(r.Changes.Experiments.Updated)},
		`[["a_draft"], [], []]`)
	if got := keysOf(exported(t, f.admin, k), "experiments"); !slices.Equal(got, []string{"b_running", "c_stopped", "d_archived"}) {
		t.Errorf("experiments after = %v", got)
	}
	if res := do(f.admin.GetExperimentWithResponse(t.Context(), k, "a_draft")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("GET a_draft = %d: %s, want 404", res.StatusCode(), res.Body)
	}
	if actions := auditActions(t, f.admin, k); !slices.Contains(actions[:3], "experiment.deleted") {
		t.Errorf("audit = %v, want experiment.deleted recorded", actions[:3])
	}
}

func TestAReplaceMayGiveTheSaltsOfADeletedDraftToANewExperiment(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	// Renaming the draft deletes it and creates another holding its salts.
	for _, e := range doc["experiments"].([]any) {
		if e := e.(map[string]any); e["key"] == "a_draft" {
			e["key"] = "e_draft"
		}
	}

	for _, dry := range []bool{true, false} {
		res := replaceInto(t, f.admin, k, doc, mergeOptions{dryRun: dry})
		if res.JSON200 == nil {
			t.Fatalf("dry run %v: replace = %d: %s", dry, res.StatusCode(), res.Body)
		}
		r := res.JSON200
		assertJSON(t, "experiments", []any{r.Changes.Experiments.Deleted, r.Changes.Experiments.Created}, `[["a_draft"], ["e_draft"]]`)
	}
	if got := keysOf(exported(t, f.admin, k), "experiments"); !slices.Equal(got, []string{"b_running", "c_stopped", "d_archived", "e_draft"}) {
		t.Errorf("experiments after = %v", got)
	}
}

func TestAReplaceRefusesAnExperimentThatStartedNamingEveryOne(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	without(doc, "experiments", "a_draft", "b_running", "c_stopped", "d_archived")
	for _, key := range []string{"b_running", "c_stopped", "d_archived"} {
		withoutRulesServing(doc, key)
	}

	for _, dry := range []bool{true, false} {
		res := replaceInto(t, f.admin, k, doc, mergeOptions{dryRun: dry})
		p := assertProblem(t, "experiments that started", res.StatusCode(), res.Body, http.StatusConflict, "not_draft")
		var named []string
		if p.Errors != nil {
			for _, v := range *p.Errors {
				named = append(named, v.Code+" "+(*v.Params)["key"].(string)+" "+(*v.Params)["status"].(string))
			}
		}
		want := []string{"not_draft b_running running", "not_draft c_stopped stopped", "not_draft d_archived archived"}
		if !slices.Equal(named, want) {
			t.Errorf("dry run %v: errors = %v, want %v", dry, named, want)
		}
	}
	// The draft is deleted by no refused import.
	for _, key := range transferExperimentKeys {
		if res := do(f.admin.GetExperimentWithResponse(t.Context(), k, key)); res.JSON200 == nil {
			t.Errorf("GET %s = %d: %s, want it kept", key, res.StatusCode(), res.Body)
		}
	}
}

func TestAReplaceNeverDeletesAnEnvironmentThatHoldsSDKKeys(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	key := mustCreateSDKKey(t, f.admin, k, "qa", management.SdkKeyCreate{Kind: "server", Name: "api"})
	doc := exported(t, f.admin, k)
	withoutEnvironment(doc, "qa")

	for _, dry := range []bool{true, false} {
		res := replaceInto(t, f.admin, k, doc, mergeOptions{dryRun: dry})
		refs := kindedReferencesOf(t, "an environment with an SDK key", res.StatusCode(), res.Body)
		if want := []string{"sdk_key " + key.Key.Id.String() + " qa"}; !slices.Equal(refs, want) {
			t.Errorf("dry run %v: references = %v, want %v", dry, refs, want)
		}
	}
	if got := keysOf(exported(t, f.admin, k), "environments"); !slices.Contains(got, "qa") {
		t.Errorf("environments after = %v, want qa kept", got)
	}

	// A key that is revoked authenticates no more, and no longer keeps it.
	if res := revoke(t, f.admin, k, "qa", key.Key.Id.String()); res.StatusCode() >= 300 {
		t.Fatalf("revoke = %d: %s", res.StatusCode(), res.Body)
	}
	r := mustReplace(t, f.admin, k, doc)
	assertJSON(t, "environments", r.Changes.Environments.Deleted, `["qa"]`)
}

func TestAReplaceDoesNotDeleteAProtectedEnvironment(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	withoutEnvironment(doc, "production")

	res := replaceInto(t, f.admin, k, doc)
	assertProblem(t, "a protected environment", res.StatusCode(), res.Body, http.StatusConflict, "environment_protected")
}

func TestAReplaceNeedsEverySectionAndAnEnvironment(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key

	res := replaceInto(t, f.admin, k, map[string]any{"schema_version": 1})
	got := violationsOf(t, "a partial document", res.StatusCode(), res.Body, "invalid_document")
	for _, want := range []string{"/project required", "/environments required", "/unit_types required", "/attributes required",
		"/segments required", "/flags required", "/experiments required"} {
		if !slices.Contains(got, want) {
			t.Errorf("violations = %v, want %s among them", got, want)
		}
	}

	doc := exported(t, f.admin, k)
	doc["environments"] = []any{}
	doc["flags"] = []any{}
	doc["experiments"] = []any{}
	res = replaceInto(t, f.admin, k, doc)
	if got = violationsOf(t, "no environment", res.StatusCode(), res.Body, "invalid_document"); !slices.Contains(got, "/environments invalid_value") {
		t.Errorf("violations = %v, want /environments invalid_value", got)
	}

	// A configuration for an environment the document does not name would be
	// written only to be deleted.
	doc = exported(t, f.admin, k)
	without(doc, "environments", "qa")
	res = replaceInto(t, f.admin, k, doc)
	got = violationsOf(t, "a configuration in a deleted environment", res.StatusCode(), res.Body, "invalid_document")
	if !slices.ContainsFunc(got, func(v string) bool { return strings.HasSuffix(v, " unknown_environment") }) {
		t.Errorf("violations = %v, want unknown_environment", got)
	}
}

func TestAReplaceDeletesAnEnvironmentBeforeWhatItsConfigurationsName(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	// Only banner_text's staging configuration serves the segment pro.
	withoutEnvironment(doc, "staging")
	without(doc, "segments", "pro")

	r := mustReplace(t, f.admin, k, doc)
	assertJSON(t, "deleted", []any{r.Changes.Environments.Deleted, r.Changes.Segments.Deleted}, `[["staging"], ["pro"]]`)
}
