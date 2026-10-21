package management_test

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// kindedReferencesOf returns "kind key environment" for each reference of a
// 409 in_use answer.
func kindedReferencesOf(t *testing.T, name string, status int, body []byte) []string {
	t.Helper()
	pr := assertProblem(t, name, status, body, http.StatusConflict, "in_use")
	var out []string
	if pr.References != nil {
		for _, r := range *pr.References {
			out = append(out, r.Kind+" "+r.Key+" "+valueOr(r.Environment))
		}
	}
	return out
}

// declare creates the string attribute key in project as c.
func declare(t *testing.T, c *management.ClientWithResponses, project, key string) {
	t.Helper()
	res := do(c.CreateAttributeWithResponse(t.Context(), project, management.AttributeCreate{Key: key, Type: "string", Name: key}))
	if res.JSON201 == nil {
		t.Fatalf("POST attributes %s = %d %s", key, res.StatusCode(), res.Body)
	}
}

// declareUnitType creates the unit type key in project as c.
func declareUnitType(t *testing.T, c *management.ClientWithResponses, project, key string) {
	t.Helper()
	res := do(c.CreateUnitTypeWithResponse(t.Context(), project, management.UnitTypeCreate{Key: key, Name: key}))
	if res.JSON201 == nil {
		t.Fatalf("POST unit types %s = %d %s", key, res.StatusCode(), res.Body)
	}
}

// referencingProject creates a project whose rules, rollouts, overrides
// and segments name the attributes plan and region and the unit type
// organization, and where the attribute spare and the unit type device are
// named by nothing:
//
//   - segment paying: plan; segment named_organizations: organization;
//   - new_checkout: a rule on region in staging, an override of an
//     organization in production;
//   - banner: a rollout by organization and a rule in segment paying in
//     development and staging;
//   - retired, archived: a rule on plan in development.
func referencingProject(t *testing.T, c *management.ClientWithResponses) management.Project {
	t.Helper()
	p := createProject(t, c, projectKey())
	for _, key := range []string{"plan", "region", "spare"} {
		declare(t, c, p.Key, key)
	}
	declareUnitType(t, c, p.Key, "organization")
	declareUnitType(t, c, p.Key, "device")
	createSegment(t, c, p.Key, management.SegmentCreate{Key: "paying", Name: "Paying", Conditions: conditions(attr("plan", "in", `["pro","team"]`))})
	createSegment(t, c, p.Key, management.SegmentCreate{Key: "named_organizations", Name: "Named organizations",
		Conditions: conditions(attr("organization", "in", `["acme"]`))})
	boolean := func(key string) {
		createFlag(t, c, p.Key, management.FlagCreate{Key: key, Type: management.FlagTypeBoolean, Name: key})
	}
	boolean("new_checkout")
	boolean("banner")
	boolean("retired")
	mustPut(t, c, p.Key, "new_checkout", "staging", 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false",
		Rules: &[]management.RuleInput{{Conditions: conditions(attr("region", "eq", `"eu"`)), Serve: serves("true")}}})
	mustPut(t, c, p.Key, "new_checkout", "production", 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false",
		Overrides: &[]management.OverrideInput{{UnitType: "organization", UnitId: "acme", Variation: "true"}}})
	for _, env := range []string{"development", "staging"} {
		mustPut(t, c, p.Key, "banner", env, 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false",
			Rules: &[]management.RuleInput{
				{Conditions: conditions(inSegment("paying")), Serve: serves("true")},
				{Serve: rolloutOf("organization", "true", 2500)},
			}})
	}
	mustPut(t, c, p.Key, "retired", "development", 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false",
		Rules: &[]management.RuleInput{{Conditions: conditions(attr("plan", "eq", `"free"`)), Serve: serves("true")}}})
	archive(t, c, p.Key, "retired", 1)
	return p
}

func TestDeletingAReferencedDeclarationIsRefusedWithItsReferences(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := referencingProject(t, admin)
	before := rulesetVersions(t, p)

	cases := []struct {
		name string
		call func() (int, []byte)
		want []string
	}{
		{"an attribute a segment and an archived flag's rule name", func() (int, []byte) {
			r := do(admin.DeleteAttributeWithResponse(t.Context(), p.Key, "plan", &management.DeleteAttributeParams{IfMatch: ifMatch(1)}))
			return r.StatusCode(), r.Body
		}, []string{"segment paying ", "flag retired development"}},
		{"an attribute a rule names", func() (int, []byte) {
			r := do(admin.DeleteAttributeWithResponse(t.Context(), p.Key, "region", &management.DeleteAttributeParams{IfMatch: ifMatch(1)}))
			return r.StatusCode(), r.Body
		}, []string{"flag new_checkout staging"}},
		{"a unit type a segment, a rollout and an override name", func() (int, []byte) {
			r := do(admin.DeleteUnitTypeWithResponse(t.Context(), p.Key, "organization", &management.DeleteUnitTypeParams{IfMatch: ifMatch(1)}))
			return r.StatusCode(), r.Body
		}, []string{"segment named_organizations ", "flag banner development", "flag banner staging", "flag new_checkout production"}},
	}
	for _, tc := range cases {
		status, body := tc.call()
		if got := kindedReferencesOf(t, tc.name, status, body); !slices.Equal(got, tc.want) {
			t.Errorf("%s: references = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := bumped(t, "refusals", before, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("refusals bumped %v", got)
	}
	for _, key := range []string{"plan", "region"} {
		if r := do(admin.GetAttributeWithResponse(t.Context(), p.Key, key)); r.JSON200 == nil {
			t.Errorf("GET attribute %s after a refused delete = %d", key, r.StatusCode())
		}
	}
	if r := do(admin.GetUnitTypeWithResponse(t.Context(), p.Key, "organization")); r.JSON200 == nil {
		t.Errorf("GET unit type organization after a refused delete = %d", r.StatusCode())
	}

	if r := do(admin.DeleteAttributeWithResponse(t.Context(), p.Key, "spare", &management.DeleteAttributeParams{IfMatch: ifMatch(1)})); r.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting an attribute nothing names = %d %s", r.StatusCode(), r.Body)
	}
	if r := do(admin.DeleteUnitTypeWithResponse(t.Context(), p.Key, "device", &management.DeleteUnitTypeParams{IfMatch: ifMatch(1)})); r.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting a unit type nothing names = %d %s", r.StatusCode(), r.Body)
	}
	mustPut(t, admin, p.Key, "new_checkout", "staging", 2, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false"})
	if r := do(admin.DeleteAttributeWithResponse(t.Context(), p.Key, "region", &management.DeleteAttributeParams{IfMatch: ifMatch(1)})); r.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting an attribute no rule names any more = %d %s", r.StatusCode(), r.Body)
	}
}

// Creating or deleting a unit type or an attribute changes what every
// environment's ruleset declares; renaming one or changing its description
// changes nothing an SDK receives.
func TestDeclarationWritesBumpEveryEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	steps := []struct {
		name   string
		write  func() (int, []byte)
		status int
		bumps  bool
	}{
		{"create a unit type", func() (int, []byte) {
			r := do(admin.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: "organization", Name: "Organization"}))
			return r.StatusCode(), r.Body
		}, http.StatusCreated, true},
		{"rename a unit type", func() (int, []byte) {
			r := do(admin.UpdateUnitTypeWithResponse(t.Context(), p.Key, "organization", &management.UpdateUnitTypeParams{IfMatch: ifMatch(1)}, management.UnitTypePatch{Name: ptr("Company")}))
			return r.StatusCode(), r.Body
		}, http.StatusOK, false},
		{"delete a unit type", func() (int, []byte) {
			r := do(admin.DeleteUnitTypeWithResponse(t.Context(), p.Key, "organization", &management.DeleteUnitTypeParams{IfMatch: ifMatch(2)}))
			return r.StatusCode(), r.Body
		}, http.StatusNoContent, true},
		{"create an attribute", func() (int, []byte) {
			r := do(admin.CreateAttributeWithResponse(t.Context(), p.Key, management.AttributeCreate{Key: "plan", Type: "string", Name: "Plan"}))
			return r.StatusCode(), r.Body
		}, http.StatusCreated, true},
		{"describe an attribute", func() (int, []byte) {
			r := do(admin.UpdateAttributeWithResponse(t.Context(), p.Key, "plan", &management.UpdateAttributeParams{IfMatch: ifMatch(1)}, management.AttributePatch{Description: ptr("What they pay for.")}))
			return r.StatusCode(), r.Body
		}, http.StatusOK, false},
		{"delete a standard attribute", func() (int, []byte) {
			r := do(admin.DeleteAttributeWithResponse(t.Context(), p.Key, "os", &management.DeleteAttributeParams{IfMatch: ifMatch(1)}))
			return r.StatusCode(), r.Body
		}, http.StatusConflict, false},
		{"delete an attribute", func() (int, []byte) {
			r := do(admin.DeleteAttributeWithResponse(t.Context(), p.Key, "plan", &management.DeleteAttributeParams{IfMatch: ifMatch(2)}))
			return r.StatusCode(), r.Body
		}, http.StatusNoContent, true},
	}
	for _, s := range steps {
		before := rulesetVersions(t, p)
		status, body := s.write()
		if status != s.status {
			t.Errorf("%s = %d %s, want %d", s.name, status, body, s.status)
			continue
		}
		want := []string(nil)
		if s.bumps {
			want = everyEnvironment
		}
		if got := bumped(t, s.name, before, rulesetVersions(t, p)); !slices.Equal(got, want) {
			t.Errorf("%s bumped %v, want %v", s.name, got, want)
		}
	}
}

func TestUnitTypesAttributesAndSegmentsAreListedWithTheirUsageCounts(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := referencingProject(t, admin)

	units := do(admin.ListUnitTypesWithResponse(t.Context(), p.Key))
	if units.JSON200 == nil {
		t.Fatalf("GET unit types = %d %s", units.StatusCode(), units.Body)
	}
	gotUnits := map[string]int{}
	for _, u := range units.JSON200.Items {
		gotUnits[u.Key] = u.UsageCount
	}
	wantUnits := map[string]int{"anonymous": 0, "device": 0, "organization": 3, "user": 0}
	if !maps.Equal(gotUnits, wantUnits) {
		t.Errorf("unit type usage counts = %v, want %v", gotUnits, wantUnits)
	}

	attrs := do(admin.ListAttributesWithResponse(t.Context(), p.Key))
	if attrs.JSON200 == nil {
		t.Fatalf("GET attributes = %d %s", attrs.StatusCode(), attrs.Body)
	}
	gotAttrs := map[string]int{}
	for _, a := range attrs.JSON200.Items {
		if !a.Standard || a.UsageCount != 0 {
			gotAttrs[a.Key] = a.UsageCount
		}
	}
	wantAttrs := map[string]int{"plan": 2, "region": 1, "spare": 0}
	if !maps.Equal(gotAttrs, wantAttrs) {
		t.Errorf("declared attribute usage counts = %v, want %v and every standard attribute unused", gotAttrs, wantAttrs)
	}

	segments := do(admin.ListSegmentsWithResponse(t.Context(), p.Key))
	if segments.JSON200 == nil {
		t.Fatalf("GET segments = %d %s", segments.StatusCode(), segments.Body)
	}
	gotSegments := map[string]int{}
	for _, s := range segments.JSON200.Items {
		gotSegments[s.Key] = s.UsageCount
	}
	if want := map[string]int{"named_organizations": 0, "paying": 1}; !maps.Equal(gotSegments, want) {
		t.Errorf("segment usage counts = %v, want %v", gotSegments, want)
	}
}

// Listing unit types, attributes or segments costs as many statements in a
// project whose flags and segments name them as in one where nothing does:
// the counts of a list are read in one query.
func TestUsageCountsCostOneQueryPerList(t *testing.T) {
	pool, queries := testkit.TracedPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	empty, used := createProject(t, admin, projectKey()), referencingProject(t, admin)
	lists := map[string]func(project string) int{
		"unit types": func(project string) int {
			return do(admin.ListUnitTypesWithResponse(t.Context(), project)).StatusCode()
		},
		"attributes": func(project string) int {
			return do(admin.ListAttributesWithResponse(t.Context(), project)).StatusCode()
		},
		"segments": func(project string) int {
			return do(admin.ListSegmentsWithResponse(t.Context(), project)).StatusCode()
		},
	}
	for name, list := range lists {
		statements := func(project string) int {
			list(project)
			queries.Reset()
			if status := list(project); status != http.StatusOK {
				t.Fatalf("GET %s = %d", name, status)
			}
			return queries.Containing("")
		}
		if e, u := statements(empty.Key), statements(used.Key); e != u {
			t.Errorf("listing %s costs %d statements with nothing named and %d with flags and segments naming them", name, e, u)
		}
	}
}
