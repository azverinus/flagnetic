package management_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// exportFixture is a project using every part of the document: an extra
// environment, a unit type, an attribute, a private and a public segment,
// flags with rules, a rollout, an override with a note, a prerequisite and an
// archived flag, and an attribute that was only discovered.
type exportFixture struct {
	srv     *testkit.Server
	admin   *management.ClientWithResponses
	project management.Project
}

func exportedProject(t *testing.T) exportFixture {
	t.Helper()
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key

	if res := do(admin.CreateEnvironmentWithResponse(t.Context(), k, management.EnvironmentCreate{Key: "qa", Name: "QA"})); res.JSON201 == nil {
		t.Fatalf("create qa: %d %s", res.StatusCode(), res.Body)
	}
	if res := do(admin.CreateUnitTypeWithResponse(t.Context(), k, management.UnitTypeCreate{Key: "device", Name: "Device"})); res.JSON201 == nil {
		t.Fatalf("create device: %d %s", res.StatusCode(), res.Body)
	}
	if res := do(admin.CreateAttributeWithResponse(t.Context(), k, management.AttributeCreate{
		Key: "plan", Type: management.AttributeTypeString, Name: "Plan", Description: ptr("The billing plan."),
	})); res.JSON201 == nil {
		t.Fatalf("create plan: %d %s", res.StatusCode(), res.Body)
	}
	createSegment(t, admin, k, management.SegmentCreate{Key: "employees", Name: "Employees", Private: ptr(true),
		Conditions: conditions(attr("user", "in", `["u1","u2"]`))})
	createSegment(t, admin, k, management.SegmentCreate{Key: "pro", Name: "Pro", Conditions: conditions(attr("plan", "eq", `"pro"`))})

	createFlag(t, admin, k, management.FlagCreate{Key: "matching_v2", Type: "boolean", Name: "Matching v2", Lifetime: ptr(management.Lifetime("permanent"))})
	createFlag(t, admin, k, management.FlagCreate{Key: "new_matching", Type: "boolean", Name: "New matching", Tags: &[]string{"mobile", "matching"}})
	createFlag(t, admin, k, management.FlagCreate{Key: "banner_text", Type: "string", Name: "Banner text",
		Description: ptr("The text on the banner."),
		Variations: &[]management.VariationInput{
			{Key: "a", Value: json.RawMessage(`"Hello"`)}, {Key: "b", Value: json.RawMessage(`"Hi"`), Name: ptr("Short")}}})
	old := createFlag(t, admin, k, management.FlagCreate{Key: "old_flag", Type: "boolean", Name: "Old flag"})
	if res := do(admin.ArchiveFlagWithResponse(t.Context(), k, "old_flag", &management.ArchiveFlagParams{IfMatch: ifMatch(old.Version)})); res.JSON200 == nil {
		t.Fatalf("archive: %d %s", res.StatusCode(), res.Body)
	}

	mustPut(t, admin, k, "matching_v2", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	mustPut(t, admin, k, "new_matching", "production", 1, management.FlagConfigurationPut{
		On: true, OffVariation: "false", Fallthrough: "false",
		Prerequisites: &[]management.Prerequisite{{Flag: "matching_v2", Variation: "true"}},
		Overrides:     &[]management.OverrideInput{{UnitType: "user", UnitId: "qa-7", Variation: "true", Note: ptr("Demo account")}},
		Rules: &[]management.RuleInput{
			{Description: ptr("Employees first"), Conditions: conditions(inSegment("employees")), Serve: serves("true")},
			{Conditions: conditions(attr("app_version", "gte", `"1.41.0"`)), Serve: rolloutOf("user", "true", 5000)},
		}})
	mustPut(t, admin, k, "banner_text", "staging", 1, management.FlagConfigurationPut{
		On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("pro")), Serve: serves("b")}}})
	setProtected(t, admin, k, "production", true)

	// An attribute that no one declared, seen in a context, becomes
	// discovered, which a document must not carry.
	mustEvaluate(t, admin, k, "staging", management.EvaluationRequest{
		Context: management.EvaluationContext{Attributes: &map[string]any{"favourite_colour": "green"}}})
	return exportFixture{srv: srv, admin: admin, project: p}
}

// export downloads the project's document in format ("" for the default).
func export(t *testing.T, c *management.ClientWithResponses, project, format string) *management.ExportProjectResponse {
	t.Helper()
	params := &management.ExportProjectParams{}
	if format != "" {
		params.Format = ptr(management.ExportProjectParamsFormat(format))
	}
	return do(c.ExportProjectWithResponse(t.Context(), project, params))
}

// exported downloads the document as JSON and decodes it as generic JSON, so
// that tests read it the way a person with a text editor would.
func exported(t *testing.T, c *management.ClientWithResponses, project string) map[string]any {
	t.Helper()
	res := export(t, c, project, "json")
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("export = %d: %s", res.StatusCode(), res.Body)
	}
	var d map[string]any
	if err := json.Unmarshal(res.Body, &d); err != nil {
		t.Fatalf("export is not JSON: %v\n%s", err, res.Body)
	}
	return d
}

// entry returns the element of the document's list section whose key is key.
func entry(t *testing.T, d map[string]any, section, key string) map[string]any {
	t.Helper()
	for _, e := range d[section].([]any) {
		if m := e.(map[string]any); m["key"] == key {
			return m
		}
	}
	t.Fatalf("%s has no %q", section, key)
	return nil
}

func keysOf(d map[string]any, section string) []string {
	var keys []string
	for _, e := range d[section].([]any) {
		keys = append(keys, e.(map[string]any)["key"].(string))
	}
	return keys
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertJSON(t *testing.T, name string, got any, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: want is not JSON: %v", name, err)
	}
	if g, x := asJSON(t, got), asJSON(t, w); g != x {
		t.Errorf("%s = %s, want %s", name, g, x)
	}
}

func TestExportHoldsTheProjectsConfigurationAndNothingElse(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key
	d := exported(t, f.admin, k)

	keys := make([]string, 0, len(d))
	for key := range d {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if want := []string{"attributes", "environments", "experiments", "flags", "project", "schema_version", "segments", "unit_types"}; !slices.Equal(keys, want) {
		t.Errorf("sections = %v, want %v: members, SDK keys, audit entries and discovered attributes must not appear", keys, want)
	}
	assertJSON(t, "schema_version", d["schema_version"], `1`)
	assertJSON(t, "project", d["project"], `{"key": "`+k+`", "name": "Project `+k+`", "description": ""}`)
	assertJSON(t, "experiments", d["experiments"], `[]`)
	assertJSON(t, "environments", d["environments"], `[
		{"key": "development", "name": "Development", "protected": false},
		{"key": "staging", "name": "Staging", "protected": false},
		{"key": "production", "name": "Production", "protected": true},
		{"key": "qa", "name": "QA", "protected": false}]`)
	assertJSON(t, "unit types", keysOf(d, "unit_types"), `["anonymous", "device", "user"]`)
	assertJSON(t, "device", entry(t, d, "unit_types", "device"), `{"key": "device", "name": "Device", "description": ""}`)
	// Only what was declared: not the standard attributes every project has,
	// and not the one that was discovered.
	assertJSON(t, "attributes", d["attributes"], `[{"key": "plan", "type": "string", "name": "Plan", "description": "The billing plan."}]`)
	assertJSON(t, "employees", entry(t, d, "segments", "employees"), `{"key": "employees", "name": "Employees", "description": "",
		"private": true, "conditions": [{"kind": "attribute", "attribute": "user", "op": "in", "value": ["u1", "u2"]}]}`)
	assertJSON(t, "segments", keysOf(d, "segments"), `["employees", "pro"]`)
	assertJSON(t, "flags", keysOf(d, "flags"), `["banner_text", "matching_v2", "new_matching", "old_flag"]`)

	// A flag carries its definition, its owner by email, whether it is
	// archived and its configuration in every environment, with the rule
	// identifiers and rollout salts the API shows.
	stored := getFlag(t, f.admin, k, "new_matching")
	fl := entry(t, d, "flags", "new_matching")
	assertJSON(t, "new_matching definition", map[string]any{
		"key": fl["key"], "type": fl["type"], "name": fl["name"], "description": fl["description"], "owner": fl["owner"],
		"tags": fl["tags"], "lifetime": fl["lifetime"], "automatic_exposure": fl["automatic_exposure"], "archived": fl["archived"],
		"variations": fl["variations"],
	}, `{"key": "new_matching", "type": "boolean", "name": "New matching", "description": "", "owner": "`+testkit.InstanceAdminEmail+`",
		"tags": ["mobile", "matching"], "lifetime": "temporary", "automatic_exposure": false, "archived": false,
		"variations": [{"key": "true", "value": true, "name": "true", "description": ""}, {"key": "false", "value": false, "name": "false", "description": ""}]}`)
	assertJSON(t, "new_matching expected_removal_at", fl["expected_removal_at"], asJSON(t, stored.ExpectedRemovalAt))
	configs := fl["configurations"].(map[string]any)
	assertJSON(t, "environments of new_matching", mapKeys(configs), `["development", "production", "qa", "staging"]`)

	live := getConfiguration(t, f.admin, k, "new_matching", "production")
	rules := configs["production"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 || len(live.Rules) != 2 {
		t.Fatalf("rules = %d, live %d, want 2", len(rules), len(live.Rules))
	}
	for i, r := range rules {
		if id := r.(map[string]any)["id"]; id != live.Rules[i].Id.String() {
			t.Errorf("rule %d id = %v, want %v, the one the API shows", i, id, live.Rules[i].Id)
		}
	}
	rollout := rules[1].(map[string]any)["serve"].(map[string]any)
	if rollout["salt"] != *live.Rules[1].Serve.Salt || rollout["percentage"] != float64(5000) || rollout["unit_type"] != "user" {
		t.Errorf("rollout = %v, want the salt the API shows", rollout)
	}
	assertJSON(t, "new_matching in production", map[string]any{
		"on": configs["production"].(map[string]any)["on"], "off_variation": configs["production"].(map[string]any)["off_variation"],
		"fallthrough":   configs["production"].(map[string]any)["fallthrough"],
		"prerequisites": configs["production"].(map[string]any)["prerequisites"], "overrides": configs["production"].(map[string]any)["overrides"],
	}, `{"on": true, "off_variation": "false", "fallthrough": "false",
		"prerequisites": [{"flag": "matching_v2", "variation": "true"}],
		"overrides": [{"unit_type": "user", "unit_id": "qa-7", "variation": "true", "note": "Demo account"}]}`)
	assertJSON(t, "new_matching in development", configs["development"],
		`{"on": false, "off_variation": "false", "fallthrough": "true", "prerequisites": [], "overrides": [], "rules": []}`)
	assertJSON(t, "old_flag archived", entry(t, d, "flags", "old_flag")["archived"], `true`)
	assertJSON(t, "banner_text", entry(t, d, "flags", "banner_text")["variations"],
		`[{"key": "a", "value": "Hello", "name": "a", "description": ""}, {"key": "b", "value": "Hi", "name": "Short", "description": ""}]`)

	// Who wrote an override, and every identifier but the rules', stay
	// behind.
	text := string(export(t, f.admin, k, "json").Body)
	for _, forbidden := range []string{"added_by", "updated_by", "created_by", "favourite_colour"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the document holds %q", forbidden)
		}
	}
	if regexp.MustCompile(`"(version|created_at|updated_at|sort_order)"`).MatchString(text) {
		t.Errorf("the document holds a version, a timestamp or a sort order of the API:\n%s", text)
	}
}

func TestExportIsTheSameBytesTwiceAndYAMLIsTheSameDocument(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key

	a, b := export(t, f.admin, k, "json"), export(t, f.admin, k, "json")
	if !bytes.Equal(a.Body, b.Body) {
		t.Errorf("two exports of an unchanged project differ")
	}
	if !bytes.HasSuffix(a.Body, []byte("}\n")) || !bytes.HasPrefix(a.Body, []byte("{\n  \"schema_version\": 1,\n")) {
		t.Errorf("JSON is not indented by two spaces and ended by a newline:\n%.100s", a.Body)
	}
	if got := a.HTTPResponse.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	// The default format is JSON.
	if c := export(t, f.admin, k, ""); !bytes.Equal(c.Body, a.Body) {
		t.Errorf("the default export is not JSON")
	}

	y := export(t, f.admin, k, "yaml")
	if y.StatusCode() != http.StatusOK || y.HTTPResponse.Header.Get("Content-Type") != "application/yaml" {
		t.Fatalf("yaml export = %d %q: %s", y.StatusCode(), y.HTTPResponse.Header.Get("Content-Type"), y.Body)
	}
	var fromYAML, fromJSON any
	if err := yaml.Unmarshal(y.Body, &fromYAML); err != nil {
		t.Fatalf("decode the YAML export: %v", err)
	}
	if err := json.Unmarshal(a.Body, &fromJSON); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, "YAML export", fromYAML, asJSON(t, fromJSON))
	// A YAML 1.1 reader would take a bare on for a boolean.
	if !bytes.Contains(y.Body, []byte(`"on": true`)) {
		t.Errorf("the YAML export does not quote the key on")
	}
	if z := export(t, f.admin, k, "yaml"); !bytes.Equal(y.Body, z.Body) {
		t.Errorf("two YAML exports differ")
	}
}

func TestExportNamesTheFileAndTheStateOfTheProject(t *testing.T) {
	f := exportedProject(t)
	k := f.project.Key

	res := export(t, f.admin, k, "yaml")
	disposition := res.HTTPResponse.Header.Get("Content-Disposition")
	if !regexp.MustCompile(`^attachment; filename="` + regexp.QuoteMeta(k) + `-\d{8}T\d{6}Z\.yaml"$`).MatchString(disposition) {
		t.Errorf("Content-Disposition = %q", disposition)
	}
	if json := export(t, f.admin, k, "json").HTTPResponse.Header.Get("Content-Disposition"); !strings.HasSuffix(json, `.json"`) {
		t.Errorf("Content-Disposition of the JSON export = %q", json)
	}

	// The ETag is the project's newest audit entry, quoted, and any change
	// to the project moves it.
	latest := func() string {
		entries := do(f.admin.ListProjectAuditEntriesWithResponse(t.Context(), k, &management.ListProjectAuditEntriesParams{}))
		if entries.JSON200 == nil || len(entries.JSON200.Items) == 0 {
			t.Fatalf("audit = %d %s", entries.StatusCode(), entries.Body)
		}
		return entries.JSON200.Items[0].Id.String()
	}
	etag := res.HTTPResponse.Header.Get("ETag")
	if etag != `"`+latest()+`"` {
		t.Errorf("ETag = %s, want the project's newest audit entry %s", etag, latest())
	}
	if again := export(t, f.admin, k, "json").HTTPResponse.Header.Get("ETag"); again != etag {
		t.Errorf("ETag of an unchanged project moved: %s then %s", etag, again)
	}
	createFlag(t, f.admin, k, management.FlagCreate{Key: "one_more", Type: "boolean", Name: "One more"})
	if moved := export(t, f.admin, k, "json").HTTPResponse.Header.Get("ETag"); moved == etag || moved != `"`+latest()+`"` {
		t.Errorf("ETag after a change = %s, was %s, want the newest entry %s", moved, etag, latest())
	}
}

func TestExportRefusesAnUnknownProjectOrFormat(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	res := export(t, admin, "no-such-project", "")
	assertProblem(t, "unknown project", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	res = export(t, admin, p.Key, "xml")
	assertProblem(t, "unknown format", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
}

func TestExportNeedsOnlyReadAccess(t *testing.T) {
	f := exportedProject(t)
	pool := testkit.Pool(t)

	viewer := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(f.project), viewer.ID, access.RoleViewer)
	reader := testkit.Client(t, f.srv, testkit.PersonalToken(t, pool, viewer.ID, "export", access.ScopeRead))
	if res := export(t, reader, f.project.Key, "json"); res.StatusCode() != http.StatusOK {
		t.Errorf("a viewer's export = %d: %s", res.StatusCode(), res.Body)
	}
	pipeline := testkit.Client(t, f.srv, testkit.ProjectToken(t, pool, projectIDOf(f.project), access.RoleViewer, access.ScopeRead))
	if res := export(t, pipeline, f.project.Key, "yaml"); res.StatusCode() != http.StatusOK {
		t.Errorf("a project token's export = %d: %s", res.StatusCode(), res.Body)
	}

	stranger := testkit.Client(t, f.srv, testkit.MemberToken(t, pool))
	if res := export(t, stranger, f.project.Key, "json"); res.StatusCode() == http.StatusOK {
		t.Errorf("a member without a grant exported the project: %s", res.Body)
	}
}

func TestExportHoldsEveryExperimentWhateverItsStatus(t *testing.T) {
	f := startExperiments(t)
	k := f.project.Key
	stringFlag(t, f.admin, k, "second_title")

	// running: held in staging and development by rules of its own.
	running := runningExperiment(t, f)
	// stopped: decided with a winner.
	second := startableExperiment("stopped_test")
	second.Flag = "second_title"
	createExperiment(t, f.admin, k, second)
	mustPut(t, f.admin, k, "second_title", "staging", 1, holding("stopped_test"))
	started := mustStart(t, f.admin, k, "stopped_test", 1)
	stopped := mustStop(t, f.admin, k, "stopped_test", started.Version, management.ExperimentStop{
		Outcome: management.ExperimentOutcomeWinner, WinnerVariation: ptr("a"), Note: ptr("A won.")})
	// archived and draft.
	archivedDraft := createExperiment(t, f.admin, k, validExperiment("archived_test"))
	archived := do(f.admin.ArchiveExperimentWithResponse(t.Context(), k, "archived_test",
		&management.ArchiveExperimentParams{IfMatch: ifMatch(archivedDraft.Version)}))
	if archived.JSON200 == nil {
		t.Fatalf("archive = %d %s", archived.StatusCode(), archived.Body)
	}
	draft := createExperiment(t, f.admin, k, startableExperiment("draft_test"))

	d := exported(t, f.admin, k)
	if got := keysOf(d, "experiments"); !slices.Equal(got, []string{"archived_test", "draft_test", "stopped_test", "title_test"}) {
		t.Fatalf("experiments = %v", got)
	}
	status := func(key string) string { return entry(t, d, "experiments", key)["status"].(string) }
	for key, want := range map[string]string{"title_test": "running", "stopped_test": "stopped", "archived_test": "archived", "draft_test": "draft"} {
		if got := status(key); got != want {
			t.Errorf("%s status = %s, want %s", key, got, want)
		}
	}

	// A started experiment carries its salts, timestamps and weights as the
	// API shows them, and the flag by key.
	e := entry(t, d, "experiments", "title_test")
	assertJSON(t, "title_test", map[string]any{
		"flag": e["flag"], "unit_type": e["unit_type"], "variations": e["variations"], "traffic_allocation": e["traffic_allocation"],
		"inclusion_salt": e["inclusion_salt"], "variation_salt": e["variation_salt"], "hypothesis": e["hypothesis"],
		"owner": e["owner"], "expected_end_at": e["expected_end_at"], "decision": e["decision"], "stopped_at": e["stopped_at"],
	}, `{"flag": "checkout_title", "unit_type": "user", "variations": [{"variation": "a", "weight": 5000}, {"variation": "b", "weight": 5000}],
		"traffic_allocation": 7000, "inclusion_salt": "`+running.InclusionSalt+`", "variation_salt": "`+running.VariationSalt+`",
		"hypothesis": "Short titles raise completed checkouts.", "owner": "`+testkit.InstanceAdminEmail+`",
		"expected_end_at": "2027-01-31", "decision": null, "stopped_at": null}`)
	if got := e["started_at"]; got == nil || got != running.StartedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("started_at = %v, want %v", got, running.StartedAt)
	}
	assertJSON(t, "stopped decision", entry(t, d, "experiments", "stopped_test")["decision"],
		`{"outcome": "winner", "winner_variation": "a", "note": "A won."}`)
	if got := entry(t, d, "experiments", "stopped_test")["stopped_at"]; got != stopped.StoppedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("stopped_at = %v, want %v", got, stopped.StoppedAt)
	}
	if got := entry(t, d, "experiments", "archived_test")["archived_at"]; got == nil {
		t.Errorf("archived_at is not exported")
	}
	if got := entry(t, d, "experiments", "draft_test"); got["started_at"] != nil || got["inclusion_salt"] != draft.InclusionSalt {
		t.Errorf("draft = %v, want its salts and no start", got)
	}

	// A rule serving an experiment names it by key.
	rules := entry(t, d, "flags", "checkout_title")["configurations"].(map[string]any)["staging"].(map[string]any)["rules"].([]any)
	assertJSON(t, "serve", rules[0].(map[string]any)["serve"], `{"kind": "experiment", "experiment": "title_test"}`)
}
