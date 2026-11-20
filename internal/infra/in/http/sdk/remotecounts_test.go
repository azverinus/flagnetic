package sdk_test

import (
	"encoding/json"
	"net/http"
	"strings"

	"testing"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// remoteCounts returns the evaluation summary rows of production that the
// server counted itself, by flag and SDK name.
func remoteCounts(t *testing.T, p project) map[string]sinks.SummaryRow {
	t.Helper()
	rows := testkit.ExportedEvaluationSummaries(t, testkit.Pool(t), p.projectID(t), p.environmentID(t, "production"))
	out := map[string]sinks.SummaryRow{}
	for _, r := range rows {
		if r.Source == string(delivery.SourceRemoteEvaluation) {
			if _, twice := out[r.FlagKey+" "+r.SDKName]; twice {
				t.Errorf("%s by %s counted in two rows", r.FlagKey, r.SDKName)
			}
			out[r.FlagKey+" "+r.SDKName] = r
		}
	}
	return out
}

func TestRemoteEvaluationsAreCountedByTheirTrueReason(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.targeted(t)
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	body := `{"context":{"units":{"user":"u-1"},"attributes":{"os":"android","country":"nz","app_version":"3.1.0"}},"flags":["ruled","dark"]}`
	for range 2 {
		ev := evaluationWith(t, srv, client, body, "User-Agent", "flagnetic-js/1.2.0 (web)")
		if ev.Flags["ruled"].Reason != "targeting_match" {
			t.Fatalf("a client key was answered %q, want the masked reason", ev.Flags["ruled"].Reason)
		}
	}
	evaluationWith(t, srv, client, body, "User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
	if got := remoteCounts(t, p); len(got) != 0 {
		t.Errorf("before the round = %v, want nothing written yet", got)
	}
	if err := srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	got := remoteCounts(t, p)
	ruled := got["ruled flagnetic-js"]
	if ruled.EvaluationReason != "rule" || ruled.VariationKey != "b" || ruled.Count != 2 || ruled.SDKVersion != "1.2.0" ||
		ruled.Platform != "web" || ruled.AppVersion != "3.1.0" || ruled.RulesetVersion == 0 {
		t.Errorf("ruled by flagnetic-js = %+v, want 2 of rule b", ruled)
	}
	if dark := got["dark flagnetic-js"]; dark.EvaluationReason != "off" || dark.Count != 2 {
		t.Errorf("dark by flagnetic-js = %+v", dark)
	}
	if unknown := got["ruled unknown"]; unknown.Count != 1 || unknown.SDKVersion != "" || unknown.Platform != "" {
		t.Errorf("ruled by a browser that is no SDK = %+v, want counted as unknown", unknown)
	}
	if len(got) != 4 {
		t.Errorf("rows = %d, want 4: %v", len(got), got)
	}

	// A round with nothing counted writes nothing.
	if err := srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	if again := remoteCounts(t, p); len(again) != 4 {
		t.Errorf("after an empty round = %d rows, want still 4", len(again))
	}
}

func TestRemoteEvaluationOfAnAppVersionHoldingNULDoesNotHoldBackAnotherProjectsCounts(t *testing.T) {
	srv := testkit.StartServer(t)
	hostile, other := newProject(t, srv), newProject(t, srv)
	hostile.flag(t, "checkout")
	other.flag(t, "checkout")
	_, hostileKey := hostile.sdkKey(t, "production", management.SdkKeyKindClient)
	_, otherKey := other.sdkKey(t, "production", management.SdkKeyKindClient)
	agent := []string{"User-Agent", "flagnetic-js/1.2.0 (web)"}
	evaluationWith(t, srv, hostileKey, `{"context":{"units":{"user":"u-1"},"attributes":{"app_version":"3.1\u00000"}}}`, agent...)
	evaluationWith(t, srv, otherKey, `{"context":{"units":{"user":"u-1"},"attributes":{"app_version":"3.1.0"}}}`, agent...)
	if err := srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	if c := remoteCounts(t, other)["checkout flagnetic-js"]; c.Count != 1 || c.AppVersion != "3.1.0" {
		t.Errorf("the other project's count = %+v, want 1 of 3.1.0", c)
	}
	if c := remoteCounts(t, hostile)["checkout flagnetic-js"]; c.Count != 1 || strings.ContainsRune(c.AppVersion, 0) {
		t.Errorf("the hostile project's count = %+v, want 1 with no NUL in its app version", c)
	}
}

func TestRemoteEvaluationCountsAreWrittenWhenTheReplicaStops(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout")
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	evaluationWith(t, srv, server, `{"context":{"units":{"user":"u-1"}}}`, "User-Agent", "flagnetic-go/1.4.0 (linux)")
	srv.CloseBackground()
	if c := remoteCounts(t, p)["checkout flagnetic-go"]; c.Count != 1 || c.EvaluationReason != "off" {
		t.Errorf("checkout after the stop = %+v, want 1 of off", c)
	}
}

// evaluationWith posts a context as JSON with the extra headers given as
// name, value pairs, and decodes the evaluation answered.
func evaluationWith(t *testing.T, srv *testkit.Server, value, body string, headers ...string) sdk.Evaluation {
	t.Helper()
	a := postEvaluate(t, srv, value, "application/json", []byte(body), headers...)
	if a.status != http.StatusOK {
		t.Fatalf("evaluate = %d: %s", a.status, a.body)
	}
	var ev sdk.Evaluation
	if err := json.Unmarshal(a.body, &ev); err != nil {
		t.Fatalf("not an evaluation: %v: %s", err, a.body)
	}
	return ev
}
