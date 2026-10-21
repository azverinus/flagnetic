package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func putOverride(t *testing.T, c *management.ClientWithResponses, project, flag, env, unitType, unitID string, body management.OverridePut) *management.PutOverrideResponse {
	t.Helper()
	return do(c.PutOverrideWithResponse(t.Context(), project, flag, env, unitType, unitID, body))
}

func kill(t *testing.T, c *management.ClientWithResponses, project, flag, env string, reason *string) *management.KillFlagResponse {
	t.Helper()
	return do(c.KillFlagWithResponse(t.Context(), project, flag, env, management.Kill{Reason: reason}))
}

// configurationEntries returns the flag's audit entries addressing an
// environment, oldest first.
func configurationEntries(t *testing.T, c *management.ClientWithResponses, project string, f management.Flag, params management.ListProjectAuditEntriesParams) []management.AuditEntry {
	t.Helper()
	params.EntityId = ptr(f.Id.String())
	res := do(c.ListProjectAuditEntriesWithResponse(t.Context(), project, &params))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	var out []management.AuditEntry
	for _, e := range res.JSON200.Items {
		if e.EnvironmentId != nil {
			out = append(out, e)
		}
	}
	slices.Reverse(out)
	return out
}

func actionsOf(entries []management.AuditEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Action)
	}
	return out
}

func TestOneOverrideIsEditedWithoutAFullWrite(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "copy")
	before := rulesetVersions(t, p)

	res := putOverride(t, admin, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "b", Note: ptr("Demo")})
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` || len(res.JSON200.Overrides) != 1 ||
		valueOr(res.JSON200.Overrides[0].Note) != "Demo" {
		t.Fatalf("PUT override = %d %s", res.StatusCode(), res.Body)
	}
	if got := bumped(t, "set", before, rulesetVersions(t, p)); !slices.Equal(got, []string{"staging"}) {
		t.Errorf("setting an override bumped %v, want staging", got)
	}
	putOverride(t, admin, p.Key, "copy", "staging", "user", "u-2", management.OverridePut{Variation: "c"})
	replaced := putOverride(t, admin, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "c"})
	if replaced.JSON200 == nil || replaced.JSON200.Version != 4 || len(replaced.JSON200.Overrides) != 2 ||
		replaced.JSON200.Overrides[0].Variation != "c" || replaced.JSON200.Overrides[0].Note != nil {
		t.Errorf("replacing u-1 = %d %s, want it changed in place, its note cleared", replaced.StatusCode(), replaced.Body)
	}
	same := putOverride(t, admin, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "c"})
	if same.JSON200 == nil || same.JSON200.Version != 4 {
		t.Errorf("setting it as it is = %d %s, want nothing changed", same.StatusCode(), same.Body)
	}

	invalid := putOverride(t, admin, p.Key, "copy", "staging", "organization", strings.Repeat("x", 257),
		management.OverridePut{Variation: "z", Note: ptr(strings.Repeat("n", 201))})
	if got := violationsOf(t, "invalid", invalid.StatusCode(), invalid.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/unit_type unknown_unit_type", "/unit_id too_long", "/note too_long", "/variation unknown_variation"}) {
		t.Errorf("violations = %v", got)
	}

	removed := do(admin.DeleteOverrideWithResponse(t.Context(), p.Key, "copy", "staging", "user", "u-1"))
	if removed.JSON200 == nil || removed.HTTPResponse.Header.Get("ETag") != `"5"` || len(removed.JSON200.Overrides) != 1 ||
		removed.JSON200.Overrides[0].UnitId != "u-2" {
		t.Errorf("DELETE override = %d %s", removed.StatusCode(), removed.Body)
	}
	missing := do(admin.DeleteOverrideWithResponse(t.Context(), p.Key, "copy", "staging", "user", "u-1"))
	assertProblem(t, "removing a missing override", missing.StatusCode(), missing.Body, http.StatusNotFound, "not_found")

	if got := actionsOf(configurationEntries(t, admin, p.Key, f, management.ListProjectAuditEntriesParams{})); !slices.Equal(got,
		[]string{"flag.override_set", "flag.override_set", "flag.override_set", "flag.override_removed"}) {
		t.Errorf("actions = %v", got)
	}
}

func TestAddedByIsTheMemberWhoLastWroteTheOverride(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	developerToken := testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleDeveloper)
	developer := testkit.Client(t, srv, developerToken)
	developerID := me(t, srv, developerToken).Member.Id

	set := putOverride(t, admin, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "b"})
	if set.JSON200 == nil || *set.JSON200.Overrides[0].AddedBy != adminID {
		t.Fatalf("set = %d %s, want added by the admin", set.StatusCode(), set.Body)
	}
	again := putOverride(t, developer, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "b"})
	if again.JSON200 == nil || *again.JSON200.Overrides[0].AddedBy != adminID {
		t.Errorf("setting it as it is = %d %s, want it still the admin's", again.StatusCode(), again.Body)
	}
	noted := putOverride(t, developer, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "b", Note: ptr("Sales demo")})
	if noted.JSON200 == nil || *noted.JSON200.Overrides[0].AddedBy != developerID || *noted.JSON200.UpdatedBy != developerID {
		t.Errorf("noted = %d %s, want it the developer's", noted.StatusCode(), noted.Body)
	}
}

func TestAnOverrideNoteReachesNoRulesetAndNoEvaluation(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	mustPut(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a"})
	putOverride(t, admin, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "b"})
	before := rulesetVersions(t, p)
	evaluated := func(draft *management.EvaluationDraft) string {
		t.Helper()
		got := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user("u-1"), ""), Draft: draft})
		if got.Flags["copy"].Reason != "override" {
			t.Errorf("u-1 = %+v, want its override", got.Flags["copy"])
		}
		out, _ := json.Marshal(got)
		return string(out)
	}
	unnoted := evaluated(nil)
	noted := putOverride(t, admin, p.Key, "copy", "staging", "user", "u-1", management.OverridePut{Variation: "b", Note: ptr("Pinned for the demo")})
	if noted.JSON200 == nil || noted.JSON200.Version != 4 {
		t.Fatalf("noting = %d %s", noted.StatusCode(), noted.Body)
	}
	body := asPut(*noted.JSON200)
	(*body.Overrides)[0].Note = ptr("Pinned for the sales demo")
	mustPut(t, admin, p.Key, "copy", "staging", 4, body)
	if got := bumped(t, "notes", before, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("writing notes bumped %v, want no ruleset changed", got)
	}
	if noted := evaluated(nil); noted != unnoted {
		t.Errorf("evaluation with a note = %s, without = %s, want them the same", noted, unnoted)
	}
	(*body.Overrides)[0].Note = ptr("A note only the draft has")
	if drafted := evaluated(&management.EvaluationDraft{Flag: "copy", Configuration: body}); drafted != unnoted {
		t.Errorf("evaluation of a draft with a note = %s, want %s", drafted, unnoted)
	}
}

func TestRolloutsCarryAGeneratedSaltOnlyRegenerationChanges(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "copy")
	c := mustPut(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 2000)}, {Serve: serves("c")}}})
	rollout, variation := c.Rules[0].Id.String(), c.Rules[1].Id.String()
	regenerate := func(rule string, version *string) *management.RegenerateRolloutSaltResponse {
		return do(admin.RegenerateRolloutSaltWithResponse(t.Context(), p.Key, "copy", "staging", rule,
			&management.RegenerateRolloutSaltParams{IfMatch: version}))
	}
	before := rulesetVersions(t, p)

	missing := regenerate(rollout, nil)
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := regenerate(rollout, ifMatch(1))
	assertProblem(t, "stale", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	notRollout := regenerate(variation, ifMatch(2))
	assertProblem(t, "a variation serve", notRollout.StatusCode(), notRollout.Body, http.StatusConflict, "not_a_rollout")
	for _, rule := range []string{"not-a-rule", f.Id.String()} {
		unknown := regenerate(rule, ifMatch(2))
		assertProblem(t, "rule "+rule, unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
	}
	res := regenerate(rollout, ifMatch(2))
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"3"` {
		t.Fatalf("regenerate = %d %s", res.StatusCode(), res.Body)
	}
	if s := res.JSON200.Rules[0].Serve.Salt; s == nil || len(*s) != 16 || *s == *c.Rules[0].Serve.Salt || res.JSON200.Rules[0].Id != c.Rules[0].Id {
		t.Errorf("salt = %v, want a new one of 16 characters on the same rule", s)
	}
	if got := bumped(t, "regenerate", before, rulesetVersions(t, p)); !slices.Equal(got, []string{"staging"}) {
		t.Errorf("regenerating bumped %v, want staging", got)
	}
	if got := actionsOf(configurationEntries(t, admin, p.Key, f, management.ListProjectAuditEntriesParams{})); !slices.Equal(got,
		[]string{"flag.configuration_updated", "flag.salt_regenerated"}) {
		t.Errorf("actions = %v", got)
	}
}

func TestKillSwitchSwitchesAFlagOffWithoutAVersion(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "copy")
	on := mustPut(t, admin, p.Key, "copy", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b",
		Rules: &[]management.RuleInput{{Serve: serves("c")}}})
	before := rulesetVersions(t, p)

	res := kill(t, admin, p.Key, "copy", "production", ptr("  Checkout failing for every user  "))
	if res.JSON200 == nil || !res.JSON200.Changed || res.JSON200.Configuration.On || res.JSON200.Configuration.Version != on.Version+1 ||
		res.HTTPResponse.Header.Get("ETag") != `"3"` || len(res.JSON200.Configuration.Rules) != 1 || res.JSON200.Configuration.Fallthrough != "b" {
		t.Fatalf("kill = %d %s, want switched off and nothing else changed", res.StatusCode(), res.Body)
	}
	if got := bumped(t, "kill", before, rulesetVersions(t, p)); !slices.Equal(got, []string{"production"}) {
		t.Errorf("the kill switch bumped %v, want production", got)
	}

	again := kill(t, admin, p.Key, "copy", "production", nil)
	if again.JSON200 == nil || again.JSON200.Changed || again.JSON200.Configuration.Version != 3 {
		t.Errorf("killing an off flag = %d %s, want nothing changed", again.StatusCode(), again.Body)
	}
	noBody := do(admin.KillFlagWithBodyWithResponse(t.Context(), p.Key, "copy", "staging", "application/json", strings.NewReader("")))
	if noBody.JSON200 == nil || noBody.JSON200.Changed {
		t.Errorf("a kill without a body = %d %s", noBody.StatusCode(), noBody.Body)
	}

	kills := configurationEntries(t, admin, p.Key, f, management.ListProjectAuditEntriesParams{KillSwitch: ptr(true)})
	if got := actionsOf(kills); !slices.Equal(got, []string{"flag.killed"}) || !kills[0].KillSwitch {
		t.Fatalf("kill switch entries = %v, want the one kill", got)
	}
	after, _ := json.Marshal(kills[0].After)
	if !strings.Contains(string(after), `"reason":"Checkout failing for every user"`) || !strings.Contains(string(after), `"on":false`) {
		t.Errorf("flag.killed after = %s, want the configuration with the reason", after)
	}
	others := configurationEntries(t, admin, p.Key, f, management.ListProjectAuditEntriesParams{KillSwitch: ptr(false)})
	if got := actionsOf(others); !slices.Equal(got, []string{"flag.configuration_updated"}) || others[0].KillSwitch {
		t.Errorf("other entries = %v, want the replace alone", got)
	}
	instance := do(admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{KillSwitch: ptr(true)}))
	if instance.JSON200 == nil || len(instance.JSON200.Items) == 0 || !slices.ContainsFunc(instance.JSON200.Items, func(e management.AuditEntry) bool {
		return e.Entity.Id == f.Id.String() && e.Action == "flag.killed"
	}) {
		t.Errorf("the instance's kill switches = %d %s", instance.StatusCode(), instance.Body)
	}
}

func TestKillSwitchIsRefusedLikeAnyConfigurationWrite(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	mustPut(t, admin, p.Key, "copy", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	viewer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleViewer))

	for name, tc := range map[string]struct {
		res    *management.KillFlagResponse
		status int
		code   string
	}{
		"an unknown flag":        {kill(t, admin, p.Key, "absent", "production", nil), http.StatusNotFound, "not_found"},
		"an unknown environment": {kill(t, admin, p.Key, "copy", "qa", nil), http.StatusNotFound, "not_found"},
		"a viewer":               {kill(t, viewer, p.Key, "copy", "production", nil), http.StatusForbidden, "forbidden"},
	} {
		assertProblem(t, name, tc.res.StatusCode(), tc.res.Body, tc.status, tc.code)
	}
	long := kill(t, admin, p.Key, "copy", "production", ptr(strings.Repeat("r", 2001)))
	if got := violationsOf(t, "an overlong reason", long.StatusCode(), long.Body, "invalid_configuration"); !slices.Equal(got, []string{"/reason too_long"}) {
		t.Errorf("violations = %v", got)
	}
	if c := getConfiguration(t, admin, p.Key, "copy", "production"); !c.On || c.Version != 2 {
		t.Errorf("after refusals = %+v, want it on as it was", c)
	}
	archive(t, admin, p.Key, "copy", 1)
	archived := kill(t, admin, p.Key, "copy", "production", nil)
	assertProblem(t, "an archived flag", archived.StatusCode(), archived.Body, http.StatusConflict, "archived")
}
