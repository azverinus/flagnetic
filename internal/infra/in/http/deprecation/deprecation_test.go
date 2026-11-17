package deprecation_test

import (
	"bytes"
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/deprecation"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/scim"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
)

var update = flag.Bool("update", false, "write the deprecation tables from the documents")

// documents are the APIs served with their deprecation tables.
var documents = []struct {
	file, pkg, generated string
	table                deprecation.Table
}{
	{"management.yaml", "management", "../management/deprecations.gen.go", management.Deprecated},
	{"sdk.yaml", "sdk", "../sdk/deprecations.gen.go", sdk.Deprecated},
	{"scim.yaml", "scim", "../scim/deprecations.gen.go", scim.Deprecated},
}

// TestTheTablesAreTheDocuments: the table each API serves its headers from is
// generated from its document, and `task generate` writes it. Nothing is
// deprecated in 1.0: when an operation is, the table says so.
func TestTheTablesAreTheDocuments(t *testing.T) {
	for _, d := range documents {
		t.Run(d.file, func(t *testing.T) {
			doc, err := os.ReadFile("../../../../../api/" + d.file)
			if err != nil {
				t.Fatal(err)
			}
			table, err := deprecation.Read(doc)
			if err != nil {
				t.Fatalf("the document announces a deprecation incompletely:\n%v", err)
			}
			want, err := deprecation.Render(d.pkg, d.file, table)
			if err != nil {
				t.Fatal(err)
			}
			if *update {
				if err = os.WriteFile(d.generated, want, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			got, err := os.ReadFile(d.generated)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s is stale; run task generate", d.generated)
			}
			if len(d.table) != len(table) {
				t.Errorf("the served table holds %d operations, the document %d", len(d.table), len(table))
			}
			for pattern, n := range table {
				if d.table[pattern] != n {
					t.Errorf("%s: served %+v, the document says %+v", pattern, d.table[pattern], n)
				}
			}
		})
	}
}

func TestNothingIsDeprecatedYet(t *testing.T) {
	for _, d := range documents {
		if len(d.table) != 0 {
			t.Errorf("%s: deprecated operations %v; 1.0 ships with none, so a deprecation is a decision: drop this guard with it", d.file, d.table)
		}
	}
}

func TestADeprecatedOperationMustSayWhenItWasDeprecated(t *testing.T) {
	for name, tc := range map[string]struct{ op, want string }{
		"no date":             {"deprecated: true", "without x-deprecated-since"},
		"a date that is none": {"deprecated: true\n      x-deprecated-since: soon", `"soon" is not a date`},
		"a sunset that is none": {"deprecated: true\n      x-deprecated-since: 2027-01-01\n      x-sunset: later",
			`"later" is not a date`},
		"a sunset before the deprecation": {"deprecated: true\n      x-deprecated-since: 2027-01-01\n      x-sunset: 2026-12-31",
			"is before x-deprecated-since"},
		"a date without the deprecation":   {"x-deprecated-since: 2027-01-01", "not deprecated: true"},
		"a sunset without the deprecation": {"x-sunset: 2027-01-01", "not deprecated: true"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := "paths:\n  /thing:\n    get:\n      operationId: getThing\n      " + tc.op + "\n"
			_, err := deprecation.Read([]byte(doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "GET /thing (getThing)") {
				t.Errorf("Read = %v, want an error naming the operation and containing %q", err, tc.want)
			}
		})
	}
	if _, err := deprecation.Read([]byte("paths: [")); err == nil {
		t.Error("an unreadable document was accepted")
	}
}

func TestOnlyTheOperationsOfAPathItemAreRead(t *testing.T) {
	doc := "paths:\n  /thing:\n    parameters: []\n    summary: x\n    get:\n      operationId: getThing\n      deprecated: true\n      x-deprecated-since: 2027-03-01\n"
	table, err := deprecation.Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := table["GET /thing"]; len(table) != 1 || !got.Since.Equal(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)) || !got.Sunset.IsZero() {
		t.Errorf("table = %+v", table)
	}
}

// TestADeprecatedOperationAnswersItsHeaders drives the SDK API's generated
// client against a server whose deprecation table is read from a fixture
// document that deprecates two of its operations, one with a sunset.
func TestADeprecatedOperationAnswersItsHeaders(t *testing.T) {
	doc, err := os.ReadFile("testdata/deprecated.yaml")
	if err != nil {
		t.Fatal(err)
	}
	table, err := deprecation.Read(doc)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, route := range []string{"GET /sdk/v1/ruleset", "POST /sdk/v1/evaluate", "POST /sdk/v1/events"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	}
	srv := httptest.NewServer(deprecation.Middleware(sdk.BaseURL, table)(mux))
	t.Cleanup(srv.Close)
	client, err := sdk.NewClientWithResponses(srv.URL + sdk.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	ruleset, err := client.GetRulesetWithResponse(ctx, &sdk.GetRulesetParams{})
	if err != nil {
		t.Fatal(err)
	}
	// 2027-03-01T00:00:00Z is 1803859200 seconds after the epoch.
	if got := ruleset.HTTPResponse.Header.Get("Deprecation"); got != "@1803859200" {
		t.Errorf("Deprecation = %q, want @1803859200", got)
	}
	if got := ruleset.HTTPResponse.Header.Get("Sunset"); got != "Wed, 01 Mar 2028 00:00:00 GMT" {
		t.Errorf("Sunset = %q", got)
	}
	if ruleset.StatusCode() != http.StatusAccepted {
		t.Errorf("the handler's own answer changed: %d", ruleset.StatusCode())
	}

	evaluate, err := client.EvaluateWithResponse(ctx, &sdk.EvaluateParams{}, sdk.EvaluateJSONRequestBody{})
	if err != nil {
		t.Fatal(err)
	}
	if got := evaluate.HTTPResponse.Header.Get("Deprecation"); got != "@1803859200" {
		t.Errorf("Deprecation = %q, want @1803859200", got)
	}
	if got := evaluate.HTTPResponse.Header.Get("Sunset"); got != "" {
		t.Errorf("an operation with no sunset answered Sunset: %q", got)
	}

	events, err := client.PostEventsWithResponse(ctx, &sdk.PostEventsParams{}, sdk.PostEventsJSONRequestBody{})
	if err != nil {
		t.Fatal(err)
	}
	if events.HTTPResponse.Header.Get("Deprecation") != "" || events.HTTPResponse.Header.Get("Sunset") != "" {
		t.Errorf("an operation that is not deprecated announced it: %v", events.HTTPResponse.Header)
	}
}

func TestAPathParameterAndAMethodDecideWhichOperationIsDeprecated(t *testing.T) {
	table := deprecation.Table{"DELETE /things/{thing}": {Since: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}
	h := deprecation.Middleware("/api/v1", table)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodDelete, "/api/v1/things/abc", true},
		{http.MethodGet, "/api/v1/things/abc", false},
		{http.MethodDelete, "/api/v1/things", false},
		{http.MethodDelete, "/other/v1/things/abc", false},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if got := rec.Header().Get("Deprecation") != ""; got != tc.want {
			t.Errorf("%s %s: announced %t, want %t", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestAnEmptyTableLeavesTheHandlerAlone(t *testing.T) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	deprecation.Middleware("/api/v1", nil)(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
	if len(rec.Header()) != 0 {
		t.Errorf("headers = %v", rec.Header())
	}
}
