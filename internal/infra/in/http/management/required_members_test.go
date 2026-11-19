package management_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A body that lacks a required member must be refused before it reaches the
// use case, whose zero value for a boolean is a valid answer: `{}` to the
// protection of production would unprotect it.
func TestABodyLackingARequiredMemberIsRefusedAndChangesNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProjectAsCreated(t, admin, projectKey()).Key
	protection := func() (bool, string) {
		res := do(admin.ListEnvironmentsWithResponse(t.Context(), p))
		if res.JSON200 == nil {
			t.Fatalf("GET environments = %d %s", res.StatusCode(), res.Body)
		}
		for _, e := range res.JSON200.Items {
			if e.Key == "production" {
				return e.Protected, string(rune('0' + e.Version))
			}
		}
		t.Fatal("no production environment")
		return false, ""
	}
	wasProtected, version := protection()
	if !wasProtected {
		t.Fatal("production starts unprotected")
	}

	for name, body := range map[string]string{"an empty object": `{}`, "null": `null`, "a misspelt member": `{"protect": true}`, "a null member": `{"protected": null}`, "an array": `[]`} {
		res, err := admin.SetEnvironmentProtectionWithBodyWithResponse(t.Context(), p, "production",
			&management.SetEnvironmentProtectionParams{IfMatch: ifMatch(1)}, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid")
		want := "/protected"
		if name == "null" || name == "an array" {
			want = ""
		}
		if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != want {
			t.Errorf("%s: violations = %+v, want one at %q", name, pr.Errors, want)
		}
	}
	if now, v := protection(); !now || v != version {
		t.Errorf("production is protected = %v at version %s after the refusals, want true at %s", now, v, version)
	}

	// A body with the member still unprotects it, and members nobody named stay
	// accepted.
	res, err := admin.SetEnvironmentProtectionWithBodyWithResponse(t.Context(), p, "production",
		&management.SetEnvironmentProtectionParams{IfMatch: ifMatch(1)}, "application/json", strings.NewReader(`{"protected": false, "from_a_newer_client": 1}`))
	if err != nil || res.JSON200 == nil || res.JSON200.Protected {
		t.Errorf("a complete body = %v %s, want it applied", res.StatusCode(), res.Body)
	}
}

func TestAFlagConfigurationWithoutOnDoesNotSwitchItOff(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProjectAsCreated(t, admin, projectKey()).Key
	res, err := admin.PutFlagConfigurationWithBodyWithResponse(t.Context(), p, "f", "staging",
		&management.PutFlagConfigurationParams{IfMatch: ifMatch(1)}, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	pr := assertProblem(t, "a configuration without on", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid")
	if pr.Errors == nil || !slices.ContainsFunc(*pr.Errors, func(v management.Violation) bool { return v.Pointer == "/on" }) {
		t.Errorf("violations = %+v, want one at /on among them", pr.Errors)
	}
}

// The mechanism is one, so a guard walks the document: for every operation
// taking a JSON object that requires members, a body with one of them removed
// is refused 422 naming it, whichever operation is added later.
func TestEveryOperationRefusesABodyWithOneRequiredMemberRemoved(t *testing.T) {
	raw, err := os.ReadFile("../../../../../api/management.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	required := func(schema map[string]any) []string {
		var out []string
		var walk func(s map[string]any)
		walk = func(s map[string]any) {
			if ref, ok := s["$ref"].(string); ok {
				s = doc.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
			}
			if parts, ok := s["allOf"].([]any); ok {
				for _, p := range parts {
					walk(p.(map[string]any))
				}
			}
			if list, ok := s["required"].([]any); ok {
				for _, r := range list {
					if !slices.Contains(out, r.(string)) {
						out = append(out, r.(string))
					}
				}
			}
		}
		walk(schema)
		return out
	}

	srv := testkit.StartServer(t)
	testkit.Bootstrap(t, testkit.Pool(t))
	admin := testkit.NewBrowser(t, srv.URL)
	admin.SignIn(t, testkit.InstanceAdminEmail, testkit.InstanceAdminPassword)
	project := createProjectAsCreated(t, admin.ClientWithResponses, projectKey()).Key
	braces := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	for path, item := range doc.Paths {
		for method, op := range item {
			o, ok := op.(map[string]any)
			if !ok || !slices.Contains([]string{"post", "put", "patch", "delete"}, method) {
				continue
			}
			body, _ := o["requestBody"].(map[string]any)
			content, _ := body["content"].(map[string]any)
			media, _ := content["application/json"].(map[string]any)
			schema, _ := media["schema"].(map[string]any)
			if schema == nil || len(content) != 1 {
				continue
			}
			members := required(schema)
			url := braces.ReplaceAllStringFunc(path, func(p string) string {
				if p == "{project}" {
					return project
				}
				return "x"
			})
			for _, missing := range members {
				whole := map[string]any{}
				for _, m := range members {
					if m != missing {
						whole[m] = "x"
					}
				}
				payload, _ := json.Marshal(whole)
				req, err := http.NewRequestWithContext(t.Context(), strings.ToUpper(method), srv.URL+management.BaseURL+url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("If-Match", `"1"`)
				res, err := admin.HTTP.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Violations []struct{ Pointer string } `json:"errors"`
				}
				_ = json.NewDecoder(res.Body).Decode(&got)
				res.Body.Close()
				pointers := make([]string, len(got.Violations))
				for i, v := range got.Violations {
					pointers[i] = v.Pointer
				}
				if res.StatusCode != http.StatusUnprocessableEntity || !slices.Contains(pointers, "/"+missing) {
					t.Errorf("%s %s without %s = %d, violations %v; want 422 naming it", strings.ToUpper(method), path, missing, res.StatusCode, pointers)
				}
				checked++
			}
		}
	}
	if checked < 50 {
		t.Errorf("the guard checked %d bodies; the document has more operations requiring members than that", checked)
	}
}
