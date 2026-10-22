package sdk_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// answer is an HTTP answer read whole.
type answer struct {
	status int
	header http.Header
	body   []byte
}

// postEvaluate posts body to srv's evaluate as contentType with key value,
// and the extra headers given as name, value pairs.
func postEvaluate(t *testing.T, srv *testkit.Server, value, contentType string, body []byte, headers ...string) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+sdk.BaseURL+"/evaluate", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if value != "" {
		req.Header.Set("Authorization", "Bearer "+value)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, _ := io.ReadAll(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: got}
}

// evaluation posts a context as JSON and decodes the evaluation answered.
func evaluation(t *testing.T, srv *testkit.Server, value, body string) (sdk.Evaluation, answer) {
	t.Helper()
	a := postEvaluate(t, srv, value, "application/json", []byte(body))
	if a.status != http.StatusOK {
		t.Fatalf("evaluate = %d: %s", a.status, a.body)
	}
	var ev sdk.Evaluation
	if err := json.Unmarshal(a.body, &ev); err != nil {
		t.Fatalf("not an evaluation: %v: %s", err, a.body)
	}
	return ev, a
}

func attribute(name, op, value string) management.Condition {
	c := management.Condition{Kind: "attribute", Attribute: &name, Op: &op}
	if value != "" {
		if err := json.Unmarshal([]byte(value), &c.Value); err != nil {
			panic(err)
		}
	}
	return c
}

func variationServe(v string) management.Serve {
	return management.Serve{Kind: "variation", Variation: &v}
}

// stringFlag creates a string flag with variations a, b and c and puts its
// configuration in production.
func (p project) stringFlag(t *testing.T, key string, put management.FlagConfigurationPut) management.FlagConfiguration {
	t.Helper()
	vs := []management.VariationInput{}
	for _, v := range []string{"a", "b", "c"} {
		vs = append(vs, management.VariationInput{Key: v, Value: management.VariationValue(`"` + strings.ToUpper(v) + `"`)})
	}
	created := do(p.admin.CreateFlagWithResponse(t.Context(), p.key, management.FlagCreate{Key: key, Name: key, Type: "string", Variations: &vs}))
	if created.JSON201 == nil {
		t.Fatalf("POST …/flags %s = %d: %s", key, created.StatusCode(), created.Body)
	}
	res := do(p.admin.PutFlagConfigurationWithResponse(t.Context(), p.key, key, "production",
		&management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)}, put))
	if res.JSON200 == nil {
		t.Fatalf("PUT …/%s/configurations/production = %d: %s", key, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// targeted seeds production with a flag decided each way an evaluation can
// be, a private segment deciding one, and answers the rule IDs by flag.
func (p project) targeted(t *testing.T) map[string]string {
	t.Helper()
	seg := do(p.admin.CreateSegmentWithResponse(t.Context(), p.key, management.SegmentCreate{
		Key: "insiders", Name: "Insiders", Private: ptr(true), Conditions: &[]management.Condition{attribute("country", "eq", `"nz"`)},
	}))
	if seg.JSON201 == nil {
		t.Fatalf("POST …/segments = %d: %s", seg.StatusCode(), seg.Body)
	}
	segment := "insiders"
	unit, full := "user", 10_000
	p.flag(t, "gate")
	rules := map[string]string{}
	p.stringFlag(t, "plain", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	p.stringFlag(t, "dark", management.FlagConfigurationPut{On: false, OffVariation: "c", Fallthrough: "b"})
	ruled := p.stringFlag(t, "ruled", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: &[]management.Condition{attribute("os", "eq", `"android"`)}, Serve: variationServe("b")}}})
	rules["ruled"] = ruled.Rules[0].Id.String()
	private := p.stringFlag(t, "private", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: &[]management.Condition{{Kind: "in_segment", Segment: &segment}}, Serve: variationServe("c")}}})
	rules["private"] = private.Rules[0].Id.String()
	rolled := p.stringFlag(t, "rolled", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: management.Serve{Kind: "rollout", UnitType: &unit, Variation: ptr("c"), Percentage: &full}}}})
	rules["rolled"] = rolled.Rules[0].Id.String()
	p.stringFlag(t, "gated", management.FlagConfigurationPut{On: true, OffVariation: "c", Fallthrough: "a",
		Prerequisites: &[]management.Prerequisite{{Flag: "gate", Variation: "true"}}})
	p.stringFlag(t, "pinned", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "u-1", Variation: "b"}}})
	return rules
}

// results returns the reason, value and rule ID of each flag of ev.
func results(ev sdk.Evaluation) map[string]string {
	out := map[string]string{}
	for key, f := range ev.Flags {
		value, _ := json.Marshal(f.Value)
		s := f.Reason + " " + string(value)
		if f.RuleId != nil {
			s += " " + *f.RuleId
		}
		out[key] = s
	}
	return out
}

const targetedContext = `{"context":{"units":{"user":"u-1"},"attributes":{"os":"android","country":"nz"}}}`

func TestAClientKeyIsAnsweredServedValuesWithoutHowTheyWereReached(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.targeted(t)
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)

	ev, a := evaluation(t, srv, client, targetedContext)
	want := map[string]string{
		"gate":    `off false`,
		"plain":   `targeting_match "B"`,
		"dark":    `off "C"`,
		"ruled":   `targeting_match "B"`,
		"private": `targeting_match "C"`,
		"rolled":  `targeting_match "C"`,
		"gated":   `targeting_match "C"`,
		"pinned":  `override "B"`,
	}
	if got := results(ev); !reflect.DeepEqual(got, want) {
		t.Errorf("client answer = %v\nwant %v", got, want)
	}
	for _, leak := range []string{"rule_id", "insiders", "prerequisite", "bucket", `"rule"`, "rollout", "fallthrough"} {
		if bytes.Contains(a.body, []byte(leak)) {
			t.Errorf("the client answer carries %q: %s", leak, a.body)
		}
	}
	if got := a.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if !ev.Flags["pinned"].OverrideInfluenced || ev.Flags["plain"].OverrideInfluenced {
		t.Errorf("override_influenced = %v, %v, want the override's only", ev.Flags["pinned"].OverrideInfluenced, ev.Flags["plain"].OverrideInfluenced)
	}
	if f := ev.Flags["plain"]; f.Type != "string" || f.Variation == nil || *f.Variation != "b" || f.AutomaticExposure {
		t.Errorf("plain = %+v, want its type and variation", f)
	}

	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	rs := ruleset(t, srv, server)
	version, _ := strconv.ParseInt(strings.Trim(rs.HTTPResponse.Header.Get("ETag"), `"`), 10, 64)
	if ev.SchemaVersion != 1 || ev.RulesetVersion != version || ev.Environment.Key != "production" || ev.Environment.Protected {
		t.Errorf("envelope = schema %d, ruleset %d, environment %+v; want 1, %d, production unprotected",
			ev.SchemaVersion, ev.RulesetVersion, ev.Environment, version)
	}
	if !bytes.Contains(a.body, []byte(`"ignored_attributes":[]`)) {
		t.Errorf("ignored_attributes is not an empty list: %s", a.body)
	}
}

func TestAServerKeyIsAnsweredTheTrueReasonAndRule(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	rules := p.targeted(t)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)

	ev, _ := evaluation(t, srv, server, targetedContext)
	want := map[string]string{
		"gate":    `off false`,
		"plain":   `fallthrough "B"`,
		"dark":    `off "C"`,
		"ruled":   `rule "B" ` + rules["ruled"],
		"private": `rule "C" ` + rules["private"],
		"rolled":  `rollout "C" ` + rules["rolled"],
		"gated":   `prerequisite_failed "C"`,
		"pinned":  `override "B"`,
	}
	if got := results(ev); !reflect.DeepEqual(got, want) {
		t.Errorf("server answer = %v\nwant %v", got, want)
	}

	elsewhere, _ := evaluation(t, srv, server, `{"context":{"units":{"user":"u-2"},"attributes":{"country":"au"}}}`)
	if got := results(elsewhere)["private"]; got != `fallthrough "A"` {
		t.Errorf("outside the private segment: %s, want fallthrough", got)
	}
}

func TestRemoteEvaluationAnswersTheFlagsAskedForOnly(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.targeted(t)
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)

	ev, _ := evaluation(t, srv, client, `{"context":{},"flags":["plain","deleted_long_ago","plain","ruled"]}`)
	keys := make([]string, 0, len(ev.Flags))
	for k := range ev.Flags {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"plain", "ruled"}) {
		t.Errorf("flags = %v, want the known ones asked for", keys)
	}
	none, a := evaluation(t, srv, client, `{"context":{"units":null,"attributes":null},"flags":[]}`)
	if len(none.Flags) != 0 || !bytes.Contains(a.body, []byte(`"flags":{}`)) {
		t.Errorf("no flags asked for: %s", a.body)
	}
}

func TestAttributesAreTypedAndTheIgnoredOnesListed(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.stringFlag(t, "android_only", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: &[]management.Condition{attribute("os", "eq", `"android"`)}, Serve: variationServe("b")}}})
	p.stringFlag(t, "big_builds", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: &[]management.Condition{attribute("build_number", "gt", "100")}, Serve: variationServe("b")}}})
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)

	ev, _ := evaluation(t, srv, client, `{"context":{"units":{"user":"u-1"},"attributes":{
		"os":"android","build_number":"500","favourite_colour":"green","user":"u-2","app_version":"1.2.3","locale":null}}}`)
	want := []sdk.IgnoredAttribute{
		{Name: "build_number", Code: "type_mismatch"},
		{Name: "favourite_colour", Code: "undeclared"},
		{Name: "user", Code: "undeclared"},
	}
	if !reflect.DeepEqual(ev.IgnoredAttributes, want) {
		t.Errorf("ignored_attributes = %v, want %v", ev.IgnoredAttributes, want)
	}
	if got := results(ev); got["android_only"] != `targeting_match "B"` || got["big_builds"] != `targeting_match "A"` {
		t.Errorf("answer = %v, want the declared os to decide and the mistyped build number left out", got)
	}
}

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func TestRemoteEvaluationRefusesWhatBreaksItsLimits(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout")
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	ok := `{"context":{"units":{"user":"u-1"}}}`
	padded := func(n int) []byte {
		return []byte(`{"context":{"attributes":{"country":"` + strings.Repeat("a", n) + `"}}}`)
	}
	atLimit := `{"context":{"units":{"user":"` + strings.Repeat("я", 128) + `"},"attributes":{"country":"` + strings.Repeat("я", 256) + `"}}}`

	for _, tc := range []struct {
		name, contentType string
		body              []byte
		headers           []string
		status            int
		code              string
	}{
		{"JSON", "application/json", []byte(ok), nil, http.StatusOK, ""},
		{"a charset", "application/json; charset=utf-8", []byte(ok), nil, http.StatusOK, ""},
		{"at every limit", "application/json", []byte(atLimit), nil, http.StatusOK, ""},
		{"gzip", "application/json", gzipped([]byte(ok)), []string{"Content-Encoding", "gzip"}, http.StatusOK, ""},
		{"over 32 KiB", "application/json", padded(33 << 10), nil, http.StatusRequestEntityTooLarge, "request_too_large"},
		{"over 32 KiB decompressed", "application/json", gzipped(padded(33 << 10)), []string{"Content-Encoding", "gzip"},
			http.StatusRequestEntityTooLarge, "request_too_large"},
		{"plain text", "text/plain", []byte(ok), nil, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"no media type", "", []byte(ok), nil, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"not JSON", "application/json", []byte(`{"context":`), nil, http.StatusBadRequest, "invalid_request"},
		{"empty", "application/json", nil, nil, http.StatusBadRequest, "invalid_request"},
		{"trailing data", "application/json", []byte(ok + `{}`), nil, http.StatusBadRequest, "invalid_request"},
		{"no context", "application/json", []byte(`{"flags":["checkout"]}`), nil, http.StatusBadRequest, "invalid_request"},
		{"a unit ID not a string", "application/json", []byte(`{"context":{"units":{"user":7}}}`), nil, http.StatusBadRequest, "invalid_request"},
		{"a flag not a string", "application/json", []byte(`{"context":{},"flags":[1]}`), nil, http.StatusBadRequest, "invalid_request"},
	} {
		a := postEvaluate(t, srv, client, tc.contentType, tc.body, tc.headers...)
		if tc.code == "" {
			if a.status != tc.status {
				t.Errorf("%s: %d, want %d: %s", tc.name, a.status, tc.status, a.body)
			}
			continue
		}
		refused(t, tc.name, a.status, a.body, tc.status, tc.code)
	}

	a := postEvaluate(t, srv, client, "application/json", []byte(`{"context":{
		"units":{"user":"`+strings.Repeat("a", 257)+`","device":"d-1","device":"d-2"},
		"attributes":{"country":"`+strings.Repeat("я", 257)+`","tags":["ok","`+strings.Repeat("x", 257)+`"],"seen":[`+
		strings.TrimSuffix(strings.Repeat(`"x",`, 65), ",")+`]}}}`))
	refused(t, "over the context's limits", a.status, a.body, http.StatusUnprocessableEntity, "invalid_context")
	var pointers []string
	if pr := problemOf(t, a.body); pr.Errors != nil {
		for _, v := range *pr.Errors {
			pointers = append(pointers, v.Pointer+" "+v.Code)
		}
	}
	want := []string{
		"/context/attributes/country too_long", "/context/attributes/seen too_many", "/context/attributes/tags/1 too_long",
		"/context/units/device duplicate", "/context/units/user too_long",
	}
	if !slices.Equal(pointers, want) {
		t.Errorf("violations = %v, want %v", pointers, want)
	}
}

func TestRemoteEvaluationIsAnswered503UntilTheRulesetIsLoaded(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	p := newProject(t, srv)
	p.flag(t, "checkout")
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)

	fault.Stall()
	a := postEvaluate(t, srv, client, "application/json", []byte(`{"context":{}}`))
	refused(t, "while loading", a.status, a.body, http.StatusServiceUnavailable, "ruleset_unavailable")
	if got := a.header.Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
	fault.Release()
	if a := postEvaluate(t, srv, client, "application/json", []byte(`{"context":{}}`)); a.status != http.StatusOK {
		t.Errorf("once loaded: %d %s", a.status, a.body)
	}
}

func TestRemoteEvaluationNegotiatesTheSchemaVersionAndNeedsAKey(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	body := []byte(`{"context":{}}`)
	for _, v := range []string{"1", "99"} {
		if a := postEvaluate(t, srv, client, "application/json", body, "Flagnetic-Schema-Version", v); a.status != http.StatusOK {
			t.Errorf("schema %s: %d %s", v, a.status, a.body)
		}
	}
	a := postEvaluate(t, srv, client, "application/json", body, "Flagnetic-Schema-Version", "0")
	refused(t, "schema 0", a.status, a.body, http.StatusNotAcceptable, "schema_version_unsupported")
	a = postEvaluate(t, srv, "", "application/json", body)
	refused(t, "no key", a.status, a.body, http.StatusUnauthorized, "sdk_key_missing")
}
