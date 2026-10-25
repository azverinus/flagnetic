package sdk_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// TestTheServerAnswersEveryProtocolExchange drives the server through every
// exchange the SDKs are tested against, in their order, and compares each
// answer with the document: the status, the headers an SDK acts on and the
// body. Only what differs on every run is normalized: ruleset versions,
// which come from the clock while the documents keep small example numbers
// that SDKs only compare, rule IDs and rollout salts, which the server
// mints, and request IDs.
func TestTheServerAnswersEveryProtocolExchange(t *testing.T) {
	c := clock.NewFake(protocolTime)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	keys := p.protocolProject(t)
	versions := map[int64]int64{p.rulesetVersion(t, "production"): 1, p.rulesetVersion(t, "staging"): 1}

	// The exchanges no single server can answer in sequence: a client key's
	// evaluate limit of one per minute, a key whose rotation overlap has
	// ended by the replica's clock, and a replica that cannot load the
	// ruleset.
	limited := testkit.StartServer(t, testkit.WithClock(c), testkit.WithRateLimits(func(r *config.RateLimits) { r.ClientEvaluate = 1 }))
	later := testkit.StartServer(t, testkit.WithClock(clock.NewFake(protocolTime.Add(2*time.Hour))))
	fault := &pgdelivery.RulesetLoadFault{}
	fault.Fail()
	unloaded := testkit.StartServer(t, testkit.WithClock(c), testkit.WithRulesetLoadFault(fault))

	pending := pendingExpectations(t)
	var exchanges []exchange
	readJSON(t, filepath.Join(protocolDir, "server", "exchanges.json"), &exchanges)
	for name := range pending {
		if !slices.ContainsFunc(exchanges, func(ex exchange) bool { return ex.Name == name }) {
			t.Errorf("%s names %q, which is no exchange", pendingFile, name)
		}
	}
	for _, ex := range exchanges {
		target := srv
		switch ex.Name {
		case "rate limited":
			target = limited
			sendExchange(t, target, ex, keys, versions) // spends the one evaluation of the window
		case "key past its rotation overlap":
			target = later
		case "ruleset on a replica that has not loaded it":
			target = unloaded
		}
		res := sendExchange(t, target, ex, keys, versions)
		var body []byte
		switch ex.Name {
		case "stream":
			// The current version at once, a heartbeat, then the version a
			// kill switch made.
			r := bufio.NewReader(res.Body)
			body = append(frames(t, r, 2), heartbeatAfter(t, c, r)...)
			p.kill(t, "checkout", "production")
			versions[p.rulesetVersion(t, "production")] = 2
			body = append(body, frames(t, r, 1)...)
		case "stream holding the current version":
			r := bufio.NewReader(res.Body)
			body = append(frames(t, r, 1), heartbeatAfter(t, c, r)...)
		default:
			body, _ = io.ReadAll(res.Body)
		}
		_ = res.Body.Close()
		t.Run(ex.Name, func(t *testing.T) { answers(t, ex, res, body, versions, pending[ex.Name]) })
	}
}

// protocolProject seeds the project the documents describe and answers the
// key of each placeholder the exchanges use.
func (p project) protocolProject(t *testing.T) map[string]string {
	t.Helper()
	ok := func(status int, body []byte, what string) {
		t.Helper()
		if status >= http.StatusMultipleChoices {
			t.Fatalf("%s = %d: %s", what, status, body)
		}
	}
	plan := do(p.admin.CreateAttributeWithResponse(t.Context(), p.key, management.AttributeCreate{Key: "plan", Name: "Plan", Type: "string"}))
	ok(plan.StatusCode(), plan.Body, "declare plan")
	for _, s := range []management.SegmentCreate{
		{Key: "employees", Name: "Employees", Private: ptr(true), Conditions: &[]management.Condition{{Kind: "attribute", Attribute: ptr("user"), Op: ptr("in"), Value: []string{"u-1001", "u-1002"}}}},
		{Key: "pro", Name: "Pro", Private: ptr(false), Conditions: &[]management.Condition{{Kind: "attribute", Attribute: ptr("plan"), Op: ptr("eq"), Value: "pro"}}},
	} {
		res := do(p.admin.CreateSegmentWithResponse(t.Context(), p.key, s))
		ok(res.StatusCode(), res.Body, "create segment "+s.Key)
	}
	variations := func(kv ...string) *[]management.VariationInput {
		var vs []management.VariationInput
		for i := 0; i+1 < len(kv); i += 2 {
			vs = append(vs, management.VariationInput{Key: kv[i], Value: json.RawMessage(kv[i+1])})
		}
		return &vs
	}
	for _, f := range []management.FlagCreate{
		{Key: "banner_text", Name: "Banner text", Type: "string", AutomaticExposure: ptr(true), Variations: variations("short", `"Try free"`, "long", `"Start your free trial"`)},
		{Key: "checkout", Name: "Checkout", Type: "boolean"},
		{Key: "max_items", Name: "Max items", Type: "number", Variations: variations("few", `10`, "many", `50`)},
		{Key: "new_search", Name: "New search", Type: "boolean"},
		{Key: "theme", Name: "Theme", Type: "json", Variations: variations("light", `{"mode":"light"}`, "dark", `{"mode":"dark","contrast":"high"}`)},
	} {
		res := do(p.admin.CreateFlagWithResponse(t.Context(), p.key, f))
		ok(res.StatusCode(), res.Body, "create flag "+f.Key)
	}
	attribute := func(name, op string, value any) []management.Condition {
		return []management.Condition{{Kind: "attribute", Attribute: ptr(name), Op: ptr(op), Value: value}}
	}
	inSegment := func(segment string) []management.Condition {
		return []management.Condition{{Kind: "in_segment", Segment: ptr(segment)}}
	}
	configure := func(flag, environment string, put management.FlagConfigurationPut) {
		t.Helper()
		res := do(p.admin.PutFlagConfigurationWithResponse(t.Context(), p.key, flag, environment, &management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)}, put))
		ok(res.StatusCode(), res.Body, "configure "+flag+" in "+environment)
	}
	configure("banner_text", "production", management.FlagConfigurationPut{On: true, OffVariation: "short", Fallthrough: "long",
		Rules: &[]management.RuleInput{{Conditions: ptr(attribute("country", "eq", "DE")), Serve: management.Serve{Kind: "variation", Variation: ptr("short")}}}})
	configure("checkout", "production", management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false",
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "qa-7", Variation: "true"}},
		Rules: &[]management.RuleInput{
			{Conditions: ptr(inSegment("employees")), Serve: management.Serve{Kind: "variation", Variation: ptr("true")}},
			{Conditions: ptr(attribute("app_version", "gte", "1.17.0")), Serve: management.Serve{Kind: "rollout", Variation: ptr("true"), UnitType: ptr("user"), Percentage: ptr(5000)}},
		}})
	configure("max_items", "production", management.FlagConfigurationPut{On: true, OffVariation: "few", Fallthrough: "few",
		Rules: &[]management.RuleInput{{Conditions: ptr(inSegment("pro")), Serve: management.Serve{Kind: "variation", Variation: ptr("many")}}}})
	configure("new_search", "production", management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true",
		Prerequisites: &[]management.Prerequisite{{Flag: "checkout", Variation: "true"}}})
	configure("theme", "production", management.FlagConfigurationPut{On: false, OffVariation: "light", Fallthrough: "dark"})
	configure("banner_text", "staging", management.FlagConfigurationPut{On: true, OffVariation: "short", Fallthrough: "long"})
	configure("checkout", "staging", management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	configure("max_items", "staging", management.FlagConfigurationPut{On: false, OffVariation: "few", Fallthrough: "few"})
	configure("new_search", "staging", management.FlagConfigurationPut{On: false, OffVariation: "false", Fallthrough: "true"})
	configure("theme", "staging", management.FlagConfigurationPut{On: false, OffVariation: "light", Fallthrough: "light"})
	// Production was configured unprotected; its documents show it
	// protected, as a new project's production is.
	p.setProtected(t, "production", true)

	// The experiment the documents' exposures belong to. It is a draft, which
	// an SDK's ruleset does not show, so the ruleset documents stay as they are.
	experiment := do(p.admin.CreateExperimentWithResponse(t.Context(), p.key, management.ExperimentCreate{
		Key: "banner_test", Name: "Banner test", Flag: "banner_text", UnitType: "user",
		Variations: []management.ExperimentWeight{{Variation: "short", Weight: 5000}, {Variation: "long", Weight: 5000}},
	}))
	ok(experiment.StatusCode(), experiment.Body, "create experiment banner_test")

	keys := map[string]string{}
	_, keys["<server key>"] = p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, keys["<client key>"] = p.sdkKey(t, "production", management.SdkKeyKindClient)
	_, keys["<staging server key>"] = p.sdkKey(t, "staging", management.SdkKeyKindServer)
	_, keys["<staging client key>"] = p.sdkKey(t, "staging", management.SdkKeyKindClient)
	revoked, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.revoke(t, "production", revoked)
	keys["<revoked server key>"] = value
	expiring, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.rotate(t, "production", expiring, 3600)
	keys["<expired server key>"] = value
	keys["<unknown server key>"] = "fn_srv_" + strings.Repeat("A", 40)
	restricted := do(p.admin.CreateSdkKeyWithResponse(t.Context(), p.key, "production", management.SdkKeyCreate{
		Kind: "client", Name: "web", AllowedOrigins: &[]string{"https://app.example.com"},
	}))
	ok(restricted.StatusCode(), restricted.Body, "create a client key with an allowed origin")
	keys["<client key allowing https://app.example.com>"] = restricted.JSON201.Value
	return keys
}

// rulesetVersion reads the version of environment's ruleset through the
// management API, which counts against no SDK limit.
func (p project) rulesetVersion(t *testing.T, environment string) int64 {
	t.Helper()
	res := do(p.admin.GetEnvironmentRulesetWithResponse(t.Context(), p.key, environment))
	if res.JSON200 == nil {
		t.Fatalf("ruleset of %s = %d: %s", environment, res.StatusCode(), res.Body)
	}
	v, _ := (*res.JSON200)["version"].(float64)
	return int64(v)
}

// sendExchange sends the exchange's request to srv, its placeholders replaced by
// keys and its document versions by the server's.
func sendExchange(t *testing.T, srv *testkit.Server, ex exchange, keys map[string]string, versions map[int64]int64) *http.Response {
	t.Helper()
	fill := func(s string) string {
		for placeholder, key := range keys {
			s = strings.ReplaceAll(s, placeholder, key)
		}
		return s
	}
	var body io.Reader
	switch ex.Name {
	case "truncated JSON":
		body = strings.NewReader(`{"context":{"units":{"user":"u-10`)
	case "body over 32 KiB":
		body = strings.NewReader(`{"context":{"attributes":{"note":"` + strings.Repeat("x", 33*1024) + `"}}}`)
	default:
		if len(ex.Request.Body) > 0 {
			body = bytes.NewReader(ex.Request.Body)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), ex.Request.Method, srv.URL+fill(ex.Request.Path), body)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range ex.Request.Headers {
		switch name {
		case "Last-Event-ID":
			value = strconv.FormatInt(servedVersion(t, versions, value), 10)
		case "If-None-Match":
			// "0" is a version the server never served; any other is one
			// the documents number.
			if value != `"0"` {
				value = strconv.Quote(strconv.FormatInt(servedVersion(t, versions, strings.Trim(value, `"`)), 10))
			}
		}
		req.Header.Set(name, fill(value))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// servedVersion returns the server's version that the documents number doc.
func servedVersion(t *testing.T, versions map[int64]int64, doc string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(doc, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var found int64
	for actual, numbered := range versions {
		if numbered == n && actual > found {
			found = actual
		}
	}
	return found
}

// frames reads n frames of a stream as sent, each through its blank line.
func frames(t *testing.T, r *bufio.Reader, n int) []byte {
	t.Helper()
	out := make(chan []byte, 1)
	go func() {
		var b []byte
		for read := 0; read < n; {
			line, err := r.ReadBytes('\n')
			b = append(b, line...)
			if err != nil {
				break
			}
			if string(line) == "\n" {
				read++
			}
		}
		out <- b
	}()
	select {
	case b := <-out:
		return b
	case <-time.After(5 * time.Second):
		t.Fatalf("the stream sent no %d frames within 5 s", n)
		return nil
	}
}

// heartbeatAfter moves the clock to the next heartbeat and reads it.
func heartbeatAfter(t *testing.T, c *clock.Fake, r *bufio.Reader) []byte {
	t.Helper()
	c.Advance(15 * time.Second)
	return frames(t, r, 1)
}

var (
	streamID      = regexp.MustCompile(`(?m)^id: (\d+)$`)
	streamVersion = regexp.MustCompile(`"version":(\d+)`)
)

// answers compares the answer with the exchange's document.
func answers(t *testing.T, ex exchange, res *http.Response, body []byte, versions map[int64]int64, pending []json.RawMessage) {
	if res.StatusCode != ex.Response.Status {
		t.Fatalf("status = %d, want %d: %s", res.StatusCode, ex.Response.Status, body)
	}
	for name, want := range ex.Response.Headers {
		got := res.Header.Get(name)
		if name == "ETag" {
			got = renumber(t, got, versions)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(pending) > 0 && (ex.Response.Body == "" || strings.HasSuffix(ex.Response.Body, ".txt")) {
		t.Errorf("%s replaces members of %q, whose answer has no JSON body to replace them in", pendingFile, ex.Name)
	}
	if ex.Response.Body == "" {
		if len(body) != 0 {
			t.Errorf("body = %s, want none", body)
		}
		return
	}
	doc, err := os.ReadFile(filepath.Join(protocolDir, ex.Response.Body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(ex.Response.Body, ".txt") {
		got := streamID.ReplaceAllStringFunc(string(body), func(m string) string { return "id: " + renumber(t, m[4:], versions) })
		got = streamVersion.ReplaceAllStringFunc(got, func(m string) string { return `"version":` + renumber(t, m[10:], versions) })
		if got != string(doc) {
			t.Errorf("stream =\n%q\nwant\n%q", got, doc)
		}
		return
	}
	var want, got any
	if err := json.Unmarshal(doc, &want); err != nil {
		t.Fatal(err)
	}
	for _, p := range pending {
		want = withPending(t, want, p)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	got = normalize(t, got, want, "", versions)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body differs from %s:\n%s", ex.Response.Body, strings.Join(differences(got, want, ""), "\n"))
	}
}

// renumber returns the documents' number of a served version, written as
// it was (an ETag keeps its quotes).
func renumber(t *testing.T, s string, versions map[int64]int64) string {
	t.Helper()
	n, err := strconv.ParseInt(strings.Trim(s, `"`), 10, 64)
	if err != nil {
		return s
	}
	numbered, ok := versions[n]
	if !ok {
		t.Errorf("version %d is none the run saw", n)
		return s
	}
	return strings.Replace(s, strconv.FormatInt(n, 10), strconv.FormatInt(numbered, 10), 1)
}

// normalize returns got with the members that differ on every run taken
// from want: versions renumbered, a rule's id and a rollout's salt, and the
// request ID.
func normalize(t *testing.T, got, want any, key string, versions map[int64]int64) any {
	t.Helper()
	switch g := got.(type) {
	case map[string]any:
		w, _ := want.(map[string]any)
		for k, v := range g {
			switch {
			case k == "request_id" || k == "salt" || k == "id" && key == "rules":
				if wv, ok := w[k]; ok {
					g[k] = wv
				}
			case k == "version" || k == "ruleset_version":
				if n, ok := v.(float64); ok {
					if numbered, ok := versions[int64(n)]; ok {
						g[k] = float64(numbered)
					}
				}
			default:
				g[k] = normalize(t, v, w[k], k, versions)
			}
		}
		return g
	case []any:
		w, _ := want.([]any)
		for i := range g {
			var wi any
			if i < len(w) {
				wi = w[i]
			}
			g[i] = normalize(t, g[i], wi, key, versions)
		}
		return g
	default:
		return got
	}
}

// pendingFile names the answers the documents pin that this server does
// not give yet, and what it answers instead. Deleting a group of it, once
// the server gives what the group lacks, holds the run to the documents
// there.
const pendingFile = "server/not-served-yet.json"

// pendingExpectations reads pendingFile: every group's expectations by
// exchange name.
func pendingExpectations(t *testing.T) map[string][]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(protocolDir, pendingFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Until map[string]struct {
			Exchanges map[string]json.RawMessage `json:"exchanges"`
		} `json:"until"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s: %v", pendingFile, err)
	}
	out := map[string][]json.RawMessage{}
	for _, group := range file.Until {
		for name, expectation := range group.Exchanges {
			out[name] = append(out[name], expectation)
		}
	}
	return out
}

// withPending returns want with the rejections pending replaces, by index,
// and the members it sets, by JSON Pointer.
func withPending(t *testing.T, want any, pending json.RawMessage) any {
	t.Helper()
	var p struct {
		Rejected []map[string]any `json:"rejected"`
		Members  map[string]any   `json:"members"`
	}
	if err := json.Unmarshal(pending, &p); err != nil {
		t.Fatal(err)
	}
	w, _ := want.(map[string]any)
	rejected, _ := w["rejected"].([]any)
	for _, instead := range p.Rejected {
		replaced := false
		for i, r := range rejected {
			if m, ok := r.(map[string]any); ok && m["index"] == instead["index"] {
				if reflect.DeepEqual(m, instead) {
					t.Errorf("%s replaces the rejection at index %v with what the document holds already", pendingFile, instead["index"])
				}
				rejected[i] = instead
				replaced = true
			}
		}
		if !replaced {
			t.Errorf("%s replaces a rejection at index %v the document does not hold", pendingFile, instead["index"])
		}
	}
	for pointer, value := range p.Members {
		parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		parent := w
		for _, part := range parts[:len(parts)-1] {
			parent, _ = parent[part].(map[string]any)
		}
		held, ok := parent[parts[len(parts)-1]]
		if !ok {
			t.Errorf("%s sets %s, which the document does not hold", pendingFile, pointer)
			continue
		}
		if reflect.DeepEqual(held, value) {
			t.Errorf("%s sets %s to what the document holds already", pendingFile, pointer)
		}
		parent[parts[len(parts)-1]] = value
	}
	return want
}

// differences lists where got and want differ, by JSON Pointer.
func differences(got, want any, at string) []string {
	g, gok := got.(map[string]any)
	w, wok := want.(map[string]any)
	if gok && wok {
		var out []string
		for k := range w {
			out = append(out, differences(g[k], w[k], at+"/"+k)...)
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				out = append(out, fmt.Sprintf("%s/%s: unexpected %v", at, k, g[k]))
			}
		}
		return out
	}
	ga, gok := got.([]any)
	wa, wok := want.([]any)
	if gok && wok && len(ga) == len(wa) {
		var out []string
		for i := range wa {
			out = append(out, differences(ga[i], wa[i], at+"/"+strconv.Itoa(i))...)
		}
		return out
	}
	if reflect.DeepEqual(got, want) {
		return nil
	}
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	return []string{fmt.Sprintf("%s: %s, want %s", at, gj, wj)}
}
