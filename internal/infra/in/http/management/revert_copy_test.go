package management_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func revert(t *testing.T, c *management.ClientWithResponses, project, flag, env string, ifMatch *string, version int64) *management.RevertFlagConfigurationResponse {
	t.Helper()
	return do(c.RevertFlagConfigurationWithResponse(t.Context(), project, flag, env,
		&management.RevertFlagConfigurationParams{IfMatch: ifMatch}, management.FlagConfigurationRevert{Version: version}))
}

func copyFrom(t *testing.T, c *management.ClientWithResponses, project, flag, env string, ifMatch *string, from string, overrides *bool) *management.CopyFlagConfigurationResponse {
	t.Helper()
	return do(c.CopyFlagConfigurationWithResponse(t.Context(), project, flag, env,
		&management.CopyFlagConfigurationParams{IfMatch: ifMatch}, management.FlagConfigurationCopy{From: from, IncludeOverrides: overrides}))
}

// targeting is a configuration switched on with a noted override, a rule
// on an attribute and a rollout.
func targeting() management.FlagConfigurationPut {
	return management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b",
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "u-1", Variation: "c", Note: ptr("Demo account")}},
		Rules: &[]management.RuleInput{
			{Description: ptr("Linux first"), Conditions: conditions(attr("os", "eq", `"linux"`)), Serve: serves("c")},
			{Description: ptr("A tenth"), Serve: rolloutOf("user", "b", 1000)},
		}}
}

// sameTargeting reports whether a and b serve the same and keep the same
// notes, authors and descriptions, whatever their versions and writers.
func sameTargeting(a, b management.FlagConfiguration) bool {
	a.Environment, a.UpdatedAt, a.UpdatedBy, a.Version = b.Environment, b.UpdatedAt, b.UpdatedBy, b.Version
	return reflect.DeepEqual(a, b)
}

// lastConfigurationEntry returns the flag's newest audit entry addressing
// an environment, with its after decoded.
func lastConfigurationEntry(t *testing.T, c *management.ClientWithResponses, project string, f management.Flag) (management.AuditEntry, map[string]any) {
	t.Helper()
	entries := configurationEntries(t, c, project, f, management.ListProjectAuditEntriesParams{})
	e := entries[len(entries)-1]
	data, _ := json.Marshal(e.After)
	var after map[string]any
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	return e, after
}

func TestARevertRestoresIdentifiersAndSaltsAfterARolloutWasDeleted(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "banner")
	v2 := mustPut(t, admin, p.Key, "banner", "staging", 1, targeting())
	cleared := asPut(v2)
	*cleared.Rules = (*cleared.Rules)[:1]
	mustPut(t, admin, p.Key, "banner", "staging", 2, cleared)
	before := rulesetVersions(t, p)

	res := revert(t, admin, p.Key, "banner", "staging", ifMatch(3), 2)
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"4"` || res.JSON200.Version != 4 {
		t.Fatalf("revert = %d %s", res.StatusCode(), res.Body)
	}
	if got := *res.JSON200; !sameTargeting(got, v2) {
		t.Errorf("reverted = %+v\nwant version 2 = %+v", got, v2)
	}
	if got := bumped(t, "revert", before, rulesetVersions(t, p)); !slices.Equal(got, []string{"staging"}) {
		t.Errorf("the revert bumped %v, want staging", got)
	}
	entry, after := lastConfigurationEntry(t, admin, p.Key, f)
	if entry.Action != "flag.reverted" || after["reverted_to"] != 2.0 || after["version"] != 4.0 {
		t.Errorf("entry %s after %v, want flag.reverted to 2 at version 4", entry.Action, after)
	}

	same := revert(t, admin, p.Key, "banner", "staging", ifMatch(4), 4)
	if same.JSON200 == nil || same.JSON200.Version != 4 {
		t.Errorf("reverting to the current version = %d %s, want nothing changed", same.StatusCode(), same.Body)
	}
	content := revert(t, admin, p.Key, "banner", "staging", ifMatch(4), 2)
	if content.JSON200 == nil || content.JSON200.Version != 4 {
		t.Errorf("reverting to the current content = %d %s, want nothing changed", content.StatusCode(), content.Body)
	}
	first := revert(t, admin, p.Key, "banner", "staging", ifMatch(4), 1)
	if first.JSON200 == nil || first.JSON200.Version != 5 || first.JSON200.On || len(first.JSON200.Rules) != 0 {
		t.Errorf("reverting to version 1 = %d %s, want the configuration the flag was created with", first.StatusCode(), first.Body)
	}
	if got := actionsOf(configurationEntries(t, admin, p.Key, f, management.ListProjectAuditEntriesParams{})); !slices.Equal(got,
		[]string{"flag.configuration_updated", "flag.configuration_updated", "flag.reverted", "flag.reverted"}) {
		t.Errorf("actions = %v, want one entry per revert that changed something", got)
	}
	again := revert(t, admin, p.Key, "banner", "staging", ifMatch(5), 4)
	if again.JSON200 == nil || !sameTargeting(*again.JSON200, v2) {
		t.Errorf("reverting to version 4, a revert's = %d %s", again.StatusCode(), again.Body)
	}
}

func TestARevertIsRefusedForWhatItCannotRestore(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "banner")
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Beta testers"})
	mustPut(t, admin, p.Key, "banner", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("beta_testers")), Serve: serves("c")}}})
	mustPut(t, admin, p.Key, "banner", "staging", 2, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a"})
	mustPut(t, admin, p.Key, "banner", "production", 1, management.FlagConfigurationPut{OffVariation: "b", Fallthrough: "b"})

	missing := revert(t, admin, p.Key, "banner", "staging", nil, 2)
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := revert(t, admin, p.Key, "banner", "staging", ifMatch(2), 2)
	assertProblem(t, "stale", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	notYet := revert(t, admin, p.Key, "banner", "staging", ifMatch(3), 9)
	assertProblem(t, "a version not yet written", notYet.StatusCode(), notYet.Body, http.StatusNotFound, "version_not_found")
	otherEnvironment := revert(t, admin, p.Key, "banner", "production", ifMatch(2), 3)
	assertProblem(t, "staging's version 3 in production", otherEnvironment.StatusCode(), otherEnvironment.Body,
		http.StatusNotFound, "version_not_found")

	deleteSegment(t, admin, p.Key, "beta_testers", 1)
	updateFlag(t, admin, p.Key, "banner", 1, management.FlagPatch{Variations: variations("a", `"A"`, "b", `"B"`)})
	res := revert(t, admin, p.Key, "banner", "staging", ifMatch(3), 2)
	if got := violationsOf(t, "what is gone", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/rules/0/conditions/0/segment unknown_segment", "/rules/0/serve/variation unknown_variation"}) {
		t.Errorf("violations = %v", got)
	}

	if created := do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: "qa", Name: "QA"})); created.JSON201 == nil {
		t.Fatalf("POST environments = %d %s", created.StatusCode(), created.Body)
	}
	mustPut(t, admin, p.Key, "banner", "qa", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	// The configuration qa's creation gave the flag is recorded by no entry
	// of its own, but whole in the before of the write after it.
	if first := revert(t, admin, p.Key, "banner", "qa", ifMatch(2), 1); first.JSON200 == nil || first.JSON200.On || first.JSON200.Version != 3 {
		t.Errorf("reverting qa to version 1 = %d %s, want it switched off again", first.StatusCode(), first.Body)
	}
	if deleted := do(admin.DeleteEnvironmentWithResponse(t.Context(), p.Key, "qa", &management.DeleteEnvironmentParams{IfMatch: ifMatch(1)})); deleted.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE qa = %d %s", deleted.StatusCode(), deleted.Body)
	}
	do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: "qa", Name: "QA again"}))
	recreated := revert(t, admin, p.Key, "banner", "qa", ifMatch(1), 2)
	assertProblem(t, "a version of the deleted qa", recreated.StatusCode(), recreated.Body, http.StatusNotFound, "version_not_found")

	archive(t, admin, p.Key, "banner", 2)
	archived := revert(t, admin, p.Key, "banner", "staging", ifMatch(3), 1)
	assertProblem(t, "an archived flag", archived.StatusCode(), archived.Body, http.StatusConflict, "archived")
}

func TestACopyKeepsTheTargetsOnOffAndTheSourcesIdentifiersAndSalts(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "banner")
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "gate", Type: management.FlagTypeBoolean, Name: "Gate"})
	source := targeting()
	source.Prerequisites = &[]management.Prerequisite{{Flag: "gate", Variation: "true"}}
	staging := mustPut(t, admin, p.Key, "banner", "staging", 1, source)
	production := mustPut(t, admin, p.Key, "banner", "production", 1, management.FlagConfigurationPut{OffVariation: "c", Fallthrough: "c",
		Overrides: &[]management.OverrideInput{{UnitType: "anonymous", UnitId: "d-9", Variation: "a"}}})
	before := rulesetVersions(t, p)

	res := copyFrom(t, admin, p.Key, "banner", "production", ifMatch(2), "staging", nil)
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"3"` {
		t.Fatalf("copy = %d %s", res.StatusCode(), res.Body)
	}
	got := *res.JSON200
	if got.On || got.OffVariation != "a" || got.Fallthrough != "b" || !reflect.DeepEqual(got.Prerequisites, staging.Prerequisites) ||
		!reflect.DeepEqual(got.Rules, staging.Rules) || !reflect.DeepEqual(got.Overrides, production.Overrides) {
		t.Errorf("copied = %+v, want staging's targeting with production's on/off and overrides", got)
	}
	if got := bumped(t, "copy", before, rulesetVersions(t, p)); !slices.Equal(got, []string{"production"}) {
		t.Errorf("the copy bumped %v, want production", got)
	}
	entry, after := lastConfigurationEntry(t, admin, p.Key, f)
	if entry.Action != "flag.configuration_copied" || after["source_environment"] != "staging" || after["source_version"] != 2.0 {
		t.Errorf("entry %s after %v, want flag.configuration_copied from staging at version 2", entry.Action, after)
	}

	differs := management.ListFlagsParams{DiffersBetween: &[]string{"staging", "production"}}
	if got := listing(t, admin, p.Key, differs); !slices.Equal(got, []string{"banner"}) {
		t.Errorf("differing after the copy = %v, want banner by its on/off and overrides", got)
	}
	withOverrides := copyFrom(t, admin, p.Key, "banner", "production", ifMatch(3), "staging", ptr(true))
	if withOverrides.JSON200 == nil || !reflect.DeepEqual(withOverrides.JSON200.Overrides, staging.Overrides) || withOverrides.JSON200.On {
		t.Fatalf("copy with overrides = %d %s, want staging's overrides with their notes and authors", withOverrides.StatusCode(), withOverrides.Body)
	}
	switchedOn := asPut(*withOverrides.JSON200)
	switchedOn.On = true
	described := mustPut(t, admin, p.Key, "banner", "production", 4, switchedOn)
	if got := listing(t, admin, p.Key, differs); len(got) != 0 {
		t.Errorf("differing once production is on = %v, want none", got)
	}
	same := copyFrom(t, admin, p.Key, "banner", "production", ifMatch(5), "staging", nil)
	if same.JSON200 == nil || same.JSON200.Version != 5 {
		t.Errorf("a copy changing nothing = %d %s", same.StatusCode(), same.Body)
	}
	redescribed := asPut(described)
	(*redescribed.Rules)[0].Description = ptr("Linux users first, in production")
	(*redescribed.Overrides)[0].Note = ptr("Production demo account")
	mustPut(t, admin, p.Key, "banner", "production", 5, redescribed)
	if got := listing(t, admin, p.Key, differs); len(got) != 0 {
		t.Errorf("differing by a rule description and an override note = %v, want none", got)
	}

	itself := copyFrom(t, admin, p.Key, "banner", "production", ifMatch(6), "production", nil)
	if got := violationsOf(t, "from itself", itself.StatusCode(), itself.Body, "invalid_configuration"); !slices.Equal(got, []string{"/from invalid_value"}) {
		t.Errorf("violations = %v", got)
	}
	unknown := copyFrom(t, admin, p.Key, "banner", "production", ifMatch(6), "qa", nil)
	assertProblem(t, "an unknown source", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
	missing := copyFrom(t, admin, p.Key, "banner", "production", nil, "staging", nil)
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := copyFrom(t, admin, p.Key, "banner", "development", ifMatch(2), "staging", nil)
	assertProblem(t, "stale", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	if !strings.Contains(string(stale.Body), `"current_version":1`) {
		t.Errorf("stale = %s, want the current version named", stale.Body)
	}
}
