package sdk_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// An SDK released later may send a member or a query parameter this server
// does not know; it must be answered as if it had not, so that new SDKs keep
// working against every server that serves this API.

func TestUnknownMembersOfAnEvaluateBodyAreIgnored(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.targeted(t)
	_, client := p.sdkKey(t, "production", management.SdkKeyKindClient)

	_, plain := evaluation(t, srv, client, targetedContext)
	_, extended := evaluation(t, srv, client,
		`{"context":{"units":{"user":"u-1"},"attributes":{"os":"android","country":"nz"},"device":{"model":"x1"},"labels":["a"]},"include_experiments":true,"hints":{"max_age":30}}`)
	if !bytes.Equal(extended.body, plain.body) {
		t.Errorf("with unknown members = %s\nwithout = %s", extended.body, plain.body)
	}
}

func TestUnknownMembersOfAnEventsBatchAreIgnoredAtEveryLevel(t *testing.T) {
	srv, _, value := eventsServer(t)
	alias := aliasEvent(eventID(protocolTime, 101))
	alias["session_id"] = "s-1"
	summary := summaryEvent(eventID(protocolTime, 102),
		map[string]any{"flag": "checkout", "variation": "true", "reason": "rule", "count": 3, "sampled": false},
		// A served error is counted by an SDK as any other reason.
		map[string]any{"flag": "banner_text", "variation": nil, "reason": "error", "count": 1},
	)
	summary["sampling_rate"] = 1
	body := batch(alias, summary)
	body = bytes.Replace(body, []byte(`"schema_version":1`), []byte(`"schema_version":1,"compression":"none","session":{"id":"s-1"}`), 1)
	body = bytes.Replace(body, []byte(`"name":"flagnetic-dart"`), []byte(`"name":"flagnetic-dart","build":"42"`), 1)

	res := batchResult(t, srv, value, body)
	if res.Accepted != 2 || res.Duplicates != 0 || res.DroppedRows != 0 || len(res.Rejected) != 0 {
		t.Errorf("batch = %+v, want both events accepted, no row dropped", res)
	}
}

// withQuery adds the query parameter name=value to a request.
func withQuery(name, value string) sdk.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		q := r.URL.Query()
		q.Set(name, value)
		r.URL.RawQuery = q.Encode()
		return nil
	}
}

func TestAnUnknownQueryParameterIsIgnoredOnEveryOperation(t *testing.T) {
	srv, p, client := eventsServer(t)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	unknown := withQuery("fields", "all")

	plain := ruleset(t, srv, server)
	extended := ruleset(t, srv, server, unknown)
	if extended.StatusCode() != http.StatusOK || !bytes.Equal(extended.Body, plain.Body) {
		t.Errorf("ruleset with an unknown parameter = %d: %s", extended.StatusCode(), extended.Body)
	}

	evaluated := post(t, srv.URL+sdk.BaseURL+"/evaluate", client, []byte(targetedContext))
	if evaluated.status != http.StatusOK {
		t.Errorf("evaluate without an unknown parameter = %d: %s", evaluated.status, evaluated.body)
	}
	if got := post(t, srv.URL+sdk.BaseURL+"/evaluate?fields=all", client, []byte(targetedContext)); got.status != http.StatusOK || !bytes.Equal(got.body, evaluated.body) {
		t.Errorf("evaluate with an unknown parameter = %d: %s", got.status, got.body)
	}
	if got := post(t, srv.URL+sdk.BaseURL+"/events?fields=all", client, batch(aliasEvent(eventID(protocolTime, 201)))); got.status != http.StatusAccepted {
		t.Errorf("events with an unknown parameter = %d: %s", got.status, got.body)
	}

	openStream(t, srv, nil, bearer(server), unknown).started(t)
	openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &client}, unknown).started(t)
}

// post posts body as JSON to url with the key value.
func post(t *testing.T, url, value string, body []byte) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+value)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, _ := io.ReadAll(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: got}
}
