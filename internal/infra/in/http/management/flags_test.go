package management_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// value returns v as a variation's JSON value.
func value(v string) management.VariationValue { return management.VariationValue(v) }

func variations(kv ...string) *[]management.VariationInput {
	out := make([]management.VariationInput, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, management.VariationInput{Key: kv[i], Value: value(kv[i+1])})
	}
	return &out
}

// createFlag creates a flag as c, failing unless it is created at version
// 1 with that version as its ETag.
func createFlag(t *testing.T, c *management.ClientWithResponses, project string, body management.FlagCreate) management.Flag {
	t.Helper()
	res := do(c.CreateFlagWithResponse(t.Context(), project, body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST flags %s = %d, ETag %q: %s", body.Key, res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

func getFlag(t *testing.T, c *management.ClientWithResponses, project, key string) management.Flag {
	t.Helper()
	res := do(c.GetFlagWithResponse(t.Context(), project, key))
	if res.JSON200 == nil {
		t.Fatalf("GET flag %s = %d: %s", key, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// ifMatch names version v as the version a write is based on.
func ifMatch(v int64) *string { return ptr(`"` + strconv.FormatInt(v, 10) + `"`) }

// uuidOf returns m as the API names a member.
func uuidOf(m access.MemberID) *openapi_types.UUID { return ptr(openapi_types.UUID(m.Bytes())) }

func updateFlag(t *testing.T, c *management.ClientWithResponses, project, key string, version int64, body management.FlagPatch) *management.UpdateFlagResponse {
	t.Helper()
	return do(c.UpdateFlagWithResponse(t.Context(), project, key, &management.UpdateFlagParams{IfMatch: ifMatch(version)}, body))
}

// rulesetVersions returns the ruleset version of every environment of
// project, as the delivery repository reads it.
func rulesetVersions(t *testing.T, p management.Project) map[string]int64 {
	t.Helper()
	v, err := pgdelivery.NewStore(testkit.Pool(t)).RulesetVersions(t.Context(), projectIDOf(p))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// bumped returns the environments whose ruleset version moved between
// before and after, failing if one moved by more than one.
func bumped(t *testing.T, name string, before, after map[string]int64) []string {
	t.Helper()
	var out []string
	for env, v := range after {
		switch v - before[env] {
		case 0:
		case 1:
			out = append(out, env)
		default:
			t.Errorf("%s: %s moved from %d to %d, want at most one step", name, env, before[env], v)
		}
	}
	slices.Sort(out)
	return out
}

var everyEnvironment = []string{"development", "production", "staging"}

// violationsOf returns "pointer code" for each violation of a 422 answer.
func violationsOf(t *testing.T, name string, status int, body []byte, code string) []string {
	t.Helper()
	p := assertProblem(t, name, status, body, http.StatusUnprocessableEntity, code)
	var out []string
	if p.Errors != nil {
		for _, v := range *p.Errors {
			out = append(out, v.Pointer+" "+v.Code)
		}
	}
	return out
}

func TestCreatingAFlagConfiguresItOffInEveryEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	before := rulesetVersions(t, p)

	f := createFlag(t, admin, p.Key, management.FlagCreate{
		Key: "new_checkout", Type: management.FlagTypeBoolean, Name: "New checkout", Description: ptr("Pays in one step"),
		Tags: &[]string{"checkout", "mobile"},
	})
	res := do(admin.GetFlagWithResponse(t.Context(), p.Key, "new_checkout"))
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("GET = %d %s, ETag %q, want the flag at version 1", res.StatusCode(), res.Body, res.HTTPResponse.Header.Get("ETag"))
	}
	if got, want := raw(*res.JSON200), raw(f); got != want {
		t.Errorf("GET = %s, want what creating answered: %s", got, want)
	}
	vs, _ := json.Marshal(f.Variations)
	if string(vs) != `[{"description":"","key":"true","name":"true","value":true},{"description":"","key":"false","name":"false","value":false}]` {
		t.Errorf("variations = %s, want true and false", vs)
	}
	if f.Version != 1 || f.AutomaticExposure || f.ArchivedAt != nil || f.Owner != adminID || f.CreatedBy == nil || *f.CreatedBy != adminID {
		t.Errorf("flag = %s, want version 1 owned and created by its creator", raw(f))
	}
	if f.Lifetime != management.LifetimeTemporary || f.ExpectedRemovalAt == nil || f.ExpectedRemovalAt.Before(time.Now().AddDate(0, 0, 89)) {
		t.Errorf("lifetime %s until %v, want temporary for 90 days", f.Lifetime, f.ExpectedRemovalAt)
	}
	var envs []string
	for _, c := range f.Environments {
		envs = append(envs, c.Environment)
		if c.On || c.OffVariation != "false" || c.Fallthrough != "true" || c.Version != 1 {
			t.Errorf("%s = %+v, want off serving false, falling through to true, at version 1", c.Environment, c)
		}
	}
	if !slices.Equal(envs, []string{"development", "staging", "production"}) {
		t.Errorf("environments = %v, want every environment by its order", envs)
	}
	if got := bumped(t, "create", before, rulesetVersions(t, p)); !slices.Equal(got, everyEnvironment) {
		t.Errorf("bumped %v, want every environment", got)
	}

	str := createFlag(t, admin, p.Key, management.FlagCreate{
		Key: "Checkout.Title", Type: management.FlagTypeString, Name: "Checkout title", Lifetime: ptr(management.LifetimePermanent),
		Variations: &[]management.VariationInput{
			{Key: "short", Value: value(`"Pay"`), Name: ptr("Short")},
			{Key: "long", Value: value(`"Pay for the session"`), Description: ptr("Longer")},
		},
	})
	if str.ExpectedRemovalAt != nil || str.Variations[1].Name != "long" || str.Tags == nil || len(str.Tags) != 0 {
		t.Errorf("string flag = %s, want permanent, long named after its key, no tags", raw(str))
	}
	for _, c := range str.Environments {
		if c.On || c.OffVariation != "short" || c.Fallthrough != "short" {
			t.Errorf("%s = %+v, want off with the first variation everywhere", c.Environment, c)
		}
	}
	created := auditOf(t, srv, "flag.created", f.Id.String())
	if len(created) != 1 || created[0].Before != nil || created[0].Entity.Kind != "flag" || created[0].Entity.Key != "new_checkout" {
		t.Fatalf("audit = %+v, want one flag.created entry", created)
	}
	after, _ := json.Marshal(created[0].After)
	for _, want := range []string{`"key":"new_checkout"`, `"version":1`, `"environment":"production"`, `"off_variation":"false"`} {
		if !strings.Contains(string(after), want) {
			t.Errorf("after = %s, want it to hold %s", after, want)
		}
	}
}

func TestANewEnvironmentConfiguresEveryExistingFlag(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "limit", Type: management.FlagTypeNumber, Name: "Limit",
		Variations: variations("low", "5", "high", "50")})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "beta", Type: management.FlagTypeBoolean, Name: "Beta"})
	archived := do(admin.ArchiveFlagWithResponse(t.Context(), p.Key, "beta", &management.ArchiveFlagParams{IfMatch: ifMatch(1)}))
	status(t, "archive beta", archived.StatusCode(), archived.Body, http.StatusOK)

	created := do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	status(t, "create qa", created.StatusCode(), created.Body, http.StatusCreated)
	for key, want := range map[string][2]string{"limit": {"low", "low"}, "beta": {"false", "true"}} {
		f := getFlag(t, admin, p.Key, key)
		qa := f.Environments[len(f.Environments)-1]
		if qa.Environment != "qa" || qa.On || qa.OffVariation != want[0] || qa.Fallthrough != want[1] || qa.Version != 1 {
			t.Errorf("%s in qa = %+v, want off serving %v", key, qa, want)
		}
	}
	if v := rulesetVersions(t, p)["qa"]; v != 1 {
		t.Errorf("qa's ruleset version = %d, want 1", v)
	}
	deleted := do(admin.DeleteEnvironmentWithResponse(t.Context(), p.Key, "qa", &management.DeleteEnvironmentParams{IfMatch: ifMatch(1)}))
	status(t, "delete qa", deleted.StatusCode(), deleted.Body, http.StatusNoContent)
	var envs []string
	for _, c := range getFlag(t, admin, p.Key, "limit").Environments {
		envs = append(envs, c.Environment)
	}
	if !slices.Equal(envs, []string{"development", "staging", "production"}) {
		t.Errorf("environments after deleting qa = %v", envs)
	}
}

func TestFlagKeysAndTypesAreValidatedAndImmutable(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	boolean := func(key string) management.FlagCreate {
		return management.FlagCreate{Key: key, Type: management.FlagTypeBoolean, Name: "X"}
	}
	for _, key := range []string{"", "1st", "_x", "has space", "ключ", strings.Repeat("a", 129)} {
		res := do(admin.CreateFlagWithResponse(t.Context(), p.Key, boolean(key)))
		if got := violationsOf(t, "key "+key, res.StatusCode(), res.Body, "invalid_flag"); !slices.Equal(got, []string{"/key invalid_key"}) {
			t.Errorf("key %q: violations = %v", key, got)
		}
	}
	for _, key := range []string{"a", "Checkout.V2", "new-checkout_3", strings.Repeat("a", 128), "A"} {
		createFlag(t, admin, p.Key, boolean(key))
	}
	yaml := do(admin.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: "y", Type: "yaml", Name: " "}))
	if got := violationsOf(t, "yaml", yaml.StatusCode(), yaml.Body, "invalid_flag"); !slices.Equal(got, []string{"/type invalid_type", "/name invalid_name"}) {
		t.Errorf("violations = %v", got)
	}
	dup := do(admin.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: "a", Type: management.FlagTypeString, Name: "X",
		Variations: variations("v", `"v"`)}))
	assertProblem(t, "a key in use", dup.StatusCode(), dup.Body, http.StatusConflict, "already_exists")
	other := createProject(t, admin, projectKey())
	createFlag(t, admin, other.Key, boolean("a"))

	rekey := updateFlag(t, admin, p.Key, "a", 1, management.FlagPatch{Key: ptr("b")})
	if got := violationsOf(t, "a new key", rekey.StatusCode(), rekey.Body, "invalid_flag"); !slices.Equal(got, []string{"/key immutable"}) {
		t.Errorf("violations = %v", got)
	}
	retype := updateFlag(t, admin, p.Key, "a", 1, management.FlagPatch{Type: ptr("string"), Name: ptr("Renamed")})
	if got := violationsOf(t, "a new type", retype.StatusCode(), retype.Body, "invalid_flag"); !slices.Equal(got, []string{"/type immutable"}) {
		t.Errorf("violations = %v", got)
	}
	if f := getFlag(t, admin, p.Key, "a"); f.Name != "X" || f.Version != 1 {
		t.Errorf("after refused changes: %s, want it untouched", raw(f))
	}
}

func TestVariationsAreTypedByTheFlagType(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	big := `"` + strings.Repeat("v", 64*1024-2) + `"`
	for name, tc := range map[string]struct {
		typ management.FlagType
		vs  *[]management.VariationInput
	}{
		"a boolean in any order": {management.FlagTypeBoolean, variations("false", "false", "true", "true")},
		"an empty string":        {management.FlagTypeString, variations("e", `""`)},
		"numbers":                {management.FlagTypeNumber, variations("a", "3", "b", "-0.25", "c", "1e3")},
		"JSON":                   {management.FlagTypeJson, variations("o", `{"b":[1,2],"a":null}`, "l", `[]`, "s", `"s"`, "big", big)},
	} {
		createFlag(t, admin, p.Key, management.FlagCreate{Key: "ok" + strings.ReplaceAll(name, " ", ""), Type: tc.typ, Name: name, Variations: tc.vs})
	}
	for name, tc := range map[string]struct {
		typ  management.FlagType
		vs   *[]management.VariationInput
		want string
	}{
		"a third boolean":         {management.FlagTypeBoolean, variations("true", "true", "false", "false", "maybe", "true"), "/variations invalid_value"},
		"a string without any":    {management.FlagTypeString, nil, "/variations invalid_value"},
		"a string given a number": {management.FlagTypeString, variations("v", "1"), "/variations/0/value invalid_type"},
		"a string given null":     {management.FlagTypeString, variations("v", "null"), "/variations/0/value invalid_value"},
		"a number given a string": {management.FlagTypeNumber, variations("v", `"5"`), "/variations/0/value invalid_type"},
		"JSON over 64 KiB":        {management.FlagTypeJson, variations("v", `"`+strings.Repeat("v", 64*1024-1)+`"`), "/variations/0/value too_long"},
		"duplicate keys":          {management.FlagTypeString, variations("a", `"x"`, "a", `"y"`), "/variations/1/key duplicate"},
		"a key with a space":      {management.FlagTypeString, variations("a b", `"x"`), "/variations/0/key invalid_key"},
	} {
		res := do(admin.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: "refused", Type: tc.typ, Name: name, Variations: tc.vs}))
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_flag"); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: violations = %v, want %s", name, got, tc.want)
		}
	}

	createFlag(t, admin, p.Key, management.FlagCreate{Key: "b", Type: management.FlagTypeBoolean, Name: "B"})
	named := updateFlag(t, admin, p.Key, "b", 1, management.FlagPatch{Variations: &[]management.VariationInput{
		{Key: "true", Value: value("true"), Name: ptr("On")}, {Key: "false", Value: value("false"), Description: ptr("Nothing changes.")},
	}})
	if named.JSON200 == nil || named.JSON200.Variations[0].Name != "On" || named.JSON200.Variations[1].Description != "Nothing changes." {
		t.Errorf("naming a boolean's variations = %d %s", named.StatusCode(), named.Body)
	}
	half := updateFlag(t, admin, p.Key, "b", 2, management.FlagPatch{Variations: variations("true", "true")})
	violationsOf(t, "a boolean with one variation", half.StatusCode(), half.Body, "invalid_flag")
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "n", Type: management.FlagTypeNumber, Name: "N", Variations: variations("one", "1")})
	for _, v := range []string{`"1"`, `[]`} {
		res := updateFlag(t, admin, p.Key, "n", 1, management.FlagPatch{Variations: variations("one", v)})
		violationsOf(t, "a number given "+v, res.StatusCode(), res.Body, "invalid_flag")
	}
}

func TestRemovingAVariationAConfigurationServesIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "copy", Type: management.FlagTypeString, Name: "Copy",
		Variations: variations("a", `"A"`, "b", `"B"`, "c", `"C"`)})
	before := rulesetVersions(t, p)

	for name, vs := range map[string]*[]management.VariationInput{
		"removing a":         variations("b", `"B"`, "c", `"C"`),
		"renaming a":         variations("A", `"A"`, "b", `"B"`, "c", `"C"`),
		"removing a, adding": variations("b", `"B"`, "c", `"C"`, "d", `"D"`),
	} {
		res := updateFlag(t, admin, p.Key, "copy", 1, management.FlagPatch{Variations: vs})
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusConflict, "in_use")
		var refs []string
		if pr.References != nil {
			for _, r := range *pr.References {
				refs = append(refs, r.Kind+" "+r.Key+" "+valueOr(r.Environment))
			}
		}
		if want := []string{"flag copy development", "flag copy staging", "flag copy production"}; !slices.Equal(refs, want) {
			t.Errorf("%s: references = %v, want %v", name, refs, want)
		}
	}
	if got := bumped(t, "refusals", before, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("refusals bumped %v", got)
	}
	res := updateFlag(t, admin, p.Key, "copy", 1, management.FlagPatch{Variations: variations("a", `"AA"`, "b", `"B"`)})
	if res.JSON200 == nil || len(res.JSON200.Variations) != 2 || res.JSON200.Version != 2 || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Errorf("removing c while changing a = %d %s", res.StatusCode(), res.Body)
	}
	if got := bumped(t, "a value", before, rulesetVersions(t, p)); !slices.Equal(got, everyEnvironment) {
		t.Errorf("changing variations bumped %v, want every environment", got)
	}
}

func valueOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestFlagOwnerAndLifetime(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	dev := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(p), dev.ID, access.RoleDeveloper)
	outsider := testkit.NewMember(t, pool)
	gone := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(p), gone.ID, access.RoleDeveloper)
	deactivated := do(admin.DeactivateMemberWithResponse(t.Context(), gone.ID.String()))
	status(t, "deactivate", deactivated.StatusCode(), deactivated.Body, http.StatusOK)
	owned := func(m access.MemberID) *management.FlagCreate {
		return &management.FlagCreate{Key: "owned", Type: management.FlagTypeBoolean, Name: "Owned", Owner: uuidOf(m)}
	}

	at := time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC)
	dated := createFlag(t, admin, p.Key, management.FlagCreate{Key: "dated", Type: management.FlagTypeBoolean, Name: "Dated",
		ExpectedRemovalAt: &at, Owner: uuidOf(dev.ID)})
	if dated.Lifetime != management.LifetimeTemporary || !dated.ExpectedRemovalAt.Equal(at) || dated.Owner != dev.ID.Bytes() {
		t.Errorf("dated = %s, want temporary until %v, owned by the developer", raw(dated), at)
	}
	for name, body := range map[string]*management.FlagCreate{
		"an outsider":      owned(outsider.ID),
		"a deactivated":    owned(gone.ID),
		"an unknown owner": owned(id.FromBytes[access.Member]([16]byte{1})),
	} {
		res := do(admin.CreateFlagWithResponse(t.Context(), p.Key, *body))
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_flag"); !slices.Equal(got, []string{"/owner invalid_owner"}) {
			t.Errorf("%s: violations = %v", name, got)
		}
	}
	permanentWithDate := do(admin.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: "pd", Type: management.FlagTypeBoolean,
		Name: "PD", Lifetime: ptr(management.LifetimePermanent), ExpectedRemovalAt: &at}))
	if got := violationsOf(t, "permanent with a date", permanentWithDate.StatusCode(), permanentWithDate.Body, "invalid_flag"); !slices.Equal(got, []string{"/expected_removal_at invalid_value"}) {
		t.Errorf("violations = %v", got)
	}

	pipeline := testkit.Client(t, srv, testkit.ProjectToken(t, pool, projectIDOf(p), access.RoleDeveloper))
	ownerless := do(pipeline.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: "piped", Type: management.FlagTypeBoolean, Name: "Piped"}))
	if got := violationsOf(t, "a project token naming no owner", ownerless.StatusCode(), ownerless.Body, "invalid_flag"); !slices.Equal(got, []string{"/owner invalid_owner"}) {
		t.Errorf("violations = %v", got)
	}
	piped := createFlag(t, pipeline, p.Key, management.FlagCreate{Key: "piped", Type: management.FlagTypeBoolean, Name: "Piped", Owner: uuidOf(dev.ID)})
	if piped.CreatedBy != nil || piped.Owner != dev.ID.Bytes() {
		t.Errorf("piped = %s, want created by no member, owned by the developer", raw(piped))
	}

	past := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	steps := []struct {
		name  string
		patch management.FlagPatch
		check func(f management.Flag) bool
	}{
		{"a past date", management.FlagPatch{ExpectedRemovalAt: &past}, func(f management.Flag) bool { return f.ExpectedRemovalAt.Equal(past) }},
		{"permanent", management.FlagPatch{Lifetime: ptr(management.LifetimePermanent)}, func(f management.Flag) bool { return f.ExpectedRemovalAt == nil }},
		{"temporary", management.FlagPatch{Lifetime: ptr(management.LifetimeTemporary)}, func(f management.Flag) bool {
			return f.ExpectedRemovalAt != nil && f.ExpectedRemovalAt.After(time.Now().AddDate(0, 0, 89))
		}},
		{"temporary until a date", management.FlagPatch{Lifetime: ptr(management.LifetimeTemporary), ExpectedRemovalAt: &at}, func(f management.Flag) bool {
			return f.ExpectedRemovalAt.Equal(at)
		}},
		{"an admin owner", management.FlagPatch{Owner: ptr(me(t, srv, testkit.InstanceAdminToken(t)).Member.Id)}, func(management.Flag) bool { return true }},
		{"tags emptied", management.FlagPatch{Tags: &[]string{}}, func(f management.Flag) bool { return f.Tags != nil && len(f.Tags) == 0 }},
	}
	version := dated.Version
	for _, s := range steps {
		res := updateFlag(t, admin, p.Key, "dated", version, s.patch)
		if res.JSON200 == nil {
			t.Errorf("%s = %d %s", s.name, res.StatusCode(), res.Body)
			continue
		}
		if !s.check(*res.JSON200) {
			t.Errorf("%s: %s", s.name, raw(*res.JSON200))
		}
		version = res.JSON200.Version
	}
	for name, patch := range map[string]management.FlagPatch{
		"an outsider":        {Owner: uuidOf(outsider.ID)},
		"duplicate tags":     {Tags: &[]string{"a", "a"}},
		"a blank tag":        {Tags: &[]string{" "}},
		"an overlong tag":    {Tags: &[]string{strings.Repeat("t", 65)}},
		"a permanent's date": {Lifetime: ptr(management.LifetimePermanent), ExpectedRemovalAt: &at},
	} {
		res := updateFlag(t, admin, p.Key, "dated", version, patch)
		violationsOf(t, name, res.StatusCode(), res.Body, "invalid_flag")
	}

	// An owner who loses every grant keeps their flags, and naming them
	// again is no change.
	do(admin.DeleteGrantWithResponse(t.Context(), p.Key, grantOf(t, admin, p.Key, dev.ID).Id.String()))
	if res := updateFlag(t, admin, p.Key, "piped", 1, management.FlagPatch{Owner: uuidOf(dev.ID), Name: ptr("Still piped")}); res.JSON200 == nil {
		t.Errorf("renaming a flag whose owner left = %d %s", res.StatusCode(), res.Body)
	}
	if res := updateFlag(t, admin, p.Key, "piped", 2, management.FlagPatch{Owner: uuidOf(dev.ID)}); res.JSON200 == nil || res.JSON200.Version != 2 {
		t.Errorf("naming only the owner who left = %d %s, want 200 and no change", res.StatusCode(), res.Body)
	}
}

func grantOf(t *testing.T, c *management.ClientWithResponses, project string, m access.MemberID) management.Grant {
	t.Helper()
	for _, pm := range projectMembers(t, c, project) {
		if pm.Member == m.Bytes() {
			return pm.Grants[0]
		}
	}
	t.Fatalf("member %s holds no grant", m)
	return management.Grant{}
}

func TestFlagDefinitionWritesNeedTheCurrentVersionInIfMatch(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "f", Type: management.FlagTypeBoolean, Name: "F"})
	before := rulesetVersions(t, p)

	missing := do(admin.UpdateFlagWithResponse(t.Context(), p.Key, "f", nil, management.FlagPatch{Name: ptr("G")}))
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	invalid := do(admin.UpdateFlagWithResponse(t.Context(), p.Key, "f", nil, management.FlagPatch{Name: ptr(" ")}))
	violationsOf(t, "an invalid change without If-Match", invalid.StatusCode(), invalid.Body, "invalid_flag")
	malformed := do(admin.UpdateFlagWithResponse(t.Context(), p.Key, "f", &management.UpdateFlagParams{IfMatch: ptr(`W/"1"`)}, management.FlagPatch{Name: ptr("G")}))
	assertProblem(t, "a weak tag", malformed.StatusCode(), malformed.Body, http.StatusBadRequest, "invalid_request")

	bare := do(admin.UpdateFlagWithResponse(t.Context(), p.Key, "f", &management.UpdateFlagParams{IfMatch: ptr("1")}, management.FlagPatch{Name: ptr("G")}))
	if bare.JSON200 == nil || bare.JSON200.Version != 2 || bare.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("a bare version = %d %s", bare.StatusCode(), bare.Body)
	}
	stale := updateFlag(t, admin, p.Key, "f", 1, management.FlagPatch{Name: ptr("H")})
	if pr := assertProblem(t, "a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch"); pr.CurrentVersion == nil || *pr.CurrentVersion != 2 {
		t.Errorf("current_version = %v, want 2", pr.CurrentVersion)
	}
	same := updateFlag(t, admin, p.Key, "f", 2, management.FlagPatch{Name: ptr("G")})
	if same.JSON200 == nil || same.JSON200.Version != 2 {
		t.Errorf("a change to nothing = %d %s, want the flag as it is", same.StatusCode(), same.Body)
	}
	if got := len(auditOf(t, srv, "flag.updated", getFlag(t, admin, p.Key, "f").Id.String())); got != 1 {
		t.Errorf("flag.updated entries = %d, want one: refusals and no-ops record nothing", got)
	}
	if got := bumped(t, "a rename", before, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("renaming bumped %v, want nothing", got)
	}
	exposed := updateFlag(t, admin, p.Key, "f", 2, management.FlagPatch{AutomaticExposure: ptr(true)})
	if exposed.JSON200 == nil {
		t.Fatalf("automatic exposure = %d %s", exposed.StatusCode(), exposed.Body)
	}
	if got := bumped(t, "automatic exposure", before, rulesetVersions(t, p)); !slices.Equal(got, everyEnvironment) {
		t.Errorf("automatic exposure bumped %v, want every environment", got)
	}
	updated := auditOf(t, srv, "flag.updated", exposed.JSON200.Id.String())
	b, _ := json.Marshal(updated[0].Before)
	a, _ := json.Marshal(updated[0].After)
	if !strings.Contains(string(b), `"automatic_exposure":false`) || !strings.Contains(string(a), `"version":3`) {
		t.Errorf("flag.updated before %s after %s, want the definitions around the change", b, a)
	}
}

func TestListingFlagsPagesByKey(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for _, key := range []string{"beta", "Zeta", "alpha", "a.b", "gamma"} {
		createFlag(t, admin, p.Key, management.FlagCreate{Key: key, Type: management.FlagTypeBoolean, Name: key})
	}
	var keys []string
	params := &management.ListFlagsParams{Limit: ptr(2)}
	for range 4 {
		res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, params))
		if res.JSON200 == nil {
			t.Fatalf("GET flags = %d %s", res.StatusCode(), res.Body)
		}
		for _, f := range res.JSON200.Items {
			keys = append(keys, f.Key)
		}
		if res.JSON200.NextCursor == nil {
			break
		}
		params.Cursor = res.JSON200.NextCursor
	}
	if want := []string{"Zeta", "a.b", "alpha", "beta", "gamma"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v by byte order", keys, want)
	}
	for name, params := range map[string]*management.ListFlagsParams{
		"a forged cursor": {Cursor: ptr("not base64!")},
		"limit 0":         {Limit: ptr(0)},
		"limit 201":       {Limit: ptr(201)},
	} {
		res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, params))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
}

func TestFlagEndpointsOfAnUnknownProjectAreNotFound(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	calls := map[string]func(project, flag string) (int, []byte){
		"list": func(project, _ string) (int, []byte) {
			res := do(admin.ListFlagsWithResponse(t.Context(), project, nil))
			return res.StatusCode(), res.Body
		},
		"create": func(project, flag string) (int, []byte) {
			res := do(admin.CreateFlagWithResponse(t.Context(), project, management.FlagCreate{Key: flag, Type: management.FlagTypeBoolean, Name: "X"}))
			return res.StatusCode(), res.Body
		},
		"get": func(project, flag string) (int, []byte) {
			res := do(admin.GetFlagWithResponse(t.Context(), project, flag))
			return res.StatusCode(), res.Body
		},
		"update": func(project, flag string) (int, []byte) {
			res := updateFlag(t, admin, project, flag, 1, management.FlagPatch{Name: ptr("Y")})
			return res.StatusCode(), res.Body
		},
	}
	for name, call := range maps.All(calls) {
		status, body := call("no-such-project", "x")
		assertProblem(t, name+" in an unknown project", status, body, http.StatusNotFound, "not_found")
		if name == "list" || name == "create" {
			continue
		}
		status, body = call(p.Key, "x")
		assertProblem(t, name+" of an unknown flag", status, body, http.StatusNotFound, "not_found")
	}
}

func archive(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) *management.ArchiveFlagResponse {
	t.Helper()
	return do(c.ArchiveFlagWithResponse(t.Context(), project, key, &management.ArchiveFlagParams{IfMatch: ifMatch(version)}))
}

func restore(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) *management.RestoreFlagResponse {
	t.Helper()
	return do(c.RestoreFlagWithResponse(t.Context(), project, key, &management.RestoreFlagParams{IfMatch: ifMatch(version)}))
}

func deleteFlag(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) *management.DeleteFlagResponse {
	t.Helper()
	return do(c.DeleteFlagWithResponse(t.Context(), project, key, &management.DeleteFlagParams{IfMatch: ifMatch(version)}))
}

func listedKeys(t *testing.T, c *management.ClientWithResponses, project string, archived bool) []string {
	t.Helper()
	res := do(c.ListFlagsWithResponse(t.Context(), project, &management.ListFlagsParams{Archived: &archived}))
	if res.JSON200 == nil {
		t.Fatalf("GET flags = %d %s", res.StatusCode(), res.Body)
	}
	var keys []string
	for _, f := range res.JSON200.Items {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestArchivingRestoringAndDeletingFlags(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "base", Type: management.FlagTypeBoolean, Name: "Base"})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "retired", Type: management.FlagTypeBoolean, Name: "Retired"})

	active := deleteFlag(t, admin, p.Key, "retired", 1)
	assertProblem(t, "deleting an active flag", active.StatusCode(), active.Body, http.StatusConflict, "not_archived")
	archived := archive(t, admin, p.Key, "retired", 1)
	if archived.JSON200 == nil || archived.JSON200.ArchivedAt == nil || archived.JSON200.Version != 2 ||
		archived.HTTPResponse.Header.Get("ETag") != `"2"` || len(archived.JSON200.Environments) != 3 {
		t.Fatalf("archive = %d %s", archived.StatusCode(), archived.Body)
	}
	if got := listedKeys(t, admin, p.Key, false); !slices.Equal(got, []string{"base"}) {
		t.Errorf("listed = %v, want the active flag alone", got)
	}
	if got := listedKeys(t, admin, p.Key, true); !slices.Equal(got, []string{"retired"}) {
		t.Errorf("listed archived = %v, want the archived flag alone", got)
	}
	if again := archive(t, admin, p.Key, "retired", 2); again.JSON200 == nil || again.JSON200.Version != 2 {
		t.Errorf("archiving again = %d %s, want it as it is", again.StatusCode(), again.Body)
	}
	restored := restore(t, admin, p.Key, "retired", 2)
	if restored.JSON200 == nil || restored.JSON200.ArchivedAt != nil || restored.JSON200.Version != 3 {
		t.Fatalf("restore = %d %s", restored.StatusCode(), restored.Body)
	}
	archive(t, admin, p.Key, "retired", 3)
	if res := deleteFlag(t, admin, p.Key, "retired", 4); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.StatusCode(), res.Body)
	}
	gone := map[string]int{
		"get":     do(admin.GetFlagWithResponse(t.Context(), p.Key, "retired")).StatusCode(),
		"restore": restore(t, admin, p.Key, "retired", 4).StatusCode(),
		"archive": archive(t, admin, p.Key, "retired", 4).StatusCode(),
		"delete":  deleteFlag(t, admin, p.Key, "retired", 4).StatusCode(),
	}
	for name, got := range gone {
		if got != http.StatusNotFound {
			t.Errorf("%s of a deleted flag = %d, want 404", name, got)
		}
	}
	again := createFlag(t, admin, p.Key, management.FlagCreate{Key: "retired", Type: management.FlagTypeString, Name: "Again",
		Variations: variations("v", `"v"`)})
	if again.Version != 1 || again.Type != management.FlagTypeString {
		t.Errorf("the key reused = %s", raw(again))
	}
}

func TestArchivingRestoringAndDeletingNeedTheCurrentVersionInIfMatch(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "f", Type: management.FlagTypeBoolean, Name: "F"})

	missing := map[string]*http.Response{
		"archive": do(admin.ArchiveFlagWithResponse(t.Context(), p.Key, "f", nil)).HTTPResponse,
		"restore": do(admin.RestoreFlagWithResponse(t.Context(), p.Key, "f", nil)).HTTPResponse,
		"delete":  do(admin.DeleteFlagWithResponse(t.Context(), p.Key, "f", nil)).HTTPResponse,
	}
	for name, res := range missing {
		if res.StatusCode != http.StatusPreconditionRequired {
			t.Errorf("%s without If-Match = %d, want 428", name, res.StatusCode)
		}
	}
	stale := archive(t, admin, p.Key, "f", 7)
	assertProblem(t, "archive at a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	archive(t, admin, p.Key, "f", 1)
	staleRestore := restore(t, admin, p.Key, "f", 1)
	assertProblem(t, "restore at a stale version", staleRestore.StatusCode(), staleRestore.Body, http.StatusPreconditionFailed, "version_mismatch")
	staleDelete := deleteFlag(t, admin, p.Key, "f", 1)
	assertProblem(t, "delete at a stale version", staleDelete.StatusCode(), staleDelete.Body, http.StatusPreconditionFailed, "version_mismatch")
	if f := getFlag(t, admin, p.Key, "f"); f.ArchivedAt == nil || f.Version != 2 {
		t.Errorf("after refusals: %s, want it archived at version 2", raw(f))
	}
}

func TestEveryAcceptedFlagDefinitionWriteBumpsTheRulesetVersionOnce(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	version := int64(1)
	steps := []struct {
		name   string
		write  func() (int, []byte, *management.Flag)
		status int
		bumps  bool
	}{
		{"create", func() (int, []byte, *management.Flag) {
			res := do(admin.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: "copy", Type: management.FlagTypeString,
				Name: "Copy", Variations: variations("a", `"A"`, "b", `"B"`)}))
			return res.StatusCode(), res.Body, res.JSON201
		}, http.StatusCreated, true},
	}
	patch := func(name string, body management.FlagPatch, want int, bumps bool) {
		steps = append(steps, struct {
			name   string
			write  func() (int, []byte, *management.Flag)
			status int
			bumps  bool
		}{name, func() (int, []byte, *management.Flag) {
			res := updateFlag(t, admin, p.Key, "copy", version, body)
			return res.StatusCode(), res.Body, res.JSON200
		}, want, bumps})
	}
	lifecycle := func(name string, write func() (int, []byte, *management.Flag), want int, bumps bool) {
		steps = append(steps, struct {
			name   string
			write  func() (int, []byte, *management.Flag)
			status int
			bumps  bool
		}{name, write, want, bumps})
	}
	patch("a name, description and tags", management.FlagPatch{Name: ptr("Copy 2"), Description: ptr("d"), Tags: &[]string{"t"}}, http.StatusOK, false)
	patch("a lifetime", management.FlagPatch{Lifetime: ptr(management.LifetimePermanent)}, http.StatusOK, false)
	patch("nothing", management.FlagPatch{Name: ptr("Copy 2")}, http.StatusOK, false)
	patch("a variation's name", management.FlagPatch{Variations: &[]management.VariationInput{
		{Key: "a", Value: value(`"A"`), Name: ptr("Ay")}, {Key: "b", Value: value(`"B"`)}}}, http.StatusOK, false)
	patch("a variation's value", management.FlagPatch{Variations: variations("a", `"AA"`, "b", `"B"`)}, http.StatusOK, true)
	patch("automatic exposure", management.FlagPatch{AutomaticExposure: ptr(true)}, http.StatusOK, true)
	patch("a key", management.FlagPatch{Key: ptr("other")}, http.StatusUnprocessableEntity, false)
	archiveStep := func() (int, []byte, *management.Flag) {
		res := archive(t, admin, p.Key, "copy", version)
		return res.StatusCode(), res.Body, res.JSON200
	}
	restoreStep := func() (int, []byte, *management.Flag) {
		res := restore(t, admin, p.Key, "copy", version)
		return res.StatusCode(), res.Body, res.JSON200
	}
	lifecycle("archive", archiveStep, http.StatusOK, true)
	lifecycle("archive again", archiveStep, http.StatusOK, false)
	lifecycle("restore", restoreStep, http.StatusOK, true)
	lifecycle("restore again", restoreStep, http.StatusOK, false)
	lifecycle("archive once more", archiveStep, http.StatusOK, true)
	lifecycle("delete", func() (int, []byte, *management.Flag) {
		res := deleteFlag(t, admin, p.Key, "copy", version)
		return res.StatusCode(), res.Body, nil
	}, http.StatusNoContent, false)

	for _, s := range steps {
		before := rulesetVersions(t, p)
		got, body, f := s.write()
		if got != s.status {
			t.Fatalf("%s = %d %s, want %d", s.name, got, body, s.status)
		}
		if f != nil {
			version = f.Version
		}
		want := []string(nil)
		if s.bumps {
			want = everyEnvironment
		}
		if b := bumped(t, s.name, before, rulesetVersions(t, p)); !slices.Equal(b, want) {
			t.Errorf("%s bumped %v, want %v", s.name, b, want)
		}
	}
}

func TestEveryFlagDefinitionWriteRecordsOneAuditEntry(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := createFlag(t, admin, p.Key, management.FlagCreate{Key: "banner", Type: management.FlagTypeBoolean, Name: "Banner"})
	updateFlag(t, admin, p.Key, "banner", 1, management.FlagPatch{Name: ptr("Top banner")})
	updateFlag(t, admin, p.Key, "banner", 2, management.FlagPatch{Name: ptr("Top banner")})
	updateFlag(t, admin, p.Key, "banner", 2, management.FlagPatch{Type: ptr("string")})
	updateFlag(t, admin, p.Key, "banner", 9, management.FlagPatch{Name: ptr("Stale")})
	deleteFlag(t, admin, p.Key, "banner", 2)
	archive(t, admin, p.Key, "banner", 2)
	restore(t, admin, p.Key, "banner", 3)
	archive(t, admin, p.Key, "banner", 4)
	archive(t, admin, p.Key, "banner", 5)
	if res := deleteFlag(t, admin, p.Key, "banner", 5); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.StatusCode(), res.Body)
	}

	res := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{EntityId: ptr(f.Id.String())}))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	var got []string
	entries := res.JSON200.Items
	slices.Reverse(entries)
	for _, e := range entries {
		got = append(got, e.Action+" "+e.Entity.Key)
		if e.EnvironmentId != nil {
			t.Errorf("%s addresses an environment, want none", e.Action)
		}
	}
	want := []string{"flag.created banner", "flag.updated banner", "flag.archived banner", "flag.restored banner",
		"flag.archived banner", "flag.deleted banner"}
	if !slices.Equal(got, want) {
		t.Fatalf("actions = %v, want %v: refusals and no-ops record nothing", got, want)
	}
	deleted := entries[len(entries)-1]
	before, _ := json.Marshal(deleted.Before)
	if deleted.After != nil || !strings.Contains(string(before), `"environments":[{`) || !strings.Contains(string(before), `"version":5`) {
		t.Errorf("flag.deleted before %s after %v, want the definition and every configuration before, nothing after", before, deleted.After)
	}
}
