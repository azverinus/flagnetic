package management_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// mergeOptions are what a merge asks besides its document.
type mergeOptions struct {
	dryRun  bool
	ifMatch string
}

// mergeInto sends doc as JSON to merge it into project.
func mergeInto(t *testing.T, c *management.ClientWithResponses, project string, doc any, opts ...mergeOptions,
) *management.ImportProjectResponse {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return mergeBody(t, c, project, "application/json", body, opts...)
}

func mergeBody(t *testing.T, c *management.ClientWithResponses, project, contentType string, body []byte, opts ...mergeOptions,
) *management.ImportProjectResponse {
	t.Helper()
	params := management.ImportProjectParams{Mode: management.ImportProjectParamsModeMerge}
	for _, o := range opts {
		if o.dryRun {
			params.DryRun = ptr(true)
		}
		if o.ifMatch != "" {
			params.IfMatch = ptr(o.ifMatch)
		}
	}
	return do(c.ImportProjectWithBodyWithResponse(t.Context(), project, &params, contentType, bytes.NewReader(body)))
}

// mustMerge merges doc, failing unless the answer is 200.
func mustMerge(t *testing.T, c *management.ClientWithResponses, project string, doc any, opts ...mergeOptions) management.ImportResult {
	t.Helper()
	res := mergeInto(t, c, project, doc, opts...)
	if res.JSON200 == nil {
		t.Fatalf("merge = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// noChanges is the answer of an import that did nothing.
const noChanges = `{"created": [], "updated": [], "deleted": []}`

func assertNothingChanged(t *testing.T, r management.ImportResult) {
	t.Helper()
	for name, k := range map[string]management.ImportKindChanges{
		"environments": r.Changes.Environments, "unit types": r.Changes.UnitTypes, "attributes": r.Changes.Attributes,
		"segments": r.Changes.Segments, "flags": r.Changes.Flags, "experiments": r.Changes.Experiments,
	} {
		assertJSON(t, name, k, noChanges)
	}
}

// configurationOf returns the configuration of the flag in env of a document
// read as generic JSON.
func documentConfigurationOf(d map[string]any, flag, env string) map[string]any {
	for _, f := range d["flags"].([]any) {
		if m := f.(map[string]any); m["key"] == flag {
			return m["configurations"].(map[string]any)[env].(map[string]any)
		}
	}
	return nil
}

func TestMergingTheProjectsOwnExportChangesNothingAndWritesNothing(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := export(t, f.admin, k, "json")
	versions, actions := rulesetVersions(t, f.project), auditActions(t, f.admin, k)

	r := mustMerge(t, f.admin, k, json.RawMessage(doc.Body))
	if r.Mode != management.ImportResultModeMerge || r.Source != management.ImportResultSourceDocument || r.DryRun || r.Project.Key != k {
		t.Errorf("result = %+v", r)
	}
	assertNothingChanged(t, r)
	assertJSON(t, "warnings", r.Warnings, `[]`)
	if got := bumped(t, "an import that changes nothing", versions, rulesetVersions(t, f.project)); len(got) != 0 {
		t.Errorf("rulesets moved on: %v", got)
	}
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("audit grew from %v to %v, want nothing recorded", actions, got)
	}

	// The same through YAML, which is the same document.
	yamlDocument := export(t, f.admin, k, "yaml")
	if res := mergeBody(t, f.admin, k, "application/yaml", yamlDocument.Body); res.JSON200 == nil {
		t.Fatalf("merge of the YAML export = %d: %s", res.StatusCode(), res.Body)
	} else {
		assertNothingChanged(t, *res.JSON200)
	}
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("audit after YAML = %v, want nothing recorded", got)
	}
}

func TestAMergeUpsertsWhatTheDocumentNamesAndLeavesTheRest(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	before := rulesetVersions(t, f.project)

	doc["project"].(map[string]any)["name"] = "Renamed by import"
	doc["environments"] = append(doc["environments"].([]any), map[string]any{"key": "canary", "name": "Canary", "protected": false})
	entry(t, doc, "environments", "qa")["name"] = "Quality"
	entry(t, doc, "segments", "pro")["conditions"] = []any{map[string]any{"kind": "attribute", "attribute": "plan", "op": "eq", "value": "enterprise"}}
	entry(t, doc, "flags", "matching_v2")["name"] = "Matching, v2"
	documentConfigurationOf(doc, "banner_text", "staging")["fallthrough"] = "b"
	doc["flags"] = append(doc["flags"].([]any), map[string]any{
		"key": "extra", "type": "boolean", "name": "Extra", "configurations": map[string]any{
			"canary": map[string]any{"on": true, "off_variation": "false", "fallthrough": "true",
				"rules": []any{map[string]any{"conditions": []any{}, "serve": map[string]any{"kind": "rollout", "variation": "true", "unit_type": "user", "percentage": 2500}}}},
		},
	})

	r := mustMerge(t, f.admin, k, doc)
	if r.Project.Name != "Renamed by import" {
		t.Errorf("project = %+v, want the new name", r.Project)
	}
	assertJSON(t, "environments", []any{r.Changes.Environments.Created, keysOfUpdates(r.Changes.Environments.Updated)}, `[["canary"], ["qa"]]`)
	assertJSON(t, "segments", []any{r.Changes.Segments.Created, keysOfUpdates(r.Changes.Segments.Updated)}, `[[], ["pro"]]`)
	assertJSON(t, "flags", []any{r.Changes.Flags.Created, keysOfUpdates(r.Changes.Flags.Updated)}, `[["extra"], ["banner_text", "matching_v2"]]`)
	assertJSON(t, "nothing deleted", []any{r.Changes.Flags.Deleted, r.Changes.Environments.Deleted}, `[[], []]`)
	assertJSON(t, "the patch of a renamed environment", updateOf(t, r.Changes.Environments.Updated, "qa").Patch,
		`[{"op": "replace", "path": "/name", "value": "Quality"}]`)
	patch := asJSON(t, updateOf(t, r.Changes.Flags.Updated, "banner_text").Patch)
	if want := `[{"op":"replace","path":"/configurations/staging/fallthrough","value":"b"}]`; patch != want {
		t.Errorf("the patch of banner_text = %s, want %s", patch, want)
	}
	assertJSON(t, "warnings", r.Warnings, `[]`)

	// What was applied is what the document says, and the rest is as it was.
	after := exported(t, f.admin, k)
	if after["project"].(map[string]any)["name"] != "Renamed by import" || after["project"].(map[string]any)["key"] != k {
		t.Errorf("project = %v", after["project"])
	}
	assertJSON(t, "environments", keysOf(after, "environments"), `["development", "staging", "production", "qa", "canary"]`)
	assertJSON(t, "flags", keysOf(after, "flags"), `["banner_text", "extra", "matching_v2", "new_matching", "old_flag"]`)
	if entry(t, after, "flags", "matching_v2")["name"] != "Matching, v2" || documentConfigurationOf(after, "banner_text", "staging")["fallthrough"] != "b" {
		t.Error("an update was not applied")
	}
	assertJSON(t, "new_matching is as it was", documentConfigurationOf(after, "new_matching", "production"), asJSON(t, documentConfigurationOf(exported(t, f.admin, k), "new_matching", "production")))

	// Rule ids and salts the document lacked were minted, and a flag that
	// existed has the new environment, switched off.
	rule := documentConfigurationOf(after, "extra", "canary")["rules"].([]any)[0].(map[string]any)
	if rule["id"] == "" || rule["serve"].(map[string]any)["salt"] == "" {
		t.Errorf("rule = %v, want an identifier and a salt minted", rule)
	}
	if c := documentConfigurationOf(after, "matching_v2", "canary"); c == nil || c["on"] != false {
		t.Errorf("matching_v2 in canary = %v, want the initial configuration, off", c)
	}
	if c := documentConfigurationOf(after, "extra", "development"); c == nil || c["on"] != false {
		t.Errorf("extra in development = %v, want the initial configuration, off", c)
	}

	// A flag or a segment written moves every environment on; the new one
	// starts moved on too.
	moved := bumped(t, "the merge", before, rulesetVersions(t, f.project))
	slices.Sort(moved)
	assertJSON(t, "environments moved on", moved, `["canary", "development", "production", "qa", "staging"]`)
}

func keysOfUpdates(us []management.ImportUpdate) []string {
	out := []string{}
	for _, u := range us {
		out = append(out, u.Key)
	}
	slices.Sort(out)
	return out
}

func updateOf(t *testing.T, us []management.ImportUpdate, key string) management.ImportUpdate {
	t.Helper()
	for _, u := range us {
		if u.Key == key {
			return u
		}
	}
	t.Fatalf("no update of %q in %v", key, us)
	return management.ImportUpdate{}
}

func TestADocumentWithOneSectionLeavesTheOthersUntouched(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	before := exported(t, f.admin, k)

	r := mustMerge(t, f.admin, k, map[string]any{"schema_version": 1,
		"segments": []any{map[string]any{"key": "contractors", "name": "Contractors", "conditions": []any{}}}})
	assertJSON(t, "segments created", r.Changes.Segments.Created, `["contractors"]`)
	assertNothingChanged(t, management.ImportResult{Changes: management.ImportChanges{
		Environments: r.Changes.Environments, UnitTypes: r.Changes.UnitTypes, Attributes: r.Changes.Attributes,
		Segments: management.ImportKindChanges{Created: []string{}, Updated: []management.ImportUpdate{}, Deleted: []string{}},
		Flags:    r.Changes.Flags, Experiments: r.Changes.Experiments,
	}})
	after := exported(t, f.admin, k)
	assertJSON(t, "segments", keysOf(after, "segments"), `["contractors", "employees", "pro"]`)
	delete(before, "segments")
	delete(after, "segments")
	assertJSON(t, "the rest of the document", after, asJSON(t, before))
}

func TestAHandWrittenDocumentWithoutIdentifiersOrSaltsMergesAsNoChangeTheSecondTime(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := map[string]any{"schema_version": 1, "flags": []any{
		map[string]any{"key": "gradual", "type": "boolean", "name": "Gradual", "configurations": map[string]any{
			"staging": map[string]any{"on": true, "off_variation": "false", "fallthrough": "false", "rules": []any{
				map[string]any{"conditions": []any{}, "serve": map[string]any{"kind": "rollout", "variation": "true", "unit_type": "user", "percentage": 3000}},
			}},
		}},
	}}
	first := mustMerge(t, f.admin, k, doc)
	assertJSON(t, "created", first.Changes.Flags.Created, `["gradual"]`)
	rule := documentConfigurationOf(exported(t, f.admin, k), "gradual", "staging")["rules"].([]any)[0].(map[string]any)
	before := rulesetVersions(t, f.project)

	second := mustMerge(t, f.admin, k, doc)
	assertNothingChanged(t, second)
	again := documentConfigurationOf(exported(t, f.admin, k), "gradual", "staging")["rules"].([]any)[0].(map[string]any)
	if asJSON(t, again) != asJSON(t, rule) {
		t.Errorf("rule after the second merge = %v, want the one minted by the first, %v", again, rule)
	}
	if got := bumped(t, "the second merge", before, rulesetVersions(t, f.project)); len(got) != 0 {
		t.Errorf("rulesets moved on: %v", got)
	}
}

func TestADryRunAnswersWhatAMergeWouldDoAndWritesNothing(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	entry(t, doc, "flags", "matching_v2")["name"] = "Matching, v2"
	versions, actions, before := rulesetVersions(t, f.project), auditActions(t, f.admin, k), exported(t, f.admin, k)

	dry := mergeInto(t, f.admin, k, doc, mergeOptions{dryRun: true})
	if dry.JSON200 == nil || !dry.JSON200.DryRun {
		t.Fatalf("dry run = %d: %s", dry.StatusCode(), dry.Body)
	}
	assertJSON(t, "flags updated", keysOfUpdates(dry.JSON200.Changes.Flags.Updated), `["matching_v2"]`)
	if got := bumped(t, "a dry run", versions, rulesetVersions(t, f.project)); len(got) != 0 {
		t.Errorf("a dry run moved %v on", got)
	}
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("a dry run recorded %v", got)
	}
	assertJSON(t, "the project after a dry run", exported(t, f.admin, k), asJSON(t, before))

	// The same import applied answers the same.
	applied := mustMerge(t, f.admin, k, doc)
	if asJSON(t, applied.Changes) != asJSON(t, dry.JSON200.Changes) || asJSON(t, applied.Warnings) != asJSON(t, dry.JSON200.Warnings) {
		t.Errorf("applied %s, dry run %s", asJSON(t, applied.Changes), asJSON(t, dry.JSON200.Changes))
	}
}

func TestAnOwnerMergesIntoAProtectedEnvironmentWhereADirectWriteIsRefused(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	pool := testkit.Pool(t)
	owner := testkit.Client(t, f.srv, testkit.GrantedToken(t, pool, projectIDOf(f.project), access.RoleOwner))

	c := getConfiguration(t, owner, k, "matching_v2", "production")
	put := asPut(c)
	put.Fallthrough = "false"
	if res := putConfiguration(t, owner, k, "matching_v2", "production", c.Version, put); res.StatusCode() != http.StatusForbidden {
		t.Fatalf("a direct write to the protected production = %d: %s, want it refused", res.StatusCode(), res.Body)
	}

	doc := exported(t, f.admin, k)
	documentConfigurationOf(doc, "matching_v2", "production")["fallthrough"] = "false"
	r := mustMerge(t, owner, k, doc)
	assertJSON(t, "flags updated", keysOfUpdates(r.Changes.Flags.Updated), `["matching_v2"]`)
	if got := documentConfigurationOf(exported(t, f.admin, k), "matching_v2", "production")["fallthrough"]; got != "false" {
		t.Errorf("fallthrough in production = %v, want the imported false", got)
	}
}

func TestOnlyAnOwnerMerges(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	pool := testkit.Pool(t)
	doc := exported(t, f.admin, k)
	entry(t, doc, "flags", "matching_v2")["name"] = "Changed"

	for name, c := range map[string]*management.ClientWithResponses{
		"a maintainer":                 testkit.Client(t, f.srv, testkit.GrantedToken(t, pool, projectIDOf(f.project), access.RoleMaintainer)),
		"a project token of the owner": testkit.Client(t, f.srv, testkit.ProjectToken(t, pool, projectIDOf(f.project), access.RoleOwner)),
		"a member without a grant":     testkit.Client(t, f.srv, testkit.MemberToken(t, pool)),
	} {
		res := mergeInto(t, c, k, doc)
		if res.StatusCode() == http.StatusOK {
			t.Errorf("%s merged", name)
			continue
		}
		if name != "a member without a grant" {
			pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
			if pr.Permission == nil || *pr.Permission != "project.import" {
				t.Errorf("%s: permission = %v, want project.import", name, pr.Permission)
			}
		}
	}
	if got := entry(t, exported(t, f.admin, k), "flags", "matching_v2")["name"]; got == "Changed" {
		t.Error("a refused merge was applied")
	}
}

func TestAnImportNeverChangesWhetherAnExistingEnvironmentIsProtected(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	entry(t, doc, "environments", "staging")["protected"] = true
	entry(t, doc, "environments", "production")["protected"] = false
	doc["environments"] = append(doc["environments"].([]any), map[string]any{"key": "canary", "name": "Canary", "protected": true})

	r := mustMerge(t, f.admin, k, doc)
	assertJSON(t, "environments created", r.Changes.Environments.Created, `["canary"]`)
	assertJSON(t, "no environment updated", r.Changes.Environments.Updated, `[]`)
	var got []string
	for _, w := range r.Warnings {
		if w.Code != "protection_kept" {
			t.Errorf("warning %+v, want protection_kept alone", w)
		}
		got = append(got, w.Pointer)
	}
	if want := []string{"/environments/1/protected", "/environments/2/protected"}; !slices.Equal(got, want) {
		t.Errorf("warning pointers = %v, want %v", got, want)
	}
	after := exported(t, f.admin, k)
	for env, want := range map[string]bool{"staging": false, "production": true, "canary": true} {
		if got := entry(t, after, "environments", env)["protected"]; got != want {
			t.Errorf("%s protected = %v, want %v", env, got, want)
		}
	}
}

func TestOwnersWhoCannotOwnAreReplacedOnNewFlagsAndKeptOnExistingOnes(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	was := entry(t, doc, "flags", "matching_v2")["owner"]
	entry(t, doc, "flags", "matching_v2")["owner"] = "nobody@example.com"
	doc["flags"] = append(doc["flags"].([]any), map[string]any{
		"key": "extra", "type": "boolean", "name": "Extra", "owner": "stranger@example.com", "configurations": map[string]any{}})

	r := mustMerge(t, f.admin, k, doc)
	assertJSON(t, "flags", []any{r.Changes.Flags.Created, r.Changes.Flags.Updated}, `[["extra"], []]`)
	emails := []string{}
	for _, w := range r.Warnings {
		if w.Code != "owner_replaced" {
			t.Errorf("warning %+v, want owner_replaced alone", w)
		}
		emails = append(emails, (*w.Params)["email"].(string))
	}
	slices.Sort(emails)
	if want := []string{"nobody@example.com", "stranger@example.com"}; !slices.Equal(emails, want) {
		t.Errorf("warned emails = %v, want %v", emails, want)
	}
	after := exported(t, f.admin, k)
	if got := entry(t, after, "flags", "matching_v2")["owner"]; got != was {
		t.Errorf("an existing flag's owner = %v, want it kept as %v", got, was)
	}
	if got := entry(t, after, "flags", "extra")["owner"]; got != was {
		t.Errorf("a new flag's owner = %v, want the importer, %v", got, was)
	}
}

func TestAnOverrideKeepsItsAuthorWhenTheDocumentLeavesItAsItWasAndTheImporterOtherwise(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	pool := testkit.Pool(t)
	owner := testkit.Client(t, f.srv, testkit.GrantedToken(t, pool, projectIDOf(f.project), access.RoleOwner))
	author := func() map[string]string {
		c := getConfiguration(t, f.admin, k, "new_matching", "production")
		out := map[string]string{}
		for _, o := range c.Overrides {
			out[o.UnitId] = o.AddedBy.String()
		}
		return out
	}
	was := author()

	doc := exported(t, f.admin, k)
	prod := documentConfigurationOf(doc, "new_matching", "production")
	prod["overrides"] = append(prod["overrides"].([]any), map[string]any{"unit_type": "user", "unit_id": "qa-8", "variation": "true", "note": ""})
	mustMerge(t, owner, k, doc)
	got := author()
	if got["qa-7"] != was["qa-7"] {
		t.Errorf("the author of the override left as it was = %s, want %s", got["qa-7"], was["qa-7"])
	}
	if got["qa-8"] == "" || got["qa-8"] == was["qa-7"] {
		t.Errorf("the author of the new override = %q, want the importing owner", got["qa-8"])
	}
	if got["qa-8"] != author()["qa-8"] {
		t.Error("the author moved")
	}
}

func TestADryRunsETagIsTheProjectsStateAndAnImportBasedOnAnOldOneIsRefusedWhole(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	entry(t, doc, "flags", "matching_v2")["name"] = "Matching, v2"

	exportTag := export(t, f.admin, k, "json").HTTPResponse.Header.Get("ETag")
	dry := mergeInto(t, f.admin, k, doc, mergeOptions{dryRun: true})
	tag := dry.HTTPResponse.Header.Get("ETag")
	if tag == "" || tag != exportTag {
		t.Fatalf("a dry run's ETag = %q, the export's %q, want the same state", tag, exportTag)
	}

	// Another change to the project, whatever it touches, moves the state on.
	createSegment(t, f.admin, k, management.SegmentCreate{Key: "meanwhile", Name: "Meanwhile", Conditions: conditions(attr("plan", "eq", `"x"`))})
	actions := auditActions(t, f.admin, k)
	res := mergeInto(t, f.admin, k, doc, mergeOptions{ifMatch: tag})
	assertProblem(t, "an import on a project that moved", res.StatusCode(), res.Body, http.StatusPreconditionFailed, "version_mismatch")
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("a refused import recorded %v", got)
	}
	if got := entry(t, exported(t, f.admin, k), "flags", "matching_v2")["name"]; got == "Matching, v2" {
		t.Error("a refused import was applied")
	}

	// Based on the state it has now, it applies; and then that state is old.
	now := mergeInto(t, f.admin, k, doc, mergeOptions{dryRun: true}).HTTPResponse.Header.Get("ETag")
	if res := mergeInto(t, f.admin, k, doc, mergeOptions{ifMatch: now}); res.JSON200 == nil {
		t.Fatalf("an import on the state it has = %d: %s", res.StatusCode(), res.Body)
	}
	again := mergeInto(t, f.admin, k, doc, mergeOptions{ifMatch: now})
	assertProblem(t, "the same state again", again.StatusCode(), again.Body, http.StatusPreconditionFailed, "version_mismatch")

	// Without the header it applies to the project as it is, and a request
	// that is not valid is judged before the state.
	if res := mergeInto(t, f.admin, k, doc); res.JSON200 == nil {
		t.Fatalf("an import without If-Match = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestAMergeRecordsEachEntityItChangesAndOneEntryForTheImport(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	doc["environments"] = append(doc["environments"].([]any), map[string]any{"key": "canary", "name": "Canary", "protected": false})
	entry(t, doc, "segments", "pro")["name"] = "Professionals"
	entry(t, doc, "flags", "matching_v2")["description"] = "Described."
	documentConfigurationOf(doc, "banner_text", "staging")["fallthrough"] = "b"
	doc["flags"] = append(doc["flags"].([]any), map[string]any{"key": "extra", "type": "boolean", "name": "Extra", "configurations": map[string]any{}})
	known := len(auditActions(t, f.admin, k))

	mustMerge(t, f.admin, k, doc)
	res := do(f.admin.ListProjectAuditEntriesWithResponse(t.Context(), k, &management.ListProjectAuditEntriesParams{Limit: ptr(200)}))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d: %s", res.StatusCode(), res.Body)
	}
	written := res.JSON200.Items[:len(res.JSON200.Items)-known]
	var actions []string
	requests := map[string]bool{}
	for _, e := range written {
		actions = append(actions, e.Action)
		if e.RequestId == nil {
			t.Fatalf("an entry without a request: %+v", e)
		}
		requests[*e.RequestId] = true
	}
	slices.Sort(actions)
	want := []string{"environment.created", "flag.configuration_updated", "flag.created", "flag.updated", "project.imported", "segment.updated"}
	if !slices.Equal(actions, want) {
		t.Errorf("entries = %v, want %v", actions, want)
	}
	if len(requests) != 1 {
		t.Errorf("entries span requests %v, want one", requests)
	}
	// The newest is the summary, recording what the import did.
	summary := written[0]
	if summary.Action != "project.imported" || summary.Entity.Kind != "project" {
		t.Fatalf("newest entry = %s of a %s, want the import's", summary.Action, summary.Entity.Kind)
	}
	after := summary.After.(map[string]any)
	if after["mode"] != "merge" || after["source"] != "document" {
		t.Errorf("summary = %v", after)
	}
	assertJSON(t, "counts of flags", after["counts"].(map[string]any)["flags"], `{"created": 1, "updated": 2, "deleted": 0}`)
}

func TestAnImportedConfigurationIsRevertedToWhole(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	doc := exported(t, f.admin, k)
	imported := documentConfigurationOf(doc, "banner_text", "staging")
	imported["fallthrough"] = "b"
	imported["rules"] = []any{map[string]any{"description": "Imported", "conditions": []any{},
		"serve": map[string]any{"kind": "rollout", "variation": "b", "unit_type": "user", "percentage": 4000}}}
	mustMerge(t, f.admin, k, doc)

	banner := getFlag(t, f.admin, k, "banner_text")
	entryAfterImport, afterImport := lastConfigurationEntry(t, f.admin, k, banner)
	if entryAfterImport.Action != "flag.configuration_updated" {
		t.Fatalf("newest entry = %s, want the configuration's", entryAfterImport.Action)
	}
	version := int64(afterImport["version"].(float64))
	importedConfiguration := getConfiguration(t, f.admin, k, "banner_text", "staging")

	changed := asPut(importedConfiguration)
	changed.Fallthrough = "a"
	*changed.Rules = (*changed.Rules)[:0]
	mustPut(t, f.admin, k, "banner_text", "staging", importedConfiguration.Version, changed)

	res := revert(t, f.admin, k, "banner_text", "staging", ifMatch(importedConfiguration.Version+1), version)
	if res.JSON200 == nil {
		t.Fatalf("revert = %d: %s", res.StatusCode(), res.Body)
	}
	if !sameTargeting(*res.JSON200, importedConfiguration) {
		t.Errorf("reverted = %+v, want the imported %+v, its rule's identifier and salt included", *res.JSON200, importedConfiguration)
	}
}

func TestAMergeSupersedesTheChangeRequestsItMovesAndNoOther(t *testing.T) {
	f := startChangeRequests(t)
	k := f.project.Key
	stringFlag(t, f.admin, k, "moved")
	stringFlag(t, f.admin, k, "kept")
	proposal := management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "c"}
	onMoved := openedOn(t, f.dev.ClientWithResponses, k, "moved", "staging", proposal)
	onKept := openedOn(t, f.dev.ClientWithResponses, k, "kept", "staging", proposal)
	elsewhere := openedOn(t, f.dev.ClientWithResponses, k, "moved", "development", proposal)

	doc := exported(t, f.admin, k)
	documentConfigurationOf(doc, "moved", "staging")["fallthrough"] = "b"
	mustMerge(t, f.admin, k, doc)

	written := lastEntryOf(t, f.srv, getFlag(t, f.admin, k, "moved").Id)
	assertSupersededBy(t, f, onMoved, written)
	for name, cr := range map[string]management.ChangeRequest{"another flag's": onKept, "another environment's": elsewhere} {
		if got := getChangeRequest(t, f.dev.ClientWithResponses, k, cr); got.State != management.ChangeRequestStateOpen {
			t.Errorf("%s change request: %s, want open", name, got.State)
		}
	}
}

func TestADocumentThatBreaksARuleIsRefusedWholeWithEveryFaultFound(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	before, actions := exported(t, f.admin, k), auditActions(t, f.admin, k)

	doc := exported(t, f.admin, k)
	entry(t, doc, "flags", "matching_v2")["name"] = "Fine"
	entry(t, doc, "flags", "new_matching")["type"] = "string"
	entry(t, doc, "attributes", "plan")["type"] = "number"
	documentConfigurationOf(doc, "banner_text", "staging")["fallthrough"] = "nowhere"
	entry(t, doc, "flags", "matching_v2")["configurations"].(map[string]any)["ghost"] = map[string]any{"on": false, "off_variation": "false", "fallthrough": "false"}
	doc["experiments"] = []any{map[string]any{"key": "e1"}}

	res := mergeInto(t, f.admin, k, doc)
	pr := assertProblem(t, "a document that breaks rules", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_document")
	var got []string
	for _, e := range *pr.Errors {
		got = append(got, e.Pointer+" "+e.Code)
	}
	for _, want := range []string{
		"/attributes/0/type type_immutable", "/experiments/0 unsupported", "/flags/2/type type_immutable",
		"/flags/1/configurations/ghost unknown_environment",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("faults %v lack %q", got, want)
		}
	}
	if !slices.ContainsFunc(got, func(g string) bool { return strings.HasPrefix(g, "/flags/0/configurations/staging/fallthrough ") }) {
		t.Errorf("faults %v lack the unknown variation of banner_text's fallthrough", got)
	}
	assertJSON(t, "the project after a refusal", exported(t, f.admin, k), asJSON(t, before))
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("a refused import recorded %v", got)
	}
}

func TestMergeRefusesWhatIsNotADocumentAndAnUnknownProject(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	if res := mergeBody(t, f.admin, k, "text/plain", []byte(`{}`)); res.StatusCode() != http.StatusUnsupportedMediaType {
		t.Errorf("another media type = %d: %s", res.StatusCode(), res.Body)
	}
	if res := mergeBody(t, f.admin, k, "application/json", []byte(`{"schema_version": 1,`)); res.StatusCode() != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d: %s", res.StatusCode(), res.Body)
	}
	if res := mergeBody(t, f.admin, k, "application/yaml", []byte("schema_version: [")); res.StatusCode() != http.StatusBadRequest {
		t.Errorf("malformed YAML = %d: %s", res.StatusCode(), res.Body)
	}
	if res := mergeInto(t, f.admin, k, map[string]any{"schema_version": 7}); res.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("another schema version = %d: %s", res.StatusCode(), res.Body)
	}
	if res := mergeInto(t, f.admin, k, map[string]any{"schema_version": 1, "colour": "red"}); res.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("an unknown field = %d: %s", res.StatusCode(), res.Body)
	}
	if res := mergeInto(t, f.admin, projectKey(), map[string]any{"schema_version": 1}); res.StatusCode() != http.StatusNotFound {
		t.Errorf("an unknown project = %d: %s", res.StatusCode(), res.Body)
	}
}
