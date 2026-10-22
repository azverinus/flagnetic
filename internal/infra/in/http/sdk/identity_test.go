package sdk_test

import (
	"net/http"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// browserUserAgent is what a browser sends whatever the SDK running in it:
// a page cannot set User-Agent.
const browserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"

func TestAnSDKThatCannotSetItsUserAgentIsNamedByWhatItPosts(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout")
	clientKey, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	serverKey, server := p.sdkKey(t, "production", management.SdkKeyKindServer)

	evaluationWith(t, srv, client,
		`{"context":{"units":{"user":"u-1"},"attributes":{"sdk":"flagnetic-js","sdk_version":"1.3.0","platform":"web"}}}`,
		"User-Agent", browserUserAgent)
	// A User-Agent naming an SDK wins over the context.
	evaluationWith(t, srv, client,
		`{"context":{"units":{"user":"u-2"},"attributes":{"sdk":"flagnetic-js","sdk_version":"1.3.0","platform":"web"}}}`,
		"User-Agent", "flagnetic-swift/2.0.0 (ios)")
	if a := postEvents(t, srv, server, batch(), "User-Agent", browserUserAgent); a.status != http.StatusAccepted {
		t.Fatalf("events = %d: %s", a.status, a.body)
	}
	// And over the envelope.
	if a := postEvents(t, srv, client, batch(), "User-Agent", "flagnetic-kotlin/0.9.0 (android)"); a.status != http.StatusAccepted {
		t.Fatalf("events = %d: %s", a.status, a.body)
	}
	for _, name := range []string{worker.RemoteEvaluationCountsName, worker.SDKInventoryName} {
		if err := srv.Tick(t, name); err != nil {
			t.Fatal(err)
		}
	}

	counted := remoteCounts(t, p)
	if js := counted["checkout flagnetic-js"]; js.Row.Count != 1 || js.SDK.Version != "1.3.0" || js.SDK.Platform != "web" {
		t.Errorf("checkout by flagnetic-js = %+v, want 1 counted under the context's SDK", js)
	}
	if swift := counted["checkout flagnetic-swift"]; swift.Row.Count != 1 || swift.SDK.Platform != "ios" {
		t.Errorf("checkout by flagnetic-swift = %+v, want 1 counted under the User-Agent's SDK", swift)
	}
	if len(counted) != 2 {
		t.Errorf("counted = %v, want the two SDKs only", counted)
	}

	type row struct{ name, version, platform, key string }
	listed := map[row]bool{}
	for _, r := range inventory(t, p.admin, p, "production") {
		listed[row{r.SdkName, r.SdkVersion, valueOf(r.Platform), r.SdkKey.KeyId}] = true
	}
	for _, want := range []row{
		{"flagnetic-js", "1.3.0", "web", clientKey.KeyId},
		{"flagnetic-swift", "2.0.0", "ios", clientKey.KeyId},
		{"flagnetic-dart", "1.0.0", "linux", serverKey.KeyId},
		{"flagnetic-kotlin", "0.9.0", "android", clientKey.KeyId},
	} {
		if !listed[want] {
			t.Errorf("inventory %v lacks %+v", listed, want)
		}
	}
	if len(listed) != 4 {
		t.Errorf("inventory = %v, want 4 rows, none unknown", listed)
	}
}

func TestAContextWithoutAnSDKIsCountedAsUnknown(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout")
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	evaluationWith(t, srv, client,
		`{"context":{"units":{"user":"u-1"},"attributes":{"sdk_version":"1.3.0","platform":"web"}}}`,
		"User-Agent", browserUserAgent)
	if err := srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	if u := remoteCounts(t, p)["checkout unknown"]; u.Row.Count != 1 || u.SDK.Version != "" || u.SDK.Platform != "" {
		t.Errorf("checkout by unknown = %+v, want 1 with neither version nor platform", u)
	}
}
