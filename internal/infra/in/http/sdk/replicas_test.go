package sdk_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// codeOf returns the problem code of a ruleset answer, or "" for a ruleset.
func codeOf(t *testing.T, res interface {
	StatusCode() int
}, body []byte,
) string {
	t.Helper()
	if res.StatusCode() == http.StatusOK {
		return ""
	}
	return problemOf(t, body).Code
}

// refusedWithin reports whether srv refuses value with code within a second.
func refusedWithin(t *testing.T, srv *testkit.Server, value, code string) bool {
	t.Helper()
	return eventually(time.Second, func() bool {
		res := ruleset(t, srv, value)
		return codeOf(t, res, res.Body) == code
	})
}

func TestARevocationOrRotationThroughOneReplicaIsRefusedByTheOtherWithinASecond(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	p := newProject(t, a)
	revoked, revokedValue := p.sdkKey(t, "production", management.SdkKeyKindServer)
	rotated, rotatedValue := p.sdkKey(t, "production", management.SdkKeyKindServer)
	// b holds both keys once it has served them.
	for _, v := range []string{revokedValue, rotatedValue} {
		if res := ruleset(t, b, v); res.StatusCode() != http.StatusOK {
			t.Fatalf("b before any change: %d %s", res.StatusCode(), res.Body)
		}
	}

	p.revoke(t, "production", revoked)
	p.rotate(t, "production", rotated, 0)
	if !refusedWithin(t, b, revokedValue, "sdk_key_revoked") {
		t.Error("b still accepts the key a revoked after a second")
	}
	if !refusedWithin(t, b, rotatedValue, "sdk_key_expired") {
		t.Error("b still accepts the key a rotated without overlap after a second")
	}
}

func TestDeletingAnEnvironmentEndsItsKeysOnEveryReplica(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	p := newProject(t, a)
	created := do(p.admin.CreateEnvironmentWithResponse(t.Context(), p.key, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	if created.JSON201 == nil {
		t.Fatalf("create qa = %d: %s", created.StatusCode(), created.Body)
	}
	_, value := p.sdkKey(t, "qa", management.SdkKeyKindServer)
	for _, srv := range []*testkit.Server{a, b} {
		if res := ruleset(t, srv, value); res.StatusCode() != http.StatusOK {
			t.Fatalf("before the deletion: %d %s", res.StatusCode(), res.Body)
		}
	}

	deleted := do(p.admin.DeleteEnvironmentWithResponse(t.Context(), p.key, "qa", &management.DeleteEnvironmentParams{IfMatch: ptr(`"1"`)}))
	if deleted.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete qa = %d: %s", deleted.StatusCode(), deleted.Body)
	}
	for name, srv := range map[string]*testkit.Server{"a": a, "b": b} {
		if !refusedWithin(t, srv, value, "sdk_key_invalid") {
			t.Errorf("%s still accepts a key of the deleted environment after a second", name)
		}
	}
}

func TestAReplicaWhoseListenerIsSilentRefusesARevokedKeyAtOnce(t *testing.T) {
	fault := &pglisten.Fault{}
	a := testkit.StartServer(t)
	b := testkit.StartServer(t, testkit.WithListenerOptions(pglisten.Options{
		PingInterval: 50 * time.Millisecond, PingTimeout: 50 * time.Millisecond, Fault: fault,
	}))
	p := newProject(t, a)
	k, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	if res := ruleset(t, b, value); res.StatusCode() != http.StatusOK {
		t.Fatalf("b before the fault: %d %s", res.StatusCode(), res.Body)
	}

	// Silenced, b's listener misses every notification, the revocation's
	// included, until its ping finds it deaf.
	fault.Silence()
	if !eventually(5*time.Second, func() bool {
		return testkit.Metric(t, b.Scrape(t), `flagnetic_listener_up{channel="sdk_keys"}`) == 0
	}) {
		t.Fatal("b's listener was never found deaf")
	}
	p.revoke(t, "production", k)
	res := ruleset(t, b, value)
	refused(t, "b with a silent listener", res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_revoked")
}
