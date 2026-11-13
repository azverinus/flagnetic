package management_test

import (
	"bytes"
	"net/http"
	"os"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// firebaseTemplate uses app.id, device.os, app.version.<, percent, ||, a user
// property and the unsupported dateTime and app.firstOpenTimestamp terms,
// over a boolean, a string, a number and a JSON parameter, two of them in a
// parameter group.
const firebaseTemplate = "../../../../domain/transfer/firebase/testdata/firebase-template.json"

type firebaseOptions struct {
	unitType string
	dryRun   bool
	ifMatch  string
}

func readFirebaseTemplate(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(firebaseTemplate)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func firebaseImport(t *testing.T, c *management.ClientWithResponses, project, env string, template []byte, opts ...firebaseOptions,
) *management.ImportFirebaseTemplateResponse {
	t.Helper()
	params := management.ImportFirebaseTemplateParams{Environment: env}
	for _, o := range opts {
		if o.unitType != "" {
			params.UnitType = ptr(o.unitType)
		}
		if o.dryRun {
			params.DryRun = ptr(true)
		}
		if o.ifMatch != "" {
			params.IfMatch = ptr(o.ifMatch)
		}
	}
	return do(c.ImportFirebaseTemplateWithBodyWithResponse(t.Context(), project, &params, "application/json", bytes.NewReader(template)))
}

func mustFirebaseImport(t *testing.T, c *management.ClientWithResponses, project, env string, template []byte, opts ...firebaseOptions,
) management.FirebaseImportResult {
	t.Helper()
	res := firebaseImport(t, c, project, env, template, opts...)
	if res.JSON200 == nil {
		t.Fatalf("import firebase = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func warningCodes(r management.FirebaseImportResult) []string {
	var out []string
	for _, w := range r.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func TestAFirebaseTemplateIsImportedIntoOneEnvironmentAndEvaluatesAsItDid(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	versions, actions := rulesetVersions(t, p), auditActions(t, admin, k)

	r := mustFirebaseImport(t, admin, k, "production", readFirebaseTemplate(t))

	if r.Mode != management.FirebaseImportResultModeMerge || r.Source != management.FirebaseImportResultSourceFirebase || r.DryRun || r.Project.Key != k {
		t.Errorf("result = mode %s, source %s, dry run %v, project %s", r.Mode, r.Source, r.DryRun, r.Project.Key)
	}
	assertJSON(t, "flags created", r.Changes.Flags.Created, `["banner_text", "legacy_limits", "new_matching", "onboarding_steps"]`)
	assertJSON(t, "attributes created", r.Changes.Attributes.Created, `["plan_type"]`)
	assertJSON(t, "declared attributes", r.Attributes, `[{"key": "plan_type", "type": "string"}]`)
	assertJSON(t, "warnings", warningCodes(r), `["attribute_declared", "condition_skipped", "in_app_default", "rollout_reshuffled", "condition_skipped"]`)
	for _, w := range r.Warnings {
		if w.Pointer == "" {
			t.Errorf("warning %s has no pointer into the template", w.Code)
		}
	}

	// The flags the answer lists are those the environment stores.
	if len(r.Flags) != 4 {
		t.Fatalf("flags = %d, want 4", len(r.Flags))
	}
	for _, f := range r.Flags {
		stored := getConfiguration(t, admin, k, f.Key, "production")
		if f.On != stored.On || f.OffVariation != stored.OffVariation || f.Fallthrough != stored.Fallthrough || len(f.Rules) != len(stored.Rules) {
			t.Errorf("answer for %s = %s, stored %s", f.Key, asJSON(t, f), asJSON(t, stored))
		}
		for i, rule := range f.Rules {
			if rule.Id == nil || stored.Rules[i].Id != *rule.Id {
				t.Errorf("%s rule %d id = %v, stored %s", f.Key, i, rule.Id, stored.Rules[i].Id)
			}
			if rule.Serve.Kind == "rollout" && (rule.Serve.Salt == nil || len(*rule.Serve.Salt) != 16) {
				t.Errorf("%s rule %d is a rollout without the salt it was given", f.Key, i)
			}
		}
	}

	// A new flag is on where it was imported and off elsewhere.
	for _, key := range []string{"new_matching", "banner_text", "onboarding_steps", "legacy_limits"} {
		if c := getConfiguration(t, admin, k, key, "production"); !c.On {
			t.Errorf("%s is off in production", key)
		}
		for _, env := range []string{"development", "staging"} {
			if c := getConfiguration(t, admin, k, key, env); c.On || len(c.Rules) != 0 {
				t.Errorf("%s in %s = %s, want off without rules", key, env, asJSON(t, c))
			}
		}
	}

	// An iOS context matches through platform, as device.os did.
	ios := mustEvaluate(t, admin, k, "production", management.EvaluationRequest{
		Context: contextOf(map[string]string{}, `{"platform": "ios", "app_version": "1.0.0"}`)})
	if got := ios.Flags["banner_text"]; got.Variation == nil || *got.Variation != "v2" || got.Reason != "rule" {
		t.Errorf("banner_text for iOS = %s, want v2 by a rule", asJSON(t, got))
	}
	oldAndroid := mustEvaluate(t, admin, k, "production", management.EvaluationRequest{
		Context: contextOf(map[string]string{}, `{"platform": "android", "app_version": "1.40.2"}`)})
	if got := oldAndroid.Flags["new_matching"]; got.Variation == nil || *got.Variation != "true" {
		t.Errorf("new_matching for an old Android = %s, want true", asJSON(t, got))
	}
	if got := mustEvaluate(t, admin, k, "staging", management.EvaluationRequest{}).Flags["new_matching"]; got.Reason != "off" {
		t.Errorf("new_matching in staging = %s, want off", asJSON(t, got))
	}

	// Production moved on, the others only for the flags created in them.
	if got := bumped(t, "import", versions, rulesetVersions(t, p)); !slices.Contains(got, "production") {
		t.Errorf("rulesets moved on: %v, want production among them", got)
	}
	got := auditActions(t, admin, k)
	if len(got) <= len(actions) || !slices.Contains(got, "project.imported") || !slices.Contains(got, "flag.created") ||
		!slices.Contains(got, "attribute.created") {
		t.Errorf("audit actions = %v", got)
	}
}

func TestReimportingTheSameFirebaseTemplateChangesNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	first := mustFirebaseImport(t, admin, k, "production", readFirebaseTemplate(t))
	versions, actions := rulesetVersions(t, p), auditActions(t, admin, k)

	again := mustFirebaseImport(t, admin, k, "production", readFirebaseTemplate(t))

	assertNothingChanged(t, management.ImportResult{Changes: again.Changes})
	assertJSON(t, "declared attributes", again.Attributes, `[]`)
	assertJSON(t, "flags", again.Flags, asJSON(t, first.Flags))
	if got := bumped(t, "a re-import", versions, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("rulesets moved on: %v", got)
	}
	if got := auditActions(t, admin, k); !slices.Equal(got, actions) {
		t.Errorf("audit grew from %v to %v, want nothing recorded", actions, got)
	}
}

func TestAFirebaseDryRunReportsTheImportAndWritesNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	versions, actions := rulesetVersions(t, p), auditActions(t, admin, k)

	res := firebaseImport(t, admin, k, "staging", readFirebaseTemplate(t), firebaseOptions{dryRun: true})
	if res.JSON200 == nil {
		t.Fatalf("dry run = %d: %s", res.StatusCode(), res.Body)
	}
	r := *res.JSON200
	if !r.DryRun || len(r.Warnings) != 5 || len(r.Flags) != 4 {
		t.Errorf("dry run = %v with %d warnings and %d flags, want everything answered", r.DryRun, len(r.Warnings), len(r.Flags))
	}
	assertJSON(t, "flags created", r.Changes.Flags.Created, `["banner_text", "legacy_limits", "new_matching", "onboarding_steps"]`)
	rules := map[string]int{}
	for _, f := range r.Flags {
		rules[f.Key] = len(f.Rules)
	}
	assertJSON(t, "rules per flag", rules, `{"banner_text": 4, "legacy_limits": 0, "new_matching": 2, "onboarding_steps": 1}`)
	if res.HTTPResponse.Header.Get("ETag") != export(t, admin, k, "json").HTTPResponse.Header.Get("ETag") {
		t.Errorf("a dry run's ETag differs from the project's state")
	}

	if res := do(admin.GetFlagWithResponse(t.Context(), k, "new_matching")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("a flag after a dry run = %d, want 404", res.StatusCode())
	}
	if res := do(admin.GetAttributeWithResponse(t.Context(), k, "plan_type")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("plan_type after a dry run = %d, want 404", res.StatusCode())
	}
	if got := bumped(t, "a dry run", versions, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("rulesets moved on: %v", got)
	}
	if got := auditActions(t, admin, k); !slices.Equal(got, actions) {
		t.Errorf("audit after a dry run = %v, want %v", got, actions)
	}
}

func TestAFirebaseImportIntoAnExistingFlagKeepsItsVariationsOverridesAndOtherEnvironments(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	createFlag(t, admin, k, management.FlagCreate{Key: "banner_text", Type: management.FlagTypeString, Name: "Banner", Description: ptr("Kept."),
		Tags: &[]string{"web"}, Variations: variations("hello", `"Hello"`, "other", `"Other"`)})
	createFlag(t, admin, k, management.FlagCreate{Key: "gate", Type: management.FlagTypeBoolean, Name: "Gate"})
	mustPut(t, admin, k, "gate", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	staging := mustPut(t, admin, k, "banner_text", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "hello", Fallthrough: "other"})
	mustPut(t, admin, k, "banner_text", "production", 1, management.FlagConfigurationPut{
		On: false, OffVariation: "hello", Fallthrough: "hello",
		Prerequisites: &[]management.Prerequisite{{Flag: "gate", Variation: "true"}},
		Overrides:     &[]management.OverrideInput{{UnitType: "user", UnitId: "qa-7", Variation: "other"}}})
	// The target is protected: an owner imports where a direct write is refused.
	setProtected(t, admin, k, "production", true)

	r := mustFirebaseImport(t, admin, k, "production", readFirebaseTemplate(t))

	assertJSON(t, "flags created", r.Changes.Flags.Created, `["legacy_limits", "new_matching", "onboarding_steps"]`)
	assertJSON(t, "flags updated", keysOfUpdates(r.Changes.Flags.Updated), `["banner_text"]`)
	f := getFlag(t, admin, k, "banner_text")
	if f.Name != "Banner" || f.Description != "Kept." || !slices.Equal(f.Tags, []string{"web"}) {
		t.Errorf("banner_text = %s, want its name, description and tags kept", asJSON(t, f))
	}
	var keys []string
	for _, v := range f.Variations {
		keys = append(keys, v.Key)
	}
	// "Hello" is the flag's own variation; the others take free v-keys.
	assertJSON(t, "variation keys", keys, `["hello", "other", "v1", "v2"]`)
	if got := getConfiguration(t, admin, k, "banner_text", "staging"); got.Version != staging.Version || asJSON(t, got.Rules) != asJSON(t, staging.Rules) {
		t.Errorf("staging = %s, want it untouched", asJSON(t, got))
	}
	prod := getConfiguration(t, admin, k, "banner_text", "production")
	if !prod.On || prod.OffVariation != "hello" || prod.Fallthrough != "hello" || len(prod.Rules) != 4 ||
		len(prod.Prerequisites) != 1 || len(prod.Overrides) != 1 || prod.Overrides[0].UnitId != "qa-7" {
		t.Errorf("production = %s, want on with the imported rules, the prerequisite and the override kept", asJSON(t, prod))
	}
}

func TestAFirebaseImportLeavesTheExperimentsAlone(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	createFlag(t, admin, k, management.FlagCreate{Key: "banner_text", Type: management.FlagTypeString, Name: "Banner",
		Variations: variations("a", `"Hello"`, "b", `"Other"`)})
	e := validExperiment("banner_test")
	e.Flag = "banner_text"
	createExperiment(t, admin, k, e)
	before := exported(t, admin, k)["experiments"]

	r := mustFirebaseImport(t, admin, k, "production", readFirebaseTemplate(t))

	assertJSON(t, "experiment changes", r.Changes.Experiments, noChanges)
	assertJSON(t, "experiments after the import", exported(t, admin, k)["experiments"], asJSON(t, before))

	// A project without any exports an empty section.
	empty := createProject(t, admin, projectKey())
	mustFirebaseImport(t, admin, empty.Key, "production", readFirebaseTemplate(t))
	assertJSON(t, "experiments of a Firebase-only project", exported(t, admin, empty.Key)["experiments"], `[]`)
}

func TestAFirebaseImportRefusesWhatItCannotImportAndWritesNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	createFlag(t, admin, k, management.FlagCreate{Key: "retyped", Type: management.FlagTypeBoolean, Name: "Retyped"})
	versions, actions := rulesetVersions(t, p), auditActions(t, admin, k)

	type refusal struct {
		env, unitType, template string
		violations              []string
	}
	for name, tc := range map[string]refusal{
		"unknown environment": {"qa", "", `{"parameters": {"a": {"defaultValue": {"value": "x"}}}}`, []string{"/query/environment unknown_environment"}},
		"unknown unit type": {"production", "nope", `{"parameters": {"a": {"defaultValue": {"value": "x"}}}}`,
			[]string{"/query/unit_type unknown_unit_type"}},
		"both":     {"qa", "nope", `{}`, []string{"/query/environment unknown_environment", "/query/unit_type unknown_unit_type"}},
		"not JSON": {"production", "", `conditions:`, []string{" invalid_json"}},
		"unknown condition": {"production", "", `{"parameters": {"a": {"defaultValue": {"value": "x"}, "conditionalValues": {"nope": {"value": "y"}}}}}`,
			[]string{"/parameters/a/conditionalValues/nope unknown_condition"}},
		"parameter twice": {"production", "", `{"parameters": {"a": {"defaultValue": {"value": "x"}}},
			"parameterGroups": {"g": {"parameters": {"a": {"defaultValue": {"value": "y"}}}}}}`, []string{"/parameterGroups/g/parameters/a duplicate_parameter"}},
		"not a boolean": {"production", "", `{"parameters": {"a": {"defaultValue": {"value": "maybe"}, "valueType": "BOOLEAN"}}}`,
			[]string{"/parameters/a/defaultValue/value invalid_value"}},
		"unknown value type": {"production", "", `{"parameters": {"a": {"defaultValue": {"value": "x"}, "valueType": "DATE"}}}`,
			[]string{"/parameters/a/valueType invalid_value_type"}},
		"retyped flag": {"production", "", `{"parameters": {"retyped": {"defaultValue": {"value": "x"}}}}`,
			[]string{"/parameters/retyped/valueType type_immutable"}},
	} {
		res := firebaseImport(t, admin, k, tc.env, []byte(tc.template), firebaseOptions{unitType: tc.unitType})
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_document"); !slices.Equal(got, tc.violations) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.violations)
		}
	}
	if res := do(admin.ImportFirebaseTemplateWithBodyWithResponse(t.Context(), k, &management.ImportFirebaseTemplateParams{Environment: "production"},
		"application/yaml", bytes.NewReader([]byte(`parameters: {}`)))); res.StatusCode() != http.StatusUnsupportedMediaType {
		t.Errorf("a YAML template = %d: %s, want 415", res.StatusCode(), res.Body)
	}
	if res := do(admin.GetFlagWithResponse(t.Context(), k, "a")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("a flag of a refused template = %d, want 404", res.StatusCode())
	}
	if got := bumped(t, "refused imports", versions, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("rulesets moved on: %v", got)
	}
	if got := auditActions(t, admin, k); !slices.Equal(got, actions) {
		t.Errorf("refused imports recorded %v", got)
	}
}

func TestOnlyAnOwnerImportsAFirebaseTemplateAndIfMatchGuardsTheState(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	k := p.Key
	pool := testkit.Pool(t)
	template := readFirebaseTemplate(t)

	for name, c := range map[string]*management.ClientWithResponses{
		"a maintainer":                 testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleMaintainer)),
		"a project token of the owner": testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(p), access.RoleOwner)),
	} {
		res := firebaseImport(t, c, k, "production", template)
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
		if pr.Permission == nil || *pr.Permission != "project.import" {
			t.Errorf("%s: permission = %v, want project.import", name, pr.Permission)
		}
	}
	owner := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleOwner))

	tag := firebaseImport(t, owner, k, "production", template, firebaseOptions{dryRun: true}).HTTPResponse.Header.Get("ETag")
	createSegment(t, admin, k, management.SegmentCreate{Key: "meanwhile", Name: "Meanwhile", Conditions: conditions(attr("user", "eq", `"x"`))})
	res := firebaseImport(t, owner, k, "production", template, firebaseOptions{ifMatch: tag})
	assertProblem(t, "an import on a project that moved", res.StatusCode(), res.Body, http.StatusPreconditionFailed, "version_mismatch")
	if res := do(admin.GetFlagWithResponse(t.Context(), k, "new_matching")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("a flag after a refused import = %d, want 404", res.StatusCode())
	}
	now := firebaseImport(t, owner, k, "production", template, firebaseOptions{dryRun: true}).HTTPResponse.Header.Get("ETag")
	if res := firebaseImport(t, owner, k, "production", template, firebaseOptions{ifMatch: now}); res.JSON200 == nil {
		t.Fatalf("an import on the state it has = %d: %s", res.StatusCode(), res.Body)
	}

	// A new flag has the default lifetime and the importing member as its
	// owner, and the summary entry names Firebase as the source.
	ownerID := do(owner.GetMeWithResponse(t.Context())).JSON200.Member.Id
	if f := getFlag(t, admin, k, "new_matching"); f.Owner != ownerID || f.Lifetime != management.LifetimeTemporary || f.ExpectedRemovalAt == nil {
		t.Errorf("new_matching = owner %s, lifetime %s until %v, want the importing owner and the default lifetime", f.Owner, f.Lifetime, f.ExpectedRemovalAt)
	}
	entries := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), k, &management.ListProjectAuditEntriesParams{Action: ptr("project.imported")}))
	if entries.JSON200 == nil || len(entries.JSON200.Items) != 1 || field(entries.JSON200.Items[0].After, "source") != "firebase" ||
		field(entries.JSON200.Items[0].After, "mode") != "merge" {
		t.Errorf("project.imported entries = %s, want one with source firebase in merge mode", entries.Body)
	}

	if res := do(admin.ImportFirebaseTemplateWithBodyWithResponse(t.Context(), "nope", &management.ImportFirebaseTemplateParams{Environment: "production"},
		"application/json", bytes.NewReader(template))); res.StatusCode() != http.StatusNotFound {
		t.Errorf("an unknown project = %d, want 404", res.StatusCode())
	}
}

func TestAFirebaseImportHonoursTheUnitTypeOfItsRollouts(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	r := mustFirebaseImport(t, admin, p.Key, "production", readFirebaseTemplate(t), firebaseOptions{unitType: "user", dryRun: true})

	var rollout *string
	for _, f := range r.Flags {
		for _, rule := range f.Rules {
			if rule.Serve.Kind == "rollout" {
				rollout = rule.Serve.UnitType
			}
		}
	}
	if rollout == nil || *rollout != "user" {
		t.Errorf("the rollout's unit type = %v, want user", rollout)
	}
}
