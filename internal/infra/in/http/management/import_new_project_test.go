package management_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// importNew sends body as contentType to create a project from it.
func importNew(t *testing.T, c *management.ClientWithResponses, params management.ImportNewProjectParams, contentType string, body []byte,
) *management.ImportNewProjectResponse {
	t.Helper()
	return do(c.ImportNewProjectWithBodyWithResponse(t.Context(), &params, contentType, bytes.NewReader(body)))
}

// importJSON creates a project from the document doc, a generic JSON value.
func importJSON(t *testing.T, c *management.ClientWithResponses, params management.ImportNewProjectParams, doc any,
) *management.ImportNewProjectResponse {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return importNew(t, c, params, "application/json", b)
}

// mustImport creates a project from doc, failing unless it is created.
func mustImport(t *testing.T, c *management.ClientWithResponses, params management.ImportNewProjectParams, doc any) management.ImportResult {
	t.Helper()
	res := importJSON(t, c, params, doc)
	if res.JSON201 == nil {
		t.Fatalf("import = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

// hasProject reports whether the instance has the project keyed key.
func hasProject(t *testing.T, c *management.ClientWithResponses, key string) bool {
	t.Helper()
	res := do(c.GetProjectWithResponse(t.Context(), key))
	switch res.StatusCode() {
	case http.StatusOK:
		return true
	case http.StatusNotFound:
		return false
	}
	t.Fatalf("GET project %s = %d: %s", key, res.StatusCode(), res.Body)
	return false
}

// documentFor is a small document that creates a project keyed key: two
// environments, one protected, a unit type, an attribute, a segment and two
// flags, one requiring the other and one rolling out.
func documentFor(key string) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"project":        map[string]any{"key": key, "name": "Imported"},
		"environments": []any{
			map[string]any{"key": "staging", "name": "Staging", "protected": false},
			map[string]any{"key": "production", "name": "Production", "protected": true},
		},
		"unit_types": []any{map[string]any{"key": "user", "name": "User"}},
		"attributes": []any{map[string]any{"key": "plan", "type": "string", "name": "Plan"}},
		"segments": []any{map[string]any{"key": "pro", "name": "Pro",
			"conditions": []any{map[string]any{"kind": "attribute", "attribute": "plan", "op": "eq", "value": "pro"}}}},
		"flags": []any{
			map[string]any{"key": "base", "type": "boolean", "name": "Base", "configurations": map[string]any{
				"production": map[string]any{"on": true, "off_variation": "false", "fallthrough": "true"},
			}},
			map[string]any{"key": "checkout", "type": "boolean", "name": "Checkout", "configurations": map[string]any{
				"production": map[string]any{
					"on": true, "off_variation": "false", "fallthrough": "false",
					"prerequisites": []any{map[string]any{"flag": "base", "variation": "true"}},
					"rules": []any{
						map[string]any{"conditions": []any{map[string]any{"kind": "in_segment", "segment": "pro"}},
							"serve": map[string]any{"kind": "variation", "variation": "true"}},
						map[string]any{"conditions": []any{},
							"serve": map[string]any{"kind": "rollout", "variation": "true", "unit_type": "user", "percentage": 5000}},
					},
				},
			}},
		},
		"experiments": []any{},
	}
}

// mutate returns a copy of doc with edit applied, so a case changes one
// thing of a document that is otherwise valid.
func mutate(doc map[string]any, edit func(d map[string]any)) map[string]any {
	b, _ := json.Marshal(doc)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	edit(out)
	return out
}

// configurationIn returns the configuration of the flag at index in the
// document's production environment.
func configurationIn(d map[string]any, flag int) map[string]any {
	return d["flags"].([]any)[flag].(map[string]any)["configurations"].(map[string]any)["production"].(map[string]any)
}

func TestImportingAnExportCreatesACopyThatKeepsRuleIdsSaltsAndBuckets(t *testing.T) {
	f := exportedProject(t)
	source := f.project.Key
	document := export(t, f.admin, source, "json")
	if document.StatusCode() != http.StatusOK {
		t.Fatalf("export = %d: %s", document.StatusCode(), document.Body)
	}

	copied := projectKey()
	res := importNew(t, f.admin, management.ImportNewProjectParams{Key: &copied}, "application/json", document.Body)
	if res.JSON201 == nil {
		t.Fatalf("import = %d: %s", res.StatusCode(), res.Body)
	}
	r := res.JSON201
	if r.Project.Key != copied || r.Mode != management.ImportResultModeCreate || r.Source != management.ImportResultSourceDocument || r.DryRun {
		t.Errorf("result = %+v", r)
	}
	assertJSON(t, "environments created", r.Changes.Environments.Created, `["development", "staging", "production", "qa"]`)
	assertJSON(t, "unit types created", r.Changes.UnitTypes.Created, `["anonymous", "device", "user"]`)
	assertJSON(t, "attributes created", r.Changes.Attributes.Created, `["plan"]`)
	assertJSON(t, "segments created", r.Changes.Segments.Created, `["employees", "pro"]`)
	assertJSON(t, "flags created", r.Changes.Flags.Created, `["banner_text", "matching_v2", "new_matching", "old_flag"]`)
	assertJSON(t, "nothing updated or deleted", []any{r.Changes.Flags.Updated, r.Changes.Flags.Deleted, r.Changes.Experiments.Created},
		`[[], [], []]`)
	assertJSON(t, "warnings", r.Warnings, `[]`)

	// The copy is the original but for its key: same environments with their
	// protection, declarations, segments, flags, rule identifiers and salts.
	want, got := exported(t, f.admin, source), exported(t, f.admin, copied)
	want["project"].(map[string]any)["key"] = copied
	assertJSON(t, "export of the copy", got, asJSON(t, want))

	// Every unit evaluates as in the original, rollout buckets included, in
	// two environments.
	for _, env := range []string{"production", "staging"} {
		for i := range 40 {
			ctx := contextOf(map[string]string{"user": fmt.Sprintf("unit-%d", i)}, `{"app_version": "1.42.0"}`)
			a := mustEvaluate(t, f.admin, source, env, management.EvaluationRequest{Context: ctx})
			b := mustEvaluate(t, f.admin, copied, env, management.EvaluationRequest{Context: ctx})
			if asJSON(t, a.Flags) != asJSON(t, b.Flags) {
				t.Fatalf("%s, unit-%d: the copy evaluates\n%s\nwhere the original evaluated\n%s", env, i, asJSON(t, b.Flags), asJSON(t, a.Flags))
			}
		}
	}
}

func TestImportingTheYAMLExportCreatesTheSameProject(t *testing.T) {
	f := exportedProject(t)
	source := f.project.Key
	yamlDocument := export(t, f.admin, source, "yaml")

	copied := projectKey()
	res := importNew(t, f.admin, management.ImportNewProjectParams{Key: &copied}, "application/yaml", yamlDocument.Body)
	if res.JSON201 == nil {
		t.Fatalf("import = %d: %s", res.StatusCode(), res.Body)
	}
	want, got := exported(t, f.admin, source), exported(t, f.admin, copied)
	want["project"].(map[string]any)["key"] = copied
	assertJSON(t, "export of the copy", got, asJSON(t, want))
}

func TestCreatingFromADocumentTakesItsKeyAndNameUnlessAskedOtherwiseAndRefusesAKeyInUse(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	key := projectKey()
	res := mustImport(t, admin, management.ImportNewProjectParams{}, documentFor(key))
	if res.Project.Key != key || res.Project.Name != "Imported" {
		t.Errorf("project = %+v, want the document's key and name", res.Project)
	}
	got := do(admin.GetProjectWithResponse(t.Context(), key))
	if got.JSON200 == nil || got.JSON200.Name != "Imported" {
		t.Fatalf("GET project = %d: %s", got.StatusCode(), got.Body)
	}
	// Exactly the document and the standard attributes: no default
	// environment or unit type.
	envs := do(admin.ListEnvironmentsWithResponse(t.Context(), key))
	if envs.JSON200 == nil || len(envs.JSON200.Items) != 2 || envs.JSON200.Items[0].Key != "staging" || envs.JSON200.Items[1].Key != "production" ||
		envs.JSON200.Items[0].Protected || !envs.JSON200.Items[1].Protected {
		t.Errorf("environments = %s, want staging then production, the second protected", envs.Body)
	}
	units := do(admin.ListUnitTypesWithResponse(t.Context(), key))
	if units.JSON200 == nil || len(units.JSON200.Items) != 1 {
		t.Errorf("unit types = %s, want only the document's", units.Body)
	}

	again := importJSON(t, admin, management.ImportNewProjectParams{}, documentFor(key))
	assertProblem(t, "a key in use", again.StatusCode(), again.Body, http.StatusConflict, "already_exists")

	// The query replaces the document's key and name.
	other, name := projectKey(), "Renamed"
	res = mustImport(t, admin, management.ImportNewProjectParams{Key: &other, Name: &name}, documentFor(key))
	if res.Project.Key != other || res.Project.Name != "Renamed" || !hasProject(t, admin, other) {
		t.Errorf("project = %+v, want the key and name asked for", res.Project)
	}
}

func TestADryRunAnswersAsTheImportDoesAndWritesNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	key := projectKey()
	dry := true

	res := importJSON(t, admin, management.ImportNewProjectParams{DryRun: ptr(dry)}, documentFor(key))
	if res.JSON200 == nil || !res.JSON200.DryRun {
		t.Fatalf("dry run = %d: %s", res.StatusCode(), res.Body)
	}
	if hasProject(t, admin, key) {
		t.Fatalf("a dry run created the project")
	}

	applied := mustImport(t, admin, management.ImportNewProjectParams{}, documentFor(key))
	if asJSON(t, res.JSON200.Changes) != asJSON(t, applied.Changes) || asJSON(t, res.JSON200.Warnings) != asJSON(t, applied.Warnings) {
		t.Errorf("a dry run answered\n%s\n%s\nbut the import\n%s\n%s", asJSON(t, res.JSON200.Changes), asJSON(t, res.JSON200.Warnings),
			asJSON(t, applied.Changes), asJSON(t, applied.Warnings))
	}

	// A dry run meets what the database refuses too: a key in use.
	again := importJSON(t, admin, management.ImportNewProjectParams{DryRun: ptr(dry)}, documentFor(key))
	assertProblem(t, "dry run of a key in use", again.StatusCode(), again.Body, http.StatusConflict, "already_exists")
}

func TestADocumentThatBreaksARuleIsRefusedWholeAtThePointerOfEachFault(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	cases := []struct {
		name string
		edit func(d map[string]any)
		// pointer and code of the fault expected among the errors.
		pointer, code string
	}{
		{"an unknown field", func(d map[string]any) { d["project"].(map[string]any)["colour"] = "red" }, "/project/colour", "unknown_field"},
		{"a flag key repeated", func(d map[string]any) {
			flags := d["flags"].([]any)
			d["flags"] = append(flags, flags[0])
		}, "/flags/2/key", "duplicate"},
		{"a unit type and an attribute sharing a key", func(d map[string]any) {
			d["attributes"] = append(d["attributes"].([]any), map[string]any{"key": "user", "type": "string", "name": "User"})
		}, "/attributes/1/key", "duplicate"},
		{"a unit type keyed as a standard attribute", func(d map[string]any) {
			d["unit_types"] = append(d["unit_types"].([]any), map[string]any{"key": "platform", "name": "Platform"})
		}, "/unit_types/1/key", "duplicate"},
		{"a standard attribute of another type", func(d map[string]any) {
			d["attributes"] = append(d["attributes"].([]any), map[string]any{"key": "app_version", "type": "string", "name": "App version"})
		}, "/attributes/1/type", "type_immutable"},
		{"an undeclared attribute", func(d map[string]any) {
			d["segments"].([]any)[0].(map[string]any)["conditions"].([]any)[0].(map[string]any)["attribute"] = "nope"
		}, "/segments/0/conditions/0/attribute", "unknown_attribute"},
		{"a prerequisite cycle", func(d map[string]any) {
			configurationIn(d, 0)["prerequisites"] = []any{map[string]any{"flag": "checkout", "variation": "true"}}
		}, "/flags/1/configurations/production/prerequisites/0/flag", "prerequisite_cycle"},
		{"an archived prerequisite", func(d map[string]any) {
			d["flags"].([]any)[0].(map[string]any)["archived"] = true
		}, "/flags/1/configurations/production/prerequisites/0/flag", "archived_flag"},
		{"an environment the document lacks", func(d map[string]any) {
			c := d["flags"].([]any)[0].(map[string]any)["configurations"].(map[string]any)
			c["qa"] = c["production"]
		}, "/flags/0/configurations/qa", "unknown_environment"},
		{"a rule identifier that is no UUID", func(d map[string]any) {
			configurationIn(d, 1)["rules"].([]any)[0].(map[string]any)["id"] = "rule-1"
		}, "/flags/1/configurations/production/rules/0/id", "invalid_value"},
		{"a salt of the wrong form", func(d map[string]any) {
			configurationIn(d, 1)["rules"].([]any)[1].(map[string]any)["serve"].(map[string]any)["salt"] = "TOO-SHORT"
		}, "/flags/1/configurations/production/rules/1/serve/salt", "invalid_value"},
		{"a variation the flag lacks", func(d map[string]any) {
			configurationIn(d, 1)["rules"].([]any)[0].(map[string]any)["serve"].(map[string]any)["variation"] = "maybe"
		}, "/flags/1/configurations/production/rules/0/serve/variation", "unknown_variation"},
		{"no environment", func(d map[string]any) { d["environments"] = []any{} }, "/environments", "invalid_value"},
		{"a section missing", func(d map[string]any) { delete(d, "segments") }, "/segments", "required"},
		{"an invalid project key", func(d map[string]any) { d["project"].(map[string]any)["key"] = "Not A Key" }, "/project/key", "invalid_key"},
		{"an invalid environment", func(d map[string]any) { d["environments"].([]any)[0].(map[string]any)["key"] = "Not A Key" }, "/environments/0/key", "invalid_key"},
		{"an invalid unit type", func(d map[string]any) { d["unit_types"].([]any)[0].(map[string]any)["name"] = "" }, "/unit_types/0/name", "invalid_name"},
		{"an invalid attribute", func(d map[string]any) { d["attributes"].([]any)[0].(map[string]any)["type"] = "bogus" }, "/attributes/0/type", "invalid_type"},
		{"an invalid segment", func(d map[string]any) { d["segments"].([]any)[0].(map[string]any)["key"] = "Bad Key" }, "/segments/0/key", "invalid_key"},
		{"an invalid flag", func(d map[string]any) { d["flags"].([]any)[0].(map[string]any)["key"] = "1bad" }, "/flags/0/key", "invalid_key"},
		{"an experiment past its draft without salts", func(d map[string]any) {
			d["experiments"] = []any{map[string]any{"key": "e", "status": "running"}}
		}, "/experiments/0/inclusion_salt", "required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := projectKey()
			doc := mutate(documentFor(key), tc.edit)
			res := importJSON(t, admin, management.ImportNewProjectParams{}, doc)
			problem := assertProblem(t, tc.name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_document")
			var found []string
			if problem.Errors != nil {
				for _, v := range *problem.Errors {
					found = append(found, v.Pointer+" "+v.Code)
				}
			}
			if !slices.Contains(found, tc.pointer+" "+tc.code) {
				t.Errorf("errors = %v, want %s %s among them", found, tc.pointer, tc.code)
			}
			if hasProject(t, admin, key) && tc.name != "an invalid project key" {
				t.Errorf("a refused document created a project")
			}
		})
	}
}

func TestEveryFaultOfADocumentIsReportedAtOnce(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	doc := mutate(documentFor(projectKey()), func(d map[string]any) {
		d["environments"].([]any)[0].(map[string]any)["name"] = ""
		configurationIn(d, 1)["rules"].([]any)[0].(map[string]any)["serve"].(map[string]any)["variation"] = "maybe"
		d["flags"].([]any)[0].(map[string]any)["key"] = "1bad"
		// Faults the document shows alone come with those of its entities.
		c := d["flags"].([]any)[0].(map[string]any)["configurations"].(map[string]any)
		c["qa"] = c["production"]
		d["unit_types"] = append(d["unit_types"].([]any), d["unit_types"].([]any)[0])
	})
	res := importJSON(t, admin, management.ImportNewProjectParams{}, doc)
	problem := assertProblem(t, "faulty document", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_document")
	var found []string
	for _, v := range *problem.Errors {
		found = append(found, v.Pointer)
	}
	for _, want := range []string{
		"/unit_types/1/key", "/flags/0/configurations/qa",
		"/environments/0/name", "/flags/0/key", "/flags/1/configurations/production/rules/0/serve/variation",
	} {
		if !slices.Contains(found, want) {
			t.Errorf("errors = %v, want %s among them", found, want)
		}
	}
}

func TestADocumentOfAnotherSchemaVersionIsRefusedNamingTheOnesRead(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	res := importJSON(t, admin, management.ImportNewProjectParams{}, mutate(documentFor(projectKey()), func(d map[string]any) { d["schema_version"] = 2 }))
	assertProblem(t, "schema version", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "schema_version_unsupported")
	var p map[string]any
	if err := json.Unmarshal(res.Body, &p); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, "supported_versions", p["supported_versions"], `[1]`)
}

func TestABodyThatIsNoDocumentOrNoJSONOrYAMLIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	doc, _ := json.Marshal(documentFor(projectKey()))

	res := importNew(t, admin, management.ImportNewProjectParams{}, "text/plain", doc)
	assertProblem(t, "another media type", res.StatusCode(), res.Body, http.StatusUnsupportedMediaType, "unsupported_media_type")
	res = importNew(t, admin, management.ImportNewProjectParams{}, "application/json", []byte(`{"schema_version": 1,`))
	assertProblem(t, "malformed JSON", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	res = importNew(t, admin, management.ImportNewProjectParams{}, "application/yaml", []byte("schema_version: [1\n"))
	assertProblem(t, "malformed YAML", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	res = importNew(t, admin, management.ImportNewProjectParams{}, "application/json", []byte(`[1]`))
	assertProblem(t, "not an object", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
}

func TestAnImportBodyOver32MiBIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	body := io.MultiReader(strings.NewReader(`{"schema_version": 1, "padding": "`), &repeated{n: 33 << 20}, strings.NewReader(`"}`))
	res := do(admin.ImportNewProjectWithBodyWithResponse(t.Context(), &management.ImportNewProjectParams{}, "application/json", body))
	assertProblem(t, "a body over the limit", res.StatusCode(), res.Body, http.StatusRequestEntityTooLarge, "request_too_large")
}

// repeated reads n bytes of the letter a.
type repeated struct{ n int }

func (r *repeated) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.n)
	for i := range n {
		p[i] = 'a'
	}
	r.n -= n
	return n, nil
}

func TestOnlyAnInstanceAdminCreatesAProjectFromADocument(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	existing := createProject(t, admin, projectKey())

	maintainer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(existing), access.RoleOwner))
	res := importJSON(t, maintainer, management.ImportNewProjectParams{}, documentFor(projectKey()))
	if res.StatusCode() != http.StatusForbidden {
		t.Errorf("a project owner's import = %d: %s, want 403", res.StatusCode(), res.Body)
	}

	pipeline := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(existing), access.RoleMaintainer))
	res = importJSON(t, pipeline, management.ImportNewProjectParams{}, documentFor(projectKey()))
	if res.StatusCode() != http.StatusForbidden {
		t.Errorf("a project token's import = %d: %s, want 403", res.StatusCode(), res.Body)
	}
}

func TestAnOwnerWhoCannotOwnIsReplacedByTheImporterAndWarned(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	// A member with no grant in a project that does not exist yet cannot own
	// there, and neither can an address nobody has.
	stranger := testkit.NewMember(t, pool)

	key := projectKey()
	doc := mutate(documentFor(key), func(d map[string]any) {
		flags := d["flags"].([]any)
		flags[0].(map[string]any)["owner"] = "nobody@example.com"
		flags[1].(map[string]any)["owner"] = strings.ToUpper(stranger.Email)
	})
	res := mustImport(t, admin, management.ImportNewProjectParams{}, doc)
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings = %s, want two", asJSON(t, res.Warnings))
	}
	assertJSON(t, "first warning", []any{res.Warnings[0].Code, res.Warnings[0].Pointer, res.Warnings[0].Params},
		`["owner_replaced", "/flags/0/owner", {"email": "nobody@example.com"}]`)
	assertJSON(t, "second warning", []any{res.Warnings[1].Code, res.Warnings[1].Pointer},
		`["owner_replaced", "/flags/1/owner"]`)
	for _, flag := range []string{"base", "checkout"} {
		if owner := getFlag(t, admin, key, flag).Owner; owner != adminID {
			t.Errorf("owner of %s = %v, want the importing member %v", flag, owner, adminID)
		}
	}

	// An owner who may own keeps the flag.
	doc = mutate(documentFor(key), func(d map[string]any) {
		d["project"].(map[string]any)["key"] = projectKey()
		d["flags"].([]any)[0].(map[string]any)["owner"] = strings.ToUpper(testkit.InstanceAdminEmail)
	})
	res = mustImport(t, admin, management.ImportNewProjectParams{}, doc)
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %s, want none for an owner who may own", asJSON(t, res.Warnings))
	}
}

func TestOverrideNotesTravelAndTheirAuthorsDoNot(t *testing.T) {
	f := exportedProject(t)
	source := f.project.Key
	pool := testkit.Pool(t)
	document := export(t, f.admin, source, "json")

	// Another instance admin imports it, and so is the author of every
	// override in the copy.
	importer, token := testkit.NewInstanceAdmin(t, pool)
	copied := projectKey()
	res := importNew(t, testkit.Client(t, f.srv, token), management.ImportNewProjectParams{Key: &copied}, "application/json", document.Body)
	if res.JSON201 == nil {
		t.Fatalf("import = %d: %s", res.StatusCode(), res.Body)
	}
	// The flags' owners are the original admin, who may own anywhere, so
	// nothing is replaced.
	if len(res.JSON201.Warnings) != 0 {
		t.Errorf("warnings = %s", asJSON(t, res.JSON201.Warnings))
	}
	original := getConfiguration(t, f.admin, source, "new_matching", "production").Overrides
	duplicate := getConfiguration(t, f.admin, copied, "new_matching", "production").Overrides
	if len(duplicate) != 1 || duplicate[0].Note == nil || *duplicate[0].Note != "Demo account" {
		t.Fatalf("overrides of the copy = %s, want the note", asJSON(t, duplicate))
	}
	if original[0].AddedBy == nil || duplicate[0].AddedBy == nil || *duplicate[0].AddedBy == *original[0].AddedBy || *duplicate[0].AddedBy != openapi_types.UUID(importer.ID.Bytes()) {
		t.Errorf("override author in the copy = %v, original %v, want the importing member %v", duplicate[0].AddedBy, original[0].AddedBy, importer.ID)
	}
}

func TestAnImportRecordsEachEntityThenOneSummaryEntryUnderTheRequestsID(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	key := projectKey()
	mustImport(t, admin, management.ImportNewProjectParams{}, documentFor(key))

	list := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), key, &management.ListProjectAuditEntriesParams{}))
	if list.JSON200 == nil {
		t.Fatalf("audit = %d: %s", list.StatusCode(), list.Body)
	}
	// Newest first.
	entries := slices.Clone(list.JSON200.Items)
	slices.Reverse(entries)
	var actions []string
	requests := map[string]bool{}
	for _, e := range entries {
		actions = append(actions, e.Action)
		if e.RequestId == nil {
			t.Fatalf("entry %s has no request ID", e.Action)
		}
		requests[*e.RequestId] = true
	}
	want := []string{
		"project.created", "environment.created", "environment.created", "unit_type.created", "attribute.created",
		"segment.created", "flag.created", "flag.created", "flag.configuration_updated", "flag.configuration_updated", "project.imported",
	}
	if !slices.Equal(actions, want) {
		t.Errorf("actions = %v, want %v", actions, want)
	}
	if len(requests) != 1 {
		t.Errorf("the entries carry %d request IDs, want the one request's", len(requests))
	}
	last := entries[len(entries)-1]
	after, _ := last.After.(map[string]any)
	if after["mode"] != "create" || after["source"] != "document" {
		t.Fatalf("project.imported after = %v", last.After)
	}
	assertJSON(t, "counts of flags", after["counts"].(map[string]any)["flags"], `{"created": 2, "updated": 0, "deleted": 0}`)
}
