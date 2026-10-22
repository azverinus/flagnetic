package sdk_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
)

// The protocol documents every SDK is tested against, and the contract the
// server is generated from. They describe the same API, so each document must
// be what the contract says that answer or request holds.
const (
	protocolDir = "../../../../../conformance/protocol"
	contract    = "../../../../../api/sdk.yaml"
)

type exchange struct {
	Name    string `json:"name"`
	Request struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	} `json:"request"`
	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"response"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// strictly decodes data into v, failing on a member v's type does not
// declare: a document carrying something the contract does not describe.
func strictly(t *testing.T, what string, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Errorf("%s does not match the contract: %v", what, err)
	}
}

// operations reads, from the contract, the statuses each operation declares,
// by method and path under /sdk/v1.
func operations(t *testing.T) map[string][]int {
	t.Helper()
	data, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]any `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][]int{}
	for path, methods := range doc.Paths {
		for method, op := range methods {
			for status := range op.Responses {
				n, err := strconv.Atoi(status)
				if err != nil {
					t.Fatalf("%s %s: status %q", method, path, status)
				}
				key := strings.ToUpper(method) + " /sdk/v1" + path
				out[key] = append(out[key], n)
			}
		}
	}
	return out
}

func TestEveryExchangeIsAnOperationAndStatusOfTheContract(t *testing.T) {
	ops := operations(t)
	var exchanges []exchange
	readJSON(t, filepath.Join(protocolDir, "server/exchanges.json"), &exchanges)
	for _, e := range exchanges {
		path, _, _ := strings.Cut(e.Request.Path, "?")
		statuses, ok := ops[e.Request.Method+" "+path]
		if !ok {
			// A path or method the API does not serve is answered by the
			// adapter itself, which the contract describes once for all.
			if e.Response.Status != 404 && e.Response.Status != 405 {
				t.Errorf("%s: %s %s is no operation of the contract", e.Name, e.Request.Method, path)
			}
			continue
		}
		if !slices.Contains(statuses, e.Response.Status) {
			t.Errorf("%s: %s %s answers %d, which the contract does not declare (%v)",
				e.Name, e.Request.Method, path, e.Response.Status, statuses)
		}
	}
}

func TestEveryDocumentTheServerAnswersIsOfTheContract(t *testing.T) {
	var exchanges []exchange
	readJSON(t, filepath.Join(protocolDir, "server/exchanges.json"), &exchanges)
	checked := 0
	for _, e := range exchanges {
		if e.Response.Body == "" || strings.HasSuffix(e.Response.Body, ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(protocolDir, e.Response.Body))
		if err != nil {
			t.Fatal(err)
		}
		var into any
		switch {
		case e.Response.Headers["Content-Type"] == "application/problem+json":
			var p sdk.Problem
			strictly(t, e.Response.Body, data, &p)
			if p.Status != e.Response.Status || p.Type != "urn:flagnetic:problem:"+p.Code || p.RequestId == "" {
				t.Errorf("%s: %+v, want status %d, type urn:flagnetic:problem:<code> and a request_id", e.Response.Body, p, e.Response.Status)
			}
			if want := strings.TrimSuffix(filepath.Base(e.Response.Body), ".json"); want != strconv.Itoa(p.Status)+"-"+p.Code {
				t.Errorf("%s holds %d %s, want the file named <status>-<code>", e.Response.Body, p.Status, p.Code)
			}
			checked++
			continue
		case strings.HasPrefix(e.Request.Path, "/sdk/v1/ruleset"):
			into = new(sdk.Ruleset)
		case strings.HasPrefix(e.Request.Path, "/sdk/v1/evaluate"):
			into = new(sdk.Evaluation)
		case strings.HasPrefix(e.Request.Path, "/sdk/v1/events"):
			into = new(sdk.EventBatchResult)
		default:
			t.Fatalf("%s: no contract type for %s", e.Name, e.Request.Path)
		}
		strictly(t, e.Response.Body, data, into)
		checked++
	}
	if checked < 20 {
		t.Errorf("checked %d documents, want every document the exchanges answer", checked)
	}
}

func TestEveryRequestBodyIsOfTheContract(t *testing.T) {
	var exchanges []exchange
	readJSON(t, filepath.Join(protocolDir, "server/exchanges.json"), &exchanges)
	types := map[string]func() any{
		"evaluation_summary": func() any { return new(sdk.EvaluationSummaryEvent) },
		"alias":              func() any { return new(sdk.AliasEvent) },
		"exposure":           func() any { return new(sdk.ExposureEvent) },
	}
	for _, e := range exchanges {
		// A body the server refuses on purpose (malformed, too large, the
		// wrong media type) is no request of the contract.
		if len(e.Request.Body) == 0 || slices.Contains([]int{400, 413, 415}, e.Response.Status) {
			continue
		}
		switch e.Request.Path {
		case "/sdk/v1/evaluate":
			strictly(t, e.Name, e.Request.Body, new(sdk.EvaluationRequest))
		case "/sdk/v1/events":
			var batch sdk.EventBatch
			strictly(t, e.Name, e.Request.Body, &batch)
			for i, ev := range batch.Events {
				kind, _ := ev["type"].(string)
				decoded, ok := types[kind]
				if !ok {
					continue // an event of a type the server does not know, rejected on purpose
				}
				data, err := json.Marshal(ev)
				if err != nil {
					t.Fatal(err)
				}
				strictly(t, e.Name+" event "+strconv.Itoa(i), data, decoded())
			}
		}
	}
}

func TestEveryProblemDocumentIsOfThePlatformShape(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(protocolDir, "server/problems/*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no problem documents: %v", err)
	}
	// The schema leaves a problem's code an open string, so that an SDK
	// treats a code it does not know like any other problem of its status;
	// every code a document answers must still be one the contract names, or
	// an SDK written against the contract would never see it coming.
	spec, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var p sdk.Problem
		strictly(t, filepath.Base(f), data, &p)
		// A code stands in backticks, alone or after its status (`404 not_found`).
		if !regexp.MustCompile("[` ]" + regexp.QuoteMeta(p.Code) + "`").Match(spec) {
			t.Errorf("%s answers %s, which the contract does not name", filepath.Base(f), p.Code)
		}
		if p.Errors != nil {
			for _, v := range *p.Errors {
				if !strings.HasPrefix(v.Pointer, "/") || v.Params == nil {
					t.Errorf("%s: violation %+v, want a JSON Pointer into the body and the rule's params", filepath.Base(f), v)
				}
			}
		}
	}
}
