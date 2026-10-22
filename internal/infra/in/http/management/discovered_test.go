package management_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// discovered writes what the replica noted, then lists the project's
// discovered attributes as key, types and count.
func discovered(t *testing.T, srv *testkit.Server, c *management.ClientWithResponses, project string) []string {
	t.Helper()
	if err := srv.Tick(t, worker.DiscoveredAttributesName); err != nil {
		t.Fatal(err)
	}
	res := do(c.ListDiscoveredAttributesWithResponse(t.Context(), project))
	if res.JSON200 == nil {
		t.Fatalf("GET …/attributes/discovered = %d %s", res.StatusCode(), res.Body)
	}
	out := []string{}
	for _, d := range res.JSON200.Items {
		out = append(out, d.Key+" "+strings.Join(d.ObservedTypes, ",")+" "+strconv.FormatInt(d.Count, 10))
	}
	return out
}

func TestEvaluateRecordsUndeclaredAttributesOfTheContext(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "subject")
	entries := len(auditActions(t, admin, p.Key))

	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user("u1"),
		`{"plan":"pro-secret","seats":3,"tags":["a"],"os":"linux","gone":null,"user":"u2","Bad Key":"x"}`)})
	c.Advance(time.Minute)
	mustEvaluate(t, admin, p.Key, "production", management.EvaluationRequest{Context: contextOf(nil, `{"plan":"basic"}`)})

	if got, want := discovered(t, srv, admin, p.Key), []string{"plan string 2", "seats number 1", "tags list 1"}; !slices.Equal(got, want) {
		t.Errorf("discovered = %v, want %v", got, want)
	}
	res := do(admin.ListDiscoveredAttributesWithResponse(t.Context(), p.Key))
	if bytes.Contains(res.Body, []byte("pro-secret")) || bytes.Contains(res.Body, []byte("basic")) {
		t.Errorf("a value was recorded: %s", res.Body)
	}
	plan := res.JSON200.Items[0]
	if !plan.FirstSeen.Equal(c.Now().Add(-time.Minute)) || !plan.LastSeen.Equal(c.Now()) {
		t.Errorf("plan seen %v to %v, want the two evaluations' times", plan.FirstSeen, plan.LastSeen)
	}
	if n := len(auditActions(t, admin, p.Key)); n != entries {
		t.Errorf("recording wrote %d audit entries, want none", n-entries)
	}
}

func TestADismissedAttributeReappearsOnlyWithANewType(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	seen := func(attributes string) {
		t.Helper()
		mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, attributes)})
	}
	seen(`{"tier":"gold"}`)
	if got := discovered(t, srv, admin, p.Key); !slices.Equal(got, []string{"tier string 1"}) {
		t.Fatalf("discovered = %v", got)
	}
	entries := len(auditActions(t, admin, p.Key))

	if res := do(admin.DismissDiscoveredAttributeWithResponse(t.Context(), p.Key, "tier")); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s", res.StatusCode(), res.Body)
	}
	if got := discovered(t, srv, admin, p.Key); len(got) != 0 {
		t.Errorf("after dismissing: %v, want nothing", got)
	}
	res := do(admin.DismissDiscoveredAttributeWithResponse(t.Context(), p.Key, "tier"))
	assertProblem(t, "dismissing twice", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	res = do(admin.DismissDiscoveredAttributeWithResponse(t.Context(), p.Key, "never_seen"))
	assertProblem(t, "dismissing what was never seen", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	seen(`{"tier":"silver"}`)
	if got := discovered(t, srv, admin, p.Key); len(got) != 0 {
		t.Errorf("seen again with a known type: %v, want still dismissed", got)
	}
	seen(`{"tier":3}`)
	if got := discovered(t, srv, admin, p.Key); !slices.Equal(got, []string{"tier string,number 3"}) {
		t.Errorf("seen with a new type: %v, want it listed again", got)
	}
	if n := len(auditActions(t, admin, p.Key)); n != entries {
		t.Errorf("recording and dismissing wrote %d audit entries, want none", n-entries)
	}
}

func TestADiscoveredAttributeIsDeclaredInOneStep(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	other := createProject(t, admin, projectKey())
	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, `{"tier":"gold","age":41}`)})
	mustEvaluate(t, admin, other.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, `{"tier":"gold"}`)})
	discovered(t, srv, admin, p.Key)
	before := rulesetVersions(t, p)

	res := do(admin.DeclareDiscoveredAttributeWithResponse(t.Context(), p.Key, "tier", management.DiscoveredAttributeDeclaration{Type: "string"}))
	if res.JSON201 == nil {
		t.Fatalf("declare = %d %s", res.StatusCode(), res.Body)
	}
	if a := res.JSON201; a.Key != "tier" || a.Type != "string" || a.Name != "tier" || a.Standard || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Errorf("declared = %+v, ETag %q", a, res.HTTPResponse.Header.Get("ETag"))
	}
	if got := discovered(t, srv, admin, p.Key); !slices.Equal(got, []string{"age number 1"}) {
		t.Errorf("after declaring: %v, want tier gone", got)
	}
	if got := discovered(t, srv, admin, other.Key); !slices.Equal(got, []string{"tier string 1"}) {
		t.Errorf("another project's: %v, want untouched", got)
	}
	if got := do(admin.GetAttributeWithResponse(t.Context(), p.Key, "tier")); got.JSON200 == nil {
		t.Errorf("GET the declared attribute = %d", got.StatusCode())
	}
	if actions := auditActions(t, admin, p.Key); len(actions) == 0 || actions[0] != "attribute.created" {
		t.Errorf("audit = %v, want the declaration's attribute.created", actions)
	}
	for env, v := range rulesetVersions(t, p) {
		if v <= before[env] {
			t.Errorf("%s ruleset version %d, want it moved on from %d", env, v, before[env])
		}
	}

	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, `{"tier":"gold"}`)})
	if got := discovered(t, srv, admin, p.Key); !slices.Equal(got, []string{"age number 1"}) {
		t.Errorf("a declared key seen again: %v, want it never recorded", got)
	}
	again := do(admin.DeclareDiscoveredAttributeWithResponse(t.Context(), p.Key, "tier", management.DiscoveredAttributeDeclaration{Type: "string"}))
	assertProblem(t, "declaring it again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
	unseen := do(admin.DeclareDiscoveredAttributeWithResponse(t.Context(), p.Key, "never_seen", management.DiscoveredAttributeDeclaration{Type: "string"}))
	assertProblem(t, "declaring what was never seen", unseen.StatusCode(), unseen.Body, http.StatusNotFound, "not_found")

	// An attribute declared directly leaves the list too.
	created := do(admin.CreateAttributeWithResponse(t.Context(), p.Key, management.AttributeCreate{Key: "age", Name: "Age", Type: "number"}))
	if created.JSON201 == nil {
		t.Fatalf("create = %d %s", created.StatusCode(), created.Body)
	}
	if got := discovered(t, srv, admin, p.Key); len(got) != 0 {
		t.Errorf("after declaring directly: %v, want nothing", got)
	}
}

func TestDiscoveredAttributesAreCappedPerProject(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	attributes := func(from, to int) string {
		parts := []string{}
		for i := from; i < to; i++ {
			parts = append(parts, `"k`+strconv.Itoa(1000+i)+`":"x"`)
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, attributes(0, 600))})
	got := discovered(t, srv, admin, p.Key)
	if len(got) != 500 || got[0] != "k1000 string 1" || got[499] != "k1499 string 1" {
		t.Fatalf("discovered %d, first %q, last %q; want the first 500 by key", len(got), got[0], got[len(got)-1])
	}
	if res := do(admin.DismissDiscoveredAttributeWithResponse(t.Context(), p.Key, "k1000")); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("dismiss = %d", res.StatusCode())
	}
	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, `{"k1001":1,"k1600":"x"}`)})
	got = discovered(t, srv, admin, p.Key)
	if len(got) != 499 || got[0] != "k1001 string,number 2" || slices.Contains(got, "k1600 string 1") {
		t.Errorf("at the cap, dismissed included: %d listed, first %q; want known keys merged and no new one", len(got), got[0])
	}
	if res := do(admin.DeclareDiscoveredAttributeWithResponse(t.Context(), p.Key, "k1002", management.DiscoveredAttributeDeclaration{Type: "string"})); res.JSON201 == nil {
		t.Fatalf("declare = %d %s", res.StatusCode(), res.Body)
	}
	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, `{"k1600":"x"}`)})
	if got := discovered(t, srv, admin, p.Key); !slices.Contains(got, "k1600 string 1") {
		t.Errorf("a declaration frees a place: k1600 not listed")
	}
}

func TestAReplicaDropsWhatItCannotHoldWithAWarning(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	parts := []string{}
	for i := range 1001 {
		parts = append(parts, `"k`+strconv.Itoa(1000+i)+`":"x"`)
	}
	mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(nil, "{"+strings.Join(parts, ",")+"}")})
	if got := discovered(t, srv, admin, p.Key); len(got) != 500 {
		t.Errorf("discovered %d, want the project's 500", len(got))
	}
	warnings := srv.Logs.Find("discovered attributes dropped: too many pending")
	if len(warnings) != 1 || warnings[0].Level != slog.LevelWarn || warnings[0].Attrs["dropped"] != int64(1) {
		t.Errorf("warnings = %+v, want one telling the one dropped", warnings)
	}
}
