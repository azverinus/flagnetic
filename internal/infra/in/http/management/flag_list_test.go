package management_test

import (
	"net/http"
	"slices"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// listing returns the keys of every flag params lists, walking the pages
// it is cut into.
func listing(t *testing.T, c *management.ClientWithResponses, project string, params management.ListFlagsParams) []string {
	t.Helper()
	keys := []string{}
	for range 50 {
		res := do(c.ListFlagsWithResponse(t.Context(), project, &params))
		if res.JSON200 == nil {
			t.Fatalf("GET flags %+v = %d %s", params, res.StatusCode(), res.Body)
		}
		for _, f := range res.JSON200.Items {
			keys = append(keys, f.Key)
		}
		if res.JSON200.NextCursor == nil {
			return keys
		}
		params.Cursor = res.JSON200.NextCursor
	}
	t.Fatalf("GET flags %+v never reached its last page", params)
	return nil
}

// listedItems returns the one page of flags params lists.
func listedItems(t *testing.T, c *management.ClientWithResponses, project string, params management.ListFlagsParams) []management.FlagListItem {
	t.Helper()
	res := do(c.ListFlagsWithResponse(t.Context(), project, &params))
	if res.JSON200 == nil {
		t.Fatalf("GET flags %+v = %d %s", params, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func TestListingFlagsFiltersAndPaginates(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	pool := testkit.Pool(t)
	p := createProject(t, admin, projectKey())
	dev := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(p), dev.ID, access.RoleDeveloper)
	payments := ptr([]string{"payments"})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "Zeta", Type: management.FlagTypeBoolean, Name: "Zeta"})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout_title", Type: management.FlagTypeString, Name: "Checkout title",
		Tags: payments, Variations: variations("short", `"Pay"`, "long", `"Pay now"`)})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout_v2", Type: management.FlagTypeBoolean, Name: "New pricing algorithm",
		Tags: ptr([]string{"payments", "risky"})})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "search_ranking", Type: management.FlagTypeString, Name: "Search ranking",
		Variations: variations("v1", `"one"`, "v2", `"two"`)})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "session_limit", Type: management.FlagTypeNumber, Name: "Session limit",
		Owner: uuidOf(dev.ID), Variations: variations("short", `30`, "long", `120`)})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "old_checkout", Type: management.FlagTypeBoolean, Name: "Old checkout", Tags: payments})
	archive(t, admin, p.Key, "old_checkout", 1)
	other := createProject(t, admin, projectKey())
	createFlag(t, admin, other.Key, management.FlagCreate{Key: "checkout_elsewhere", Type: management.FlagTypeBoolean, Name: "Elsewhere"})

	boolean := management.FlagTypeBoolean
	unknown := openapi_types.UUID{1}
	cases := map[string]struct {
		params management.ListFlagsParams
		want   []string
	}{
		"nothing":                   {management.ListFlagsParams{}, []string{"Zeta", "checkout_title", "checkout_v2", "search_ranking", "session_limit"}},
		"text in a key, any case":   {management.ListFlagsParams{Q: ptr("CHECKOUT")}, []string{"checkout_title", "checkout_v2"}},
		"text in a name":            {management.ListFlagsParams{Q: ptr("algorithm")}, []string{"checkout_v2"}},
		"an underscore as itself":   {management.ListFlagsParams{Q: ptr("_")}, []string{"checkout_title", "checkout_v2", "search_ranking", "session_limit"}},
		"a percent sign as itself":  {management.ListFlagsParams{Q: ptr("%")}, []string{}},
		"a tag":                     {management.ListFlagsParams{Tag: ptr("payments")}, []string{"checkout_title", "checkout_v2"}},
		"a type":                    {management.ListFlagsParams{Type: &boolean}, []string{"Zeta", "checkout_v2"}},
		"a tag and a type":          {management.ListFlagsParams{Tag: ptr("payments"), Type: &boolean}, []string{"checkout_v2"}},
		"an owner":                  {management.ListFlagsParams{Owner: uuidOf(dev.ID)}, []string{"session_limit"}},
		"an owner of nothing":       {management.ListFlagsParams{Owner: &unknown}, []string{}},
		"archived":                  {management.ListFlagsParams{Archived: ptr(true)}, []string{"old_checkout"}},
		"archived, a tag and text":  {management.ListFlagsParams{Archived: ptr(true), Tag: ptr("payments"), Q: ptr("old")}, []string{"old_checkout"}},
		"archived and another type": {management.ListFlagsParams{Archived: ptr(true), Type: ptr(management.FlagTypeString)}, []string{}},
	}
	for name, tc := range cases {
		if got := listing(t, admin, p.Key, tc.params); !slices.Equal(got, tc.want) {
			t.Errorf("%s: flags = %v, want %v", name, got, tc.want)
		}
		tc.params.Limit = ptr(2)
		if got := listing(t, admin, p.Key, tc.params); !slices.Equal(got, tc.want) {
			t.Errorf("%s, by pages of two: flags = %v, want %v", name, got, tc.want)
		}
	}

	for name, params := range map[string]management.ListFlagsParams{
		"limit 0":         {Limit: ptr(0)},
		"limit 201":       {Limit: ptr(201)},
		"a forged cursor": {Cursor: ptr("not a cursor!")},
		"an unknown type": {Type: ptr(management.FlagType("yaml"))},
		"an unknown sort": {Sort: ptr(management.ListFlagsParamsSort("owner"))},
	} {
		res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &params))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
}

func TestListedFlagsAreSortedByKeyNameOrLastUpdate(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for _, f := range [][2]string{{"alpha", "Search ranking"}, {"beta", "Banner"}, {"gamma", "Checkout"}, {"delta", "Banner"}} {
		createFlag(t, admin, p.Key, management.FlagCreate{Key: f[0], Type: management.FlagTypeBoolean, Name: f[1]})
	}
	if r := updateFlag(t, admin, p.Key, "beta", 1, management.FlagPatch{Description: ptr("Shown on the home page.")}); r.JSON200 == nil {
		t.Fatalf("PATCH beta = %d %s", r.StatusCode(), r.Body)
	}
	if r := updateFlag(t, admin, p.Key, "gamma", 1, management.FlagPatch{Tags: ptr([]string{"payments"})}); r.JSON200 == nil {
		t.Fatalf("PATCH gamma = %d %s", r.StatusCode(), r.Body)
	}
	cases := map[management.ListFlagsParamsSort][]string{
		"key":     {"alpha", "beta", "delta", "gamma"},
		"name":    {"beta", "delta", "gamma", "alpha"},
		"updated": {"gamma", "beta", "delta", "alpha"},
	}
	for sort, want := range cases {
		if got := listing(t, admin, p.Key, management.ListFlagsParams{Sort: &sort}); !slices.Equal(got, want) {
			t.Errorf("sort %s: flags = %v, want %v", sort, got, want)
		}
		if got := listing(t, admin, p.Key, management.ListFlagsParams{Sort: &sort, Limit: ptr(1)}); !slices.Equal(got, want) {
			t.Errorf("sort %s, a flag a page: flags = %v, want %v", sort, got, want)
		}
	}

	first := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &management.ListFlagsParams{Limit: ptr(1)}))
	if first.JSON200 == nil || first.JSON200.NextCursor == nil {
		t.Fatalf("GET flags = %d %s", first.StatusCode(), first.Body)
	}
	byName := management.ListFlagsParamsSort("name")
	res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &management.ListFlagsParams{Sort: &byName, Cursor: first.JSON200.NextCursor}))
	assertProblem(t, "a cursor of another sort", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
}

// stateOf returns "environment on/off rules rollout" for each environment
// of a listed flag, a dash for what it lacks.
func stateOf(f management.FlagListItem) []string {
	mark := func(b bool, s string) string {
		if b {
			return s
		}
		return "-"
	}
	out := make([]string, len(f.States))
	for i, s := range f.States {
		out[i] = s.Environment + " " + mark(s.On, "on") + " " + mark(s.HasRules, "rules") + " " + mark(s.HasRollout, "rollout")
	}
	return out
}

func TestListedFlagsCarryTheirStateInEveryEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "matching")
	stringFlag(t, admin, p.Key, "untouched")
	mustPut(t, admin, p.Key, "matching", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 1000)}}})
	mustPut(t, admin, p.Key, "matching", "development", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(attr("country", "eq", `"NZ"`)), Serve: serves("c")}}})

	items := listedItems(t, admin, p.Key, management.ListFlagsParams{})
	if len(items) != 2 {
		t.Fatalf("flags = %d, want 2", len(items))
	}
	want := map[string][]string{
		"matching":  {"development on rules -", "staging on rules rollout", "production - - -"},
		"untouched": {"development - - -", "staging - - -", "production - - -"},
	}
	for _, f := range items {
		if got := stateOf(f); !slices.Equal(got, want[f.Key]) {
			t.Errorf("%s: states = %q, want %q", f.Key, got, want[f.Key])
		}
	}
	matching := getFlag(t, admin, p.Key, "matching")
	if items[0].Key != "matching" || items[0].Name != matching.Name || items[0].Type != matching.Type ||
		items[0].Version != matching.Version || !items[0].UpdatedAt.Equal(matching.UpdatedAt) {
		t.Errorf("listed definition = %+v, want that of %+v", items[0], matching)
	}
}

func TestListedFlagsAreFilteredByStateWithinOneEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for _, key := range []string{"on_plain", "off_with_rules", "rolled_out", "untouched"} {
		stringFlag(t, admin, p.Key, key)
	}
	mustPut(t, admin, p.Key, "on_plain", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	mustPut(t, admin, p.Key, "off_with_rules", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(attr("country", "eq", `"NZ"`)), Serve: serves("b")}}})
	mustPut(t, admin, p.Key, "rolled_out", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 5000)}}})

	type filter = management.ListFlagsParamsState
	cases := []struct {
		env   string
		state filter
		want  []string
	}{
		{"staging", "on", []string{"on_plain", "rolled_out"}},
		{"staging", "off", []string{"off_with_rules", "untouched"}},
		{"staging", "rules", []string{"off_with_rules", "rolled_out"}},
		{"staging", "rollout", []string{"rolled_out"}},
		{"production", "on", []string{}},
		{"production", "off", []string{"off_with_rules", "on_plain", "rolled_out", "untouched"}},
	}
	for _, tc := range cases {
		params := management.ListFlagsParams{Environment: ptr(tc.env), State: ptr(tc.state)}
		if got := listing(t, admin, p.Key, params); !slices.Equal(got, tc.want) {
			t.Errorf("%s in %s: flags = %v, want %v", tc.state, tc.env, got, tc.want)
		}
		params.Limit = ptr(1)
		if got := listing(t, admin, p.Key, params); !slices.Equal(got, tc.want) {
			t.Errorf("%s in %s, a flag a page: flags = %v, want %v", tc.state, tc.env, got, tc.want)
		}
	}
	if got := listing(t, admin, p.Key, management.ListFlagsParams{Environment: ptr("staging")}); len(got) != 4 {
		t.Errorf("an environment without a state lists %v, want every flag", got)
	}
	if got := listing(t, admin, p.Key, management.ListFlagsParams{Environment: ptr("staging"), State: ptr(filter("on")), Q: ptr("roll")}); !slices.Equal(got, []string{"rolled_out"}) {
		t.Errorf("a state and text list %v, want [rolled_out]", got)
	}

	for name, params := range map[string]management.ListFlagsParams{
		"a state without an environment": {State: ptr(filter("on"))},
		"an unknown state":               {Environment: ptr("staging"), State: ptr(filter("half_on"))},
	} {
		res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &params))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
	res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &management.ListFlagsParams{Environment: ptr("qa"), State: ptr(filter("on"))}))
	assertProblem(t, "an unknown environment", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
}

func TestTheStateFollowsConfigurationWritesAndKills(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "matching")
	productionState := func() string {
		t.Helper()
		items := listedItems(t, admin, p.Key, management.ListFlagsParams{})
		return stateOf(items[0])[2]
	}
	on := management.ListFlagsParams{Environment: ptr("production"), State: ptr(management.ListFlagsParamsState("on"))}

	mustPut(t, admin, p.Key, "matching", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 1000)}}})
	if got := productionState(); got != "production on rules rollout" {
		t.Errorf("after a write = %q", got)
	}
	if r := kill(t, admin, p.Key, "matching", "production", nil); r.JSON200 == nil {
		t.Fatalf("kill = %d %s", r.StatusCode(), r.Body)
	}
	if got := productionState(); got != "production - rules rollout" {
		t.Errorf("after the kill switch = %q", got)
	}
	if got := listing(t, admin, p.Key, on); len(got) != 0 {
		t.Errorf("on in production after the kill switch = %v", got)
	}
	mustPut(t, admin, p.Key, "matching", "production", 3, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a"})
	if got := productionState(); got != "production - - -" {
		t.Errorf("after removing the rules = %q", got)
	}
}

func TestFlagsDifferingBetweenTwoEnvironmentsAreListed(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for _, key := range []string{"different_rules", "noted_differently", "same", "switched_on", "zz_different_overrides"} {
		stringFlag(t, admin, p.Key, key)
	}
	both := func(key string, staging, production management.FlagConfigurationPut) {
		mustPut(t, admin, p.Key, key, "staging", 1, staging)
		mustPut(t, admin, p.Key, key, "production", 1, production)
	}
	off := management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "b"}
	both("same", off, off)
	withRule := off
	withRule.Rules = &[]management.RuleInput{{Conditions: conditions(attr("country", "eq", `"NZ"`)), Serve: serves("c")}}
	both("different_rules", withRule, off)
	overridden := func(note string) management.FlagConfigurationPut {
		c := off
		c.Overrides = &[]management.OverrideInput{{UnitType: "user", UnitId: "u-1", Variation: "c", Note: ptr(note)}}
		return c
	}
	both("noted_differently", overridden("asked for it"), overridden("support case"))
	both("zz_different_overrides", overridden("asked for it"), off)
	switchedOn := off
	switchedOn.On = true
	both("switched_on", off, switchedOn)
	stringFlag(t, admin, p.Key, "archived_one")
	mustPut(t, admin, p.Key, "archived_one", "staging", 1, switchedOn)
	archive(t, admin, p.Key, "archived_one", 1)

	differs := management.ListFlagsParams{DiffersBetween: &[]string{"staging", "production"}}
	want := []string{"different_rules", "switched_on", "zz_different_overrides"}
	if got := listing(t, admin, p.Key, differs); !slices.Equal(got, want) {
		t.Errorf("flags differing = %v, want %v", got, want)
	}
	differs.Limit = ptr(1)
	if got := listing(t, admin, p.Key, differs); !slices.Equal(got, want) {
		t.Errorf("flags differing, a flag a page = %v, want %v", got, want)
	}
	reversed := management.ListFlagsParams{DiffersBetween: &[]string{"production", "staging"}, Q: ptr("rules")}
	if got := listing(t, admin, p.Key, reversed); !slices.Equal(got, []string{"different_rules"}) {
		t.Errorf("flags differing the other way round, with text = %v, want [different_rules]", got)
	}
	archived := management.ListFlagsParams{DiffersBetween: &[]string{"staging", "production"}, Archived: ptr(true)}
	if got := listing(t, admin, p.Key, archived); !slices.Equal(got, []string{"archived_one"}) {
		t.Errorf("archived flags differing = %v, want [archived_one]", got)
	}
	if got := listing(t, admin, p.Key, management.ListFlagsParams{DiffersBetween: &[]string{"development", "production"}}); len(got) != 5 {
		t.Errorf("flags differing between development and production = %v, want the five production changed", got)
	}

	for name, keys := range map[string][]string{
		"one environment":    {"staging"},
		"the same one twice": {"staging", "staging"},
		"three environments": {"development", "staging", "production"},
	} {
		res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &management.ListFlagsParams{DiffersBetween: &keys}))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
	res := do(admin.ListFlagsWithResponse(t.Context(), p.Key, &management.ListFlagsParams{DiffersBetween: &[]string{"staging", "qa"}}))
	assertProblem(t, "an unknown environment", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
}

// A page of 25 flags costs the statements a page of 5 does, whether it is
// filtered by state or by what differs between two environments.
func TestAPageOfFlagsCostsTheSameWhateverItsSize(t *testing.T) {
	pool, queries := testkit.TracedPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for i := range 25 {
		key := "flag_" + string(rune('a'+i))
		stringFlag(t, admin, p.Key, key)
		mustPut(t, admin, p.Key, key, "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	}
	pages := map[string]management.ListFlagsParams{
		"plain":     {},
		"by state":  {Environment: ptr("production"), State: ptr(management.ListFlagsParamsState("on"))},
		"differing": {DiffersBetween: &[]string{"staging", "production"}},
	}
	for name, params := range pages {
		statements := func(limit int) int {
			params.Limit = ptr(limit)
			listedItems(t, admin, p.Key, params)
			queries.Reset()
			if items := listedItems(t, admin, p.Key, params); len(items) != limit {
				t.Fatalf("%s: a page of %d holds %d", name, limit, len(items))
			}
			return queries.Containing("")
		}
		if small, large := statements(5), statements(25); small != large {
			t.Errorf("%s: a page of 5 costs %d statements, a page of 25 %d", name, small, large)
		}
	}
}
