package management_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// serverRuleset returns the ruleset document of project's environment, as
// its server SDKs receive it.
func serverRuleset(t *testing.T, c *management.ClientWithResponses, project, environment string) string {
	t.Helper()
	res := do(c.GetEnvironmentRulesetWithResponse(t.Context(), project, environment))
	if res.JSON200 == nil {
		t.Fatalf("GET %s ruleset = %d %s", environment, res.StatusCode(), res.Body)
	}
	return string(res.Body)
}

// environmentVersion returns the version of project's environment.
func environmentVersion(t *testing.T, c *management.ClientWithResponses, project, environment string) int64 {
	t.Helper()
	res := do(c.GetEnvironmentWithResponse(t.Context(), project, environment))
	if res.JSON200 == nil {
		t.Fatalf("GET %s = %d %s", environment, res.StatusCode(), res.Body)
	}
	return res.JSON200.Version
}

// setProtected sets the protection of project's environment as c, failing
// unless it is accepted.
func setProtected(t *testing.T, c *management.ClientWithResponses, project, environment string, protected bool) {
	t.Helper()
	res := protect(t, c, project, environment, environmentVersion(t, c, project, environment), protected)
	if res.JSON200 == nil {
		t.Fatalf("protect %s %v = %d %s", environment, protected, res.StatusCode(), res.Body)
	}
}

// refusedUnchanged fails unless a write answered 403
// change_request_required naming production, and no ruleset of p moved.
func refusedUnchanged(t *testing.T, name string, p management.Project, versions map[string]int64, status int, body []byte) {
	t.Helper()
	if pr := assertProblem(t, name, status, body, http.StatusForbidden, "change_request_required"); pr.Detail != nil &&
		!strings.Contains(*pr.Detail, "production") {
		t.Errorf("%s: the refusal does not name production: %s", name, *pr.Detail)
	}
	if moved := bumped(t, name, versions, rulesetVersions(t, p)); moved != nil {
		t.Errorf("%s: rulesets of %v moved, want none", name, moved)
	}
}

// A new project's production is protected: a developer and a maintainer
// alike are told to propose a change request, and nothing moves, while
// staging stays direct.
func TestEditingAProtectedEnvironmentNeedsAChangeRequest(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProjectAsCreated(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Type: management.FlagTypeBoolean, Name: "Checkout"})

	for _, role := range []access.RoleName{access.RoleDeveloper, access.RoleMaintainer} {
		c := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), role))
		versions := rulesetVersions(t, p)
		res := putConfiguration(t, c, p.Key, "checkout", "production", 1,
			management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
		refusedUnchanged(t, string(role)+" editing production", p, versions, res.StatusCode(), res.Body)
		if got := getConfiguration(t, c, p.Key, "checkout", "production"); got.On || got.Version != 1 {
			t.Errorf("%s: production after the refusal is on %v at version %d, want off at 1", role, got.On, got.Version)
		}

		staging := getConfiguration(t, c, p.Key, "checkout", "staging")
		edited := putConfiguration(t, c, p.Key, "checkout", "staging", staging.Version,
			management.FlagConfigurationPut{On: !staging.On, OffVariation: "false", Fallthrough: "true"})
		status(t, string(role)+" editing staging", edited.StatusCode(), edited.Body, http.StatusOK)
	}
}

// Clearing protection is the owner's way out: a developer then edits
// production directly, until the owner protects it again.
func TestAnOwnerClearingProtectionOpensTheEnvironmentToDirectWrites(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProjectAsCreated(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Type: management.FlagTypeBoolean, Name: "Checkout"})
	owner := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleOwner))
	developer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleDeveloper))

	setProtected(t, owner, p.Key, "production", false)
	on := putConfiguration(t, developer, p.Key, "checkout", "production", 1,
		management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	if !status(t, "developer editing unprotected production", on.StatusCode(), on.Body, http.StatusOK) {
		return
	}

	setProtected(t, owner, p.Key, "production", true)
	versions := rulesetVersions(t, p)
	off := putConfiguration(t, developer, p.Key, "checkout", "production", on.JSON200.Version,
		management.FlagConfigurationPut{On: false, OffVariation: "false", Fallthrough: "true"})
	refusedUnchanged(t, "developer editing production protected again", p, versions, off.StatusCode(), off.Body)
}

// guarded is a project whose production, set up while unprotected, holds
// checkout switched on with a rule on the segment employees, a rollout and
// an override; retired, archived while on; quiet, off; banner, a string
// flag off; and the segment unused, which no flag references. Production
// is protected again.
type guarded struct {
	p       management.Project
	rollout string
}

func newGuarded(t *testing.T, admin *management.ClientWithResponses) guarded {
	t.Helper()
	p := createProjectAsCreated(t, admin, projectKey())
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "employees", Name: "Employees",
		Conditions: conditions(attr("country", "in", `["de","fr"]`))})
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "unused", Name: "Unused",
		Conditions: conditions(attr("country", "in", `["it"]`))})
	for _, key := range []string{"checkout", "retired", "quiet"} {
		createFlag(t, admin, p.Key, management.FlagCreate{Key: key, Type: management.FlagTypeBoolean, Name: key})
	}
	stringFlag(t, admin, p.Key, "banner")
	setProtected(t, admin, p.Key, "production", false)
	on := mustPut(t, admin, p.Key, "checkout", "production", 1, management.FlagConfigurationPut{
		On: true, OffVariation: "false", Fallthrough: "false",
		Rules: &[]management.RuleInput{
			{Conditions: conditions(inSegment("employees")), Serve: serves("true")},
			{Serve: rolloutOf("user", "true", 2000)},
		},
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "qa-7", Variation: "true"}},
	})
	mustPut(t, admin, p.Key, "retired", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	if res := archive(t, admin, p.Key, "retired", 1); res.JSON200 == nil {
		t.Fatalf("archive retired = %d %s", res.StatusCode(), res.Body)
	}
	setProtected(t, admin, p.Key, "production", true)
	return guarded{p: p, rollout: on.Rules[1].Id.String()}
}

// Every write that changes what production serves is refused, whatever
// entity it touches, and no environment moves.
func TestWritesThatChangeAProtectedRulesetAreRefused(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	g := newGuarded(t, admin)
	p := g.p.Key
	c := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(g.p), access.RoleMaintainer))
	checkout := func() int64 { return getConfiguration(t, c, p, "checkout", "production").Version }
	flagVersion := func(key string) int64 { return getFlag(t, c, p, key).Version }
	segmentVersion := func() int64 { return do(c.GetSegmentWithResponse(t.Context(), p, "employees")).JSON200.Version }

	for _, tc := range []struct {
		name  string
		write func() (int, []byte)
	}{
		{"replacing the configuration", func() (int, []byte) {
			res := putConfiguration(t, c, p, "checkout", "production", checkout(),
				management.FlagConfigurationPut{On: false, OffVariation: "false", Fallthrough: "false"})
			return res.StatusCode(), res.Body
		}},
		{"setting an override", func() (int, []byte) {
			res := putOverride(t, c, p, "checkout", "production", "user", "qa-8", management.OverridePut{Variation: "true"})
			return res.StatusCode(), res.Body
		}},
		{"removing an override", func() (int, []byte) {
			res := do(c.DeleteOverrideWithResponse(t.Context(), p, "checkout", "production", "user", "qa-7"))
			return res.StatusCode(), res.Body
		}},
		{"regenerating a rollout's salt", func() (int, []byte) {
			res := do(c.RegenerateRolloutSaltWithResponse(t.Context(), p, "checkout", "production", g.rollout,
				&management.RegenerateRolloutSaltParams{IfMatch: ifMatch(checkout())}))
			return res.StatusCode(), res.Body
		}},
		{"reverting the configuration", func() (int, []byte) {
			res := revert(t, c, p, "checkout", "production", ifMatch(checkout()), 1)
			return res.StatusCode(), res.Body
		}},
		{"copying staging's configuration", func() (int, []byte) {
			res := copyFrom(t, c, p, "checkout", "production", ifMatch(checkout()), "staging", nil)
			return res.StatusCode(), res.Body
		}},
		{"an off flag's variation value", func() (int, []byte) {
			// banner is off in production, where it serves its off
			// variation's value.
			res := updateFlag(t, c, p, "banner", flagVersion("banner"), management.FlagPatch{
				Variations: variations("a", `"Welcome"`, "b", `"B"`, "c", `"C"`)})
			return res.StatusCode(), res.Body
		}},
		{"automatic exposure", func() (int, []byte) {
			res := updateFlag(t, c, p, "checkout", flagVersion("checkout"), management.FlagPatch{AutomaticExposure: ptr(true)})
			return res.StatusCode(), res.Body
		}},
		{"a referenced segment's conditions", func() (int, []byte) {
			res := updateSegment(t, c, p, "employees", segmentVersion(), management.SegmentPatch{
				Conditions: conditions(attr("country", "in", `["at"]`))})
			return res.StatusCode(), res.Body
		}},
		{"a referenced segment's private switch", func() (int, []byte) {
			res := updateSegment(t, c, p, "employees", segmentVersion(), management.SegmentPatch{Private: ptr(true)})
			return res.StatusCode(), res.Body
		}},
		{"archiving a flag on in production", func() (int, []byte) {
			res := archive(t, c, p, "checkout", flagVersion("checkout"))
			return res.StatusCode(), res.Body
		}},
		{"restoring a flag on in production", func() (int, []byte) {
			res := restore(t, c, p, "retired", flagVersion("retired"))
			return res.StatusCode(), res.Body
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versions := rulesetVersions(t, g.p)
			before := serverRuleset(t, c, p, "production")
			status, body := tc.write()
			refusedUnchanged(t, tc.name, g.p, versions, status, body)
			if after := serverRuleset(t, c, p, "production"); after != before {
				t.Errorf("production's ruleset changed:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

// Writes that change nothing production serves stay direct for a
// developer, and for a maintainer where the write is a maintainer's.
func TestWritesThatLeaveAProtectedRulesetAloneStayDirect(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	g := newGuarded(t, admin)
	p := g.p.Key
	developer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(g.p), access.RoleDeveloper))
	maintainer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(g.p), access.RoleMaintainer))
	flagVersion := func(key string) int64 { return getFlag(t, developer, p, key).Version }

	for _, tc := range []struct {
		name  string
		write func() (int, []byte)
	}{
		{"a flag's name, description and tags", func() (int, []byte) {
			res := updateFlag(t, developer, p, "checkout", flagVersion("checkout"), management.FlagPatch{
				Name: ptr("Checkout flow"), Description: ptr("The new checkout"), Tags: &[]string{"payments"}})
			return res.StatusCode(), res.Body
		}},
		{"a flag's lifetime", func() (int, []byte) {
			res := updateFlag(t, developer, p, "checkout", flagVersion("checkout"), management.FlagPatch{
				Lifetime: ptr(management.LifetimePermanent)})
			return res.StatusCode(), res.Body
		}},
		{"creating a flag", func() (int, []byte) {
			res := do(developer.CreateFlagWithResponse(t.Context(), p, management.FlagCreate{
				Key: "fresh", Type: management.FlagTypeBoolean, Name: "Fresh"}))
			return res.StatusCode(), res.Body
		}},
		{"creating a segment", func() (int, []byte) {
			res := do(developer.CreateSegmentWithResponse(t.Context(), p, management.SegmentCreate{Key: "beta", Name: "Beta"}))
			return res.StatusCode(), res.Body
		}},
		{"editing an unreferenced segment", func() (int, []byte) {
			s := do(developer.GetSegmentWithResponse(t.Context(), p, "unused")).JSON200
			res := updateSegment(t, developer, p, "unused", s.Version, management.SegmentPatch{
				Private: ptr(true), Conditions: conditions(attr("country", "in", `["es"]`))})
			return res.StatusCode(), res.Body
		}},
		{"deleting an unreferenced segment", func() (int, []byte) {
			s := do(maintainer.GetSegmentWithResponse(t.Context(), p, "unused")).JSON200
			res := deleteSegment(t, maintainer, p, "unused", s.Version)
			return res.StatusCode(), res.Body
		}},
		{"archiving a flag off in production", func() (int, []byte) {
			res := archive(t, maintainer, p, "quiet", flagVersion("quiet"))
			return res.StatusCode(), res.Body
		}},
		{"restoring a flag off in production", func() (int, []byte) {
			res := restore(t, maintainer, p, "quiet", flagVersion("quiet"))
			return res.StatusCode(), res.Body
		}},
		{"declaring an attribute", func() (int, []byte) {
			res := do(maintainer.CreateAttributeWithResponse(t.Context(), p, management.AttributeCreate{Key: "plan", Type: "string", Name: "Plan"}))
			return res.StatusCode(), res.Body
		}},
		{"declaring a unit type", func() (int, []byte) {
			res := do(maintainer.CreateUnitTypeWithResponse(t.Context(), p, management.UnitTypeCreate{Key: "account", Name: "Account"}))
			return res.StatusCode(), res.Body
		}},
		{"editing staging", func() (int, []byte) {
			res := putConfiguration(t, developer, p, "checkout", "staging", 1,
				management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
			return res.StatusCode(), res.Body
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := tc.write()
			if status >= 300 {
				t.Errorf("status = %d, body %s", status, body)
			}
		})
	}
}

// The kill switch is the deliberate exception: it switches a flag off in
// a protected environment with the permission it always needs, and
// switching the flag back on is an ordinary write again.
func TestTheKillSwitchChangesAProtectedRulesetDirectly(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	g := newGuarded(t, admin)
	p := g.p.Key
	developer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(g.p), access.RoleDeveloper))
	versions := rulesetVersions(t, g.p)

	killed := kill(t, developer, p, "checkout", "production", ptr("payments failing"))
	if killed.JSON200 == nil || !killed.JSON200.Changed || killed.JSON200.Configuration.On {
		t.Fatalf("developer's kill switch in production = %d %s", killed.StatusCode(), killed.Body)
	}
	if moved := bumped(t, "the kill switch", versions, rulesetVersions(t, g.p)); len(moved) != 1 || moved[0] != "production" {
		t.Errorf("the kill switch moved the rulesets of %v, want production alone", moved)
	}

	versions = rulesetVersions(t, g.p)
	back := putConfiguration(t, developer, p, "checkout", "production", killed.JSON200.Configuration.Version,
		management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	refusedUnchanged(t, "switching back on", g.p, versions, back.StatusCode(), back.Body)
}

// A write whose change reaches staging and production at once is refused
// whole: staging does not move either.
func TestAWriteChangingProtectedAndUnprotectedEnvironmentsIsRefusedWhole(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	g := newGuarded(t, admin)
	p := g.p.Key
	mustPut(t, admin, p, "checkout", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false",
		Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("employees")), Serve: serves("true")}}})
	versions := rulesetVersions(t, g.p)
	staging := serverRuleset(t, admin, p, "staging")

	s := do(admin.GetSegmentWithResponse(t.Context(), p, "employees")).JSON200
	res := updateSegment(t, admin, p, "employees", s.Version, management.SegmentPatch{Conditions: conditions(attr("country", "in", `["at"]`))})
	refusedUnchanged(t, "a segment staging and production reference", g.p, versions, res.StatusCode(), res.Body)
	if after := serverRuleset(t, admin, p, "staging"); after != staging {
		t.Errorf("staging's ruleset changed:\nbefore %s\nafter  %s", staging, after)
	}
	if got := do(admin.GetSegmentWithResponse(t.Context(), p, "employees")).JSON200; got.Version != s.Version {
		t.Errorf("the segment moved to version %d, want %d", got.Version, s.Version)
	}
}

// Validation and If-Match answer first: the guard judges a write that
// would otherwise be accepted.
func TestValidationAndIfMatchAnswerBeforeTheGuard(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	g := newGuarded(t, admin)
	p := g.p.Key
	current := getConfiguration(t, admin, p, "checkout", "production").Version
	off := management.FlagConfigurationPut{On: false, OffVariation: "false", Fallthrough: "false"}

	missing := do(admin.PutFlagConfigurationWithResponse(t.Context(), p, "checkout", "production",
		&management.PutFlagConfigurationParams{}, off))
	assertProblem(t, "without If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := putConfiguration(t, admin, p, "checkout", "production", current-1, off)
	assertProblem(t, "a stale If-Match", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	invalid := putConfiguration(t, admin, p, "checkout", "production", current,
		management.FlagConfigurationPut{On: false, OffVariation: "nope", Fallthrough: "false"})
	assertProblem(t, "an invalid configuration", invalid.StatusCode(), invalid.Body, http.StatusUnprocessableEntity, "invalid_configuration")
}

// The guard's time is observed on each write it judges, refused or not,
// and not on a write that reaches no protected environment.
func TestTheGuardsDurationIsObserved(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	g := newGuarded(t, admin)
	p := g.p.Key
	judged := func() int {
		t.Helper()
		for line := range strings.Lines(srv.Scrape(t)) {
			if rest, ok := strings.CutPrefix(line, "flagnetic_protection_guard_seconds_count "); ok {
				n, err := strconv.Atoi(strings.TrimSpace(rest))
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
		}
		t.Fatal("flagnetic_protection_guard_seconds is not exposed")
		return 0
	}

	before := judged()
	res := updateFlag(t, admin, p, "checkout", getFlag(t, admin, p, "checkout").Version, management.FlagPatch{AutomaticExposure: ptr(true)})
	assertProblem(t, "automatic exposure", res.StatusCode(), res.Body, http.StatusForbidden, "change_request_required")
	putConfiguration(t, admin, p, "checkout", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	if after := judged(); after != before+1 {
		t.Errorf("writes judged = %d, want %d: one refused, one in staging alone", after, before+1)
	}
}
