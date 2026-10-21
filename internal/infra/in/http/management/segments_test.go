package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// attr returns an attribute condition whose value is the JSON v, absent
// when v is "".
func attr(attribute, op, v string) management.Condition {
	c := management.Condition{Kind: "attribute", Attribute: &attribute, Op: &op}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &c.Value); err != nil {
			panic(err)
		}
	}
	return c
}

// conditions returns cs as a body lists them, an empty list when none.
func conditions(cs ...management.Condition) *[]management.Condition {
	if cs == nil {
		cs = []management.Condition{}
	}
	return &cs
}

// createSegment creates a segment as c, failing unless it is created at
// version 1 with that version as its ETag.
func createSegment(t *testing.T, c *management.ClientWithResponses, project string, body management.SegmentCreate) management.Segment {
	t.Helper()
	res := do(c.CreateSegmentWithResponse(t.Context(), project, body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST segments %s = %d, ETag %q: %s", body.Key, res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

func updateSegment(t *testing.T, c *management.ClientWithResponses, project, key string, version int64, body management.SegmentPatch) *management.UpdateSegmentResponse {
	t.Helper()
	return do(c.UpdateSegmentWithResponse(t.Context(), project, key, &management.UpdateSegmentParams{IfMatch: ifMatch(version)}, body))
}

func deleteSegment(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) *management.DeleteSegmentResponse {
	t.Helper()
	return do(c.DeleteSegmentWithResponse(t.Context(), project, key, &management.DeleteSegmentParams{IfMatch: ifMatch(version)}))
}

func TestSegmentLifecycle(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	created := createSegment(t, admin, p.Key, management.SegmentCreate{Key: "android_old", Name: "Old Android",
		Conditions: conditions(attr("os", "eq", `"android"`), attr("app_version", "lt", `"1.41.0"`))})
	conds, _ := json.Marshal(created.Conditions)
	if string(conds) != `[{"attribute":"os","kind":"attribute","op":"eq","value":"android"},{"attribute":"app_version","kind":"attribute","op":"lt","value":"1.41.0"}]` ||
		created.Private || created.Version != 1 || created.Description != "" {
		t.Errorf("created = %s", raw(created))
	}
	got := do(admin.GetSegmentWithResponse(t.Context(), p.Key, "android_old"))
	if got.JSON200 == nil || raw(*got.JSON200) != raw(created) || got.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Errorf("GET = %d %s, ETag %q, want what creating answered", got.StatusCode(), got.Body, got.HTTPResponse.Header.Get("ETag"))
	}
	everyone := createSegment(t, admin, p.Key, management.SegmentCreate{Key: "everyone", Name: "Everyone"})
	if everyone.Conditions == nil || len(everyone.Conditions) != 0 {
		t.Errorf("a segment without conditions = %s, want an empty list", raw(everyone))
	}
	listed := do(admin.ListSegmentsWithResponse(t.Context(), p.Key))
	var keys []string
	for _, s := range listed.JSON200.Items {
		keys = append(keys, s.Key)
	}
	if !slices.Equal(keys, []string{"android_old", "everyone"}) {
		t.Errorf("listed = %v", keys)
	}

	changed := updateSegment(t, admin, p.Key, "android_old", 1, management.SegmentPatch{Name: ptr("Android before 1.41"), Private: ptr(true),
		Conditions: conditions(attr("os", "in", `["android","harmony"]`))})
	if changed.JSON200 == nil || !changed.JSON200.Private || changed.JSON200.Version != 2 || len(changed.JSON200.Conditions) != 1 ||
		changed.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("PATCH = %d %s", changed.StatusCode(), changed.Body)
	}
	emptied := updateSegment(t, admin, p.Key, "android_old", 2, management.SegmentPatch{Conditions: conditions()})
	if emptied.JSON200 == nil || len(emptied.JSON200.Conditions) != 0 || emptied.JSON200.Version != 3 {
		t.Fatalf("PATCH no conditions = %d %s", emptied.StatusCode(), emptied.Body)
	}
	usages := do(admin.ListSegmentUsagesWithResponse(t.Context(), p.Key, "android_old"))
	if usages.JSON200 == nil || usages.JSON200.Items == nil || len(usages.JSON200.Items) != 0 {
		t.Errorf("usages = %d %s, want none", usages.StatusCode(), usages.Body)
	}
	if res := deleteSegment(t, admin, p.Key, "android_old", 3); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.StatusCode(), res.Body)
	}
	for name, status := range map[string]int{
		"get":    do(admin.GetSegmentWithResponse(t.Context(), p.Key, "android_old")).StatusCode(),
		"patch":  updateSegment(t, admin, p.Key, "android_old", 3, management.SegmentPatch{Name: ptr("x")}).StatusCode(),
		"delete": deleteSegment(t, admin, p.Key, "android_old", 3).StatusCode(),
		"usages": do(admin.ListSegmentUsagesWithResponse(t.Context(), p.Key, "android_old")).StatusCode(),
	} {
		if status != http.StatusNotFound {
			t.Errorf("%s of a deleted segment = %d, want 404", name, status)
		}
	}
}

func TestSegmentKeysAreValidatedUniqueAndImmutable(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for _, key := range []string{"", "Beta", "1st", "with space", strings.Repeat("a", 65)} {
		res := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: key, Name: "S"}))
		if got := violationsOf(t, "key "+key, res.StatusCode(), res.Body, "invalid_segment"); !slices.Equal(got, []string{"/key invalid_key"}) {
			t.Errorf("key %q: violations = %v", key, got)
		}
	}
	blank := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "s", Name: " "}))
	violationsOf(t, "a blank name", blank.StatusCode(), blank.Body, "invalid_segment")
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Beta testers"})
	dup := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Again"}))
	assertProblem(t, "a key in use", dup.StatusCode(), dup.Body, http.StatusConflict, "already_exists")
	createSegment(t, admin, createProject(t, admin, projectKey()).Key, management.SegmentCreate{Key: "beta_testers", Name: "Elsewhere"})
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "user", Name: "Named like a unit type"})
	rekey := updateSegment(t, admin, p.Key, "beta_testers", 1, management.SegmentPatch{Key: ptr("staff")})
	if got := violationsOf(t, "a new key", rekey.StatusCode(), rekey.Body, "invalid_segment"); !slices.Equal(got, []string{"/key immutable"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestSegmentsAcceptOnlyAttributeConditions(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Beta testers"})
	for name, c := range map[string]management.Condition{
		"in_segment":     {Kind: "in_segment", Segment: ptr("beta_testers")},
		"not_in_segment": {Kind: "not_in_segment", Segment: ptr("beta_testers")},
		"prerequisite":   {Kind: "prerequisite", Flag: ptr("f"), Variation: ptr("true")},
		"expression":     {Kind: "expression"},
	} {
		cs := conditions(attr("os", "eq", `"ios"`), c)
		created := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "nested", Name: "Nested", Conditions: cs}))
		if got := violationsOf(t, "create with "+name, created.StatusCode(), created.Body, "invalid_segment"); !slices.Equal(got, []string{"/conditions/1/kind invalid_value"}) {
			t.Errorf("%s: violations = %v", name, got)
		}
		patched := updateSegment(t, admin, p.Key, "beta_testers", 1, management.SegmentPatch{Conditions: cs})
		violationsOf(t, "patch with "+name, patched.StatusCode(), patched.Body, "invalid_segment")
	}
	if res := do(admin.GetSegmentWithResponse(t.Context(), p.Key, "nested")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("the refused segment = %d, want none", res.StatusCode())
	}
	if res := do(admin.GetSegmentWithResponse(t.Context(), p.Key, "beta_testers")); res.JSON200 == nil || res.JSON200.Version != 1 || len(res.JSON200.Conditions) != 0 {
		t.Errorf("beta_testers after refused changes = %d %s", res.StatusCode(), res.Body)
	}
}

func TestSegmentConditionsValidateAgainstDeclarations(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	pro := management.SegmentCreate{Key: "pro", Name: "Pro", Conditions: conditions(attr("plan", "eq", `"pro"`))}
	undeclared := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, pro))
	if got := violationsOf(t, "an undeclared attribute", undeclared.StatusCode(), undeclared.Body, "invalid_segment"); !slices.Equal(got, []string{"/conditions/0/attribute unknown_attribute"}) {
		t.Errorf("violations = %v", got)
	}
	for key, typ := range map[string]management.AttributeType{
		"plan": management.AttributeTypeString, "seats": management.AttributeTypeNumber, "beta": management.AttributeTypeBoolean,
		"signed_up": management.AttributeTypeDate, "roles": management.AttributeTypeList,
	} {
		if res := do(admin.CreateAttributeWithResponse(t.Context(), p.Key, management.AttributeCreate{Key: key, Name: key, Type: typ})); res.JSON201 == nil {
			t.Fatalf("declaring %s = %d %s", key, res.StatusCode(), res.Body)
		}
	}
	createSegment(t, admin, p.Key, pro)
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "typed", Name: "Typed", Conditions: conditions(
		attr("seats", "gte", `10`), attr("beta", "eq", `false`), attr("app_version", "in", `["1.40.0","1.41.0"]`),
		attr("signed_up", "after", `"2025-06-30T12:00:00.5+03:00"`), attr("roles", "contains_any", `["admin","owner"]`),
		attr("roles", "is_empty", ""), attr("os", "matches", `"^(android|ios)$"`), attr("beta", "exists", `null`),
	)})
	for name, tc := range map[string]struct {
		c    management.Condition
		want string
	}{
		"lt on a string":              {attr("os", "lt", `"x"`), "/conditions/0/op invalid_operator"},
		"a string for a number":       {attr("seats", "eq", `"5"`), "/conditions/0/value invalid_value"},
		"a broken version in a list":  {attr("app_version", "in", `["1.41.0","garbage"]`), "/conditions/0/value invalid_value"},
		"a date that is not RFC 3339": {attr("signed_up", "before", `"2026-01-01"`), "/conditions/0/value invalid_value"},
		"a value for exists":          {attr("os", "exists", `"android"`), "/conditions/0/value invalid_value"},
		"no operator":                 {management.Condition{Kind: "attribute", Attribute: ptr("os")}, "/conditions/0/op invalid_operator"},
		"a segment on an attribute":   {management.Condition{Kind: "attribute", Attribute: ptr("os"), Op: ptr("exists"), Segment: ptr("pro")}, "/conditions/0 invalid_value"},
	} {
		res := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "refused", Name: name, Conditions: conditions(tc.c)}))
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_segment"); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: violations = %v, want %s", name, got, tc.want)
		}
	}
}

func TestSegmentLimits(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	ids := func(n int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = `"u-1"`
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	for _, op := range []string{"in", "not_in"} {
		createSegment(t, admin, p.Key, management.SegmentCreate{Key: "max_" + op, Name: "Max", Conditions: conditions(attr("user", op, ids(10000)))})
		over := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "over_" + op, Name: "Over",
			Conditions: conditions(attr("user", op, ids(10001)))}))
		if got := violationsOf(t, "10,001 entries", over.StatusCode(), over.Body, "invalid_segment"); !slices.Equal(got, []string{"/conditions/0/value too_many"}) {
			t.Errorf("%s: violations = %v", op, got)
		}
	}
	twenty := make([]management.Condition, 20)
	for i := range twenty {
		twenty[i] = attr("os", "exists", "")
	}
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "twenty", Name: "Twenty", Conditions: &twenty})
	more := append(slices.Clone(twenty), attr("os", "exists", ""))
	created := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "more", Name: "More", Conditions: &more}))
	if got := violationsOf(t, "21 conditions", created.StatusCode(), created.Body, "invalid_segment"); !slices.Equal(got, []string{"/conditions too_many"}) {
		t.Errorf("violations = %v", got)
	}
	patched := updateSegment(t, admin, p.Key, "twenty", 1, management.SegmentPatch{Conditions: &more})
	violationsOf(t, "21 conditions by PATCH", patched.StatusCode(), patched.Body, "invalid_segment")
}

func TestAUnitTypeWorksAsAStringAttributeInASegment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	if res := do(admin.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: "organization", Name: "Organization"})); res.JSON201 == nil {
		t.Fatalf("declaring organization = %d %s", res.StatusCode(), res.Body)
	}
	testers := createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Beta testers", Private: ptr(true),
		Conditions: conditions(attr("user", "in", `["u-beta-1","u-beta-2"]`))})
	if !testers.Private {
		t.Errorf("beta_testers = %s, want private", raw(testers))
	}
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_customers", Name: "Beta customers",
		Conditions: conditions(attr("organization", "starts_with", `"org-"`), attr("anonymous", "not_exists", ""))})
	for name, tc := range map[string]struct {
		c    management.Condition
		want string
	}{
		"lt":           {attr("user", "lt", `"u"`), "/conditions/0/op invalid_operator"},
		"a number":     {attr("user", "eq", `42`), "/conditions/0/value invalid_value"},
		"contains_any": {attr("user", "contains_any", `["u"]`), "/conditions/0/op invalid_operator"},
	} {
		res := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "refused", Name: name, Conditions: conditions(tc.c)}))
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_segment"); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: violations = %v, want %s", name, got, tc.want)
		}
	}
}

func TestSegmentWritesBumpEveryEnvironmentAndRecordAnAuditEntry(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	var id string
	steps := []struct {
		name   string
		write  func() (int, []byte)
		status int
		bumps  bool
	}{
		{"create", func() (int, []byte) {
			res := do(admin.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: "apple", Name: "Apple",
				Conditions: conditions(attr("os", "eq", `"ios"`))}))
			if res.JSON201 != nil {
				id = res.JSON201.Id.String()
			}
			return res.StatusCode(), res.Body
		}, http.StatusCreated, true},
		{"a name", func() (int, []byte) {
			res := updateSegment(t, admin, p.Key, "apple", 1, management.SegmentPatch{Name: ptr("Apple devices")})
			return res.StatusCode(), res.Body
		}, http.StatusOK, false},
		{"conditions", func() (int, []byte) {
			res := updateSegment(t, admin, p.Key, "apple", 2, management.SegmentPatch{Conditions: conditions(attr("os", "in", `["ios","ipados"]`))})
			return res.StatusCode(), res.Body
		}, http.StatusOK, true},
		{"private", func() (int, []byte) {
			res := updateSegment(t, admin, p.Key, "apple", 3, management.SegmentPatch{Private: ptr(true)})
			return res.StatusCode(), res.Body
		}, http.StatusOK, true},
		{"an undeclared attribute", func() (int, []byte) {
			res := updateSegment(t, admin, p.Key, "apple", 4, management.SegmentPatch{Conditions: conditions(attr("tier", "eq", `"gold"`))})
			return res.StatusCode(), res.Body
		}, http.StatusUnprocessableEntity, false},
		{"a stale version", func() (int, []byte) {
			res := updateSegment(t, admin, p.Key, "apple", 3, management.SegmentPatch{Name: ptr("Stale")})
			return res.StatusCode(), res.Body
		}, http.StatusPreconditionFailed, false},
		{"no version", func() (int, []byte) {
			res := do(admin.UpdateSegmentWithResponse(t.Context(), p.Key, "apple", nil, management.SegmentPatch{Name: ptr("None")}))
			return res.StatusCode(), res.Body
		}, http.StatusPreconditionRequired, false},
		{"nothing", func() (int, []byte) {
			res := updateSegment(t, admin, p.Key, "apple", 4, management.SegmentPatch{Private: ptr(true),
				Conditions: conditions(attr("os", "in", `["ios","ipados"]`))})
			return res.StatusCode(), res.Body
		}, http.StatusOK, false},
		{"delete without a version", func() (int, []byte) {
			res := do(admin.DeleteSegmentWithResponse(t.Context(), p.Key, "apple", nil))
			return res.StatusCode(), res.Body
		}, http.StatusPreconditionRequired, false},
		{"delete at a stale version", func() (int, []byte) {
			res := deleteSegment(t, admin, p.Key, "apple", 3)
			return res.StatusCode(), res.Body
		}, http.StatusPreconditionFailed, false},
		{"delete", func() (int, []byte) {
			res := deleteSegment(t, admin, p.Key, "apple", 4)
			return res.StatusCode(), res.Body
		}, http.StatusNoContent, true},
	}
	for _, s := range steps {
		before := rulesetVersions(t, p)
		if got, body := s.write(); got != s.status {
			t.Fatalf("%s = %d %s, want %d", s.name, got, body, s.status)
		}
		want := []string(nil)
		if s.bumps {
			want = everyEnvironment
		}
		if b := bumped(t, s.name, before, rulesetVersions(t, p)); !slices.Equal(b, want) {
			t.Errorf("%s bumped %v, want %v", s.name, b, want)
		}
	}

	res := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{EntityId: &id}))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	var actions []string
	for _, e := range res.JSON200.Items {
		actions = append(actions, e.Action)
		if e.Entity.Kind != "segment" || e.Entity.Key != "apple" || e.EnvironmentId != nil {
			t.Errorf("%s = %+v, want the segment apple in no environment", e.Action, e.Entity)
		}
	}
	if want := []string{"segment.deleted", "segment.updated", "segment.updated", "segment.updated", "segment.created"}; !slices.Equal(actions, want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	deleted := res.JSON200.Items[0]
	before, _ := json.Marshal(deleted.Before)
	if deleted.After != nil || !strings.Contains(string(before), `"version":4`) || !strings.Contains(string(before), `"private":true`) {
		t.Errorf("segment.deleted before %s after %v", before, deleted.After)
	}
}

func TestSegmentEndpointsOfAnUnknownProjectOrSegmentAreNotFound(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	calls := map[string]func(project string) int{
		"get": func(project string) int {
			return do(admin.GetSegmentWithResponse(t.Context(), project, "x")).StatusCode()
		},
		"patch": func(project string) int {
			return updateSegment(t, admin, project, "x", 1, management.SegmentPatch{Name: ptr("y")}).StatusCode()
		},
		"delete": func(project string) int { return deleteSegment(t, admin, project, "x", 1).StatusCode() },
		"usages": func(project string) int {
			return do(admin.ListSegmentUsagesWithResponse(t.Context(), project, "x")).StatusCode()
		},
	}
	for name, call := range calls {
		for _, project := range []string{"no-such-project", p.Key} {
			if got := call(project); got != http.StatusNotFound {
				t.Errorf("%s in %s = %d, want 404", name, project, got)
			}
		}
	}
	if got := do(admin.ListSegmentsWithResponse(t.Context(), "no-such-project")).StatusCode(); got != http.StatusNotFound {
		t.Errorf("list in an unknown project = %d", got)
	}
	created := do(admin.CreateSegmentWithResponse(t.Context(), "no-such-project", management.SegmentCreate{Key: "s", Name: "S"}))
	if created.StatusCode() != http.StatusNotFound {
		t.Errorf("create in an unknown project = %d", created.StatusCode())
	}
}
