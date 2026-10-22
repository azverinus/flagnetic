package sdk_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func do[R any](res R, err error) R {
	if err != nil {
		panic("sdk_test: request failed: " + err.Error())
	}
	return res
}

func ptr[T any](v T) *T { return &v }

// project is a project created for one test, with its environments
// production and staging.
type project struct {
	key   string
	admin *management.ClientWithResponses
}

// newProject creates a project through srv as the instance admin.
func newProject(t *testing.T, srv *testkit.Server) project {
	t.Helper()
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	key := "p" + strings.ToLower(rand.Text()[:12])
	res := do(admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Project " + key}))
	if res.JSON201 == nil {
		t.Fatalf("POST /projects = %d: %s", res.StatusCode(), res.Body)
	}
	return project{key: key, admin: admin}
}

// sdkKey creates an SDK key of kind in environment, answering its value.
func (p project) sdkKey(t *testing.T, environment string, kind management.SdkKeyKind) (management.SdkKey, string) {
	t.Helper()
	res := do(p.admin.CreateSdkKeyWithResponse(t.Context(), p.key, environment, management.SdkKeyCreate{Kind: string(kind), Name: "checkout"}))
	if res.JSON201 == nil {
		t.Fatalf("POST …/environments/%s/sdk-keys = %d: %s", environment, res.StatusCode(), res.Body)
	}
	return res.JSON201.Key, res.JSON201.Value
}

// revoke revokes an SDK key of environment.
func (p project) revoke(t *testing.T, environment string, k management.SdkKey) {
	t.Helper()
	res := do(p.admin.RevokeSdkKeyWithResponse(t.Context(), p.key, environment, k.Id.String()))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("revoke = %d: %s", res.StatusCode(), res.Body)
	}
}

// rotate rotates an SDK key of environment with an overlap of seconds.
func (p project) rotate(t *testing.T, environment string, k management.SdkKey, seconds int64) {
	t.Helper()
	res := do(p.admin.RotateSdkKeyWithResponse(t.Context(), p.key, environment, k.Id.String(),
		management.SdkKeyRotation{OverlapSeconds: &seconds}))
	if res.StatusCode() != http.StatusCreated {
		t.Fatalf("rotate = %d: %s", res.StatusCode(), res.Body)
	}
}

// flag creates a boolean flag keyed key.
func (p project) flag(t *testing.T, key string) {
	t.Helper()
	res := do(p.admin.CreateFlagWithResponse(t.Context(), p.key, management.FlagCreate{Key: key, Name: "Flag " + key, Type: "boolean"}))
	if res.JSON201 == nil {
		t.Fatalf("POST …/flags = %d: %s", res.StatusCode(), res.Body)
	}
}

// client returns a client of srv's SDK API.
func client(t *testing.T, srv *testkit.Server) *sdk.ClientWithResponses {
	t.Helper()
	c, err := sdk.NewClientWithResponses(srv.URL + sdk.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// header returns a request editor setting a header, or adding it when it
// is set already.
func header(name, value string) sdk.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		r.Header.Add(name, value)
		return nil
	}
}

// bearer presents value as the SDK key.
func bearer(value string) sdk.RequestEditorFn { return header("Authorization", "Bearer "+value) }

// ruleset asks srv for the ruleset with key value.
func ruleset(t *testing.T, srv *testkit.Server, value string, editors ...sdk.RequestEditorFn) *sdk.GetRulesetResponse {
	t.Helper()
	return do(client(t, srv).GetRulesetWithResponse(t.Context(), &sdk.GetRulesetParams{}, append([]sdk.RequestEditorFn{bearer(value)}, editors...)...))
}

// problemOf decodes a problem answer.
func problemOf(t *testing.T, body []byte) sdk.Problem {
	t.Helper()
	var p sdk.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("not a problem: %v: %s", err, body)
	}
	return p
}

// refused fails unless an answer is a problem of status and code.
func refused(t *testing.T, name string, status int, body []byte, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus {
		t.Errorf("%s: status = %d, want %d: %s", name, status, wantStatus, body)
		return
	}
	if p := problemOf(t, body); p.Code != wantCode {
		t.Errorf("%s: code = %q, want %q", name, p.Code, wantCode)
	}
}

// eventually polls cond every 10ms until it holds or within passes,
// reporting whether it held.
func eventually(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// document is the part of a ruleset document a test reads.
type document struct {
	Version     int64  `json:"version"`
	Scope       string `json:"scope"`
	Environment struct {
		Key string `json:"key"`
	} `json:"environment"`
	Flags []struct {
		Key string `json:"key"`
		On  bool   `json:"on"`
	} `json:"flags"`
}

func decode(t *testing.T, body []byte) document {
	t.Helper()
	var d document
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("not a ruleset: %v: %s", err, body)
	}
	return d
}

// kill switches flag off in environment, a write moving its ruleset on.
func (p project) kill(t *testing.T, flag, environment string) {
	t.Helper()
	res := do(p.admin.KillFlagWithResponse(t.Context(), p.key, flag, environment, management.Kill{}))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("kill = %d: %s", res.StatusCode(), res.Body)
	}
}

// environmentID returns the identifier of environment.
func (p project) environmentID(t *testing.T, environment string) workspace.EnvironmentID {
	t.Helper()
	res := do(p.admin.GetEnvironmentWithResponse(t.Context(), p.key, environment))
	if res.JSON200 == nil {
		t.Fatalf("GET …/environments/%s = %d: %s", environment, res.StatusCode(), res.Body)
	}
	return id.FromBytes[workspace.Environment](res.JSON200.Id)
}

// projectID returns the identifier of the project.
func (p project) projectID(t *testing.T) workspace.ProjectID {
	t.Helper()
	res := do(p.admin.GetProjectWithResponse(t.Context(), p.key))
	if res.JSON200 == nil {
		t.Fatalf("GET /projects/%s = %d: %s", p.key, res.StatusCode(), res.Body)
	}
	return id.FromBytes[workspace.Project](res.JSON200.Id)
}

// switchOn switches a new flag on in environment, serving true.
func (p project) switchOn(t *testing.T, flag, environment string) {
	t.Helper()
	res := do(p.admin.PutFlagConfigurationWithResponse(t.Context(), p.key, flag, environment,
		&management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)},
		management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"}))
	if res.JSON200 == nil {
		t.Fatalf("PUT …/%s/configurations/%s = %d: %s", flag, environment, res.StatusCode(), res.Body)
	}
}
