package sdk_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// beaconContentType is what navigator.sendBeacon sends a string as.
const beaconContentType = "text/plain;charset=UTF-8"

// beacon posts body to the events path with query as its query string and
// the headers given as name, value pairs, as navigator.sendBeacon does: no
// Authorization unless one is given.
func beacon(t *testing.T, srv *testkit.Server, query url.Values, contentType string, body []byte, headers ...string) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+sdk.BaseURL+"/events?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
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

func keyInQuery(value string) url.Values { return url.Values{"sdk_key": {value}} }

func TestAClosingPagesBeaconIsStoredLikeAnyBatch(t *testing.T) {
	srv, _, client := eventsServer(t)
	id := eventID(protocolTime, 1)
	body := batch(aliasEvent(id))
	a := beacon(t, srv, keyInQuery(client), beaconContentType, body, "Origin", "https://app.example.com")
	if a.status != http.StatusAccepted {
		t.Fatalf("beacon = %d: %s", a.status, a.body)
	}
	var res sdk.EventBatchResult
	if err := json.Unmarshal(a.body, &res); err != nil || res.Accepted != 1 {
		t.Fatalf("beacon answered %s, want 1 accepted", a.body)
	}
	browserHeaders(t, "beacon", a.header)
	if a.header.Get("RateLimit-Limit") != "60" || a.header.Get("RateLimit-Remaining") != "59" {
		t.Errorf("RateLimit = %s/%s, want the client events limit of 60",
			a.header.Get("RateLimit-Remaining"), a.header.Get("RateLimit-Limit"))
	}
	// The same event posted the usual way afterwards was received already.
	if again := batchResult(t, srv, client, body); again.Duplicates != 1 || again.Accepted != 0 {
		t.Errorf("the same event with the key in Authorization = %+v, want a duplicate", again)
	}

	plain := beacon(t, srv, keyInQuery(client), "text/plain", batch(aliasEvent(eventID(protocolTime, 2))))
	if plain.status != http.StatusAccepted {
		t.Errorf("text/plain without a charset = %d: %s", plain.status, plain.body)
	}
	// A text/plain body is held to the same rules as JSON.
	notJSON := beacon(t, srv, keyInQuery(client), beaconContentType, []byte("events=1"))
	refused(t, "a text/plain body that is not JSON", notJSON.status, notJSON.body, http.StatusBadRequest, "invalid_request")
	form := beacon(t, srv, keyInQuery(client), "application/x-www-form-urlencoded", body)
	refused(t, "a form", form.status, form.body, http.StatusUnsupportedMediaType, "unsupported_media_type")
}

func TestABeaconIsRefusedForTheKeyItCarriesOrTheOriginItComesFrom(t *testing.T) {
	srv, p, client := eventsServer(t)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, listing := p.clientKeyWithOrigins(t, "https://app.example.com")
	body := batch(aliasEvent(eventID(protocolTime, 1)))

	a := beacon(t, srv, keyInQuery(server), beaconContentType, body)
	refused(t, "a server key in the query", a.status, a.body, http.StatusForbidden, "server_key_in_query")

	a = beacon(t, srv, keyInQuery(listing), beaconContentType, body, "Origin", "https://elsewhere.example.com")
	refused(t, "an unlisted origin", a.status, a.body, http.StatusForbidden, "origin_not_allowed")
	a = beacon(t, srv, keyInQuery(listing), beaconContentType, body, "Origin", "https://app.example.com")
	if a.status != http.StatusAccepted || a.header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("a listed origin = %d, Allow-Origin %q: %s", a.status, a.header.Get("Access-Control-Allow-Origin"), a.body)
	}

	a = beacon(t, srv, keyInQuery(client), beaconContentType, body, "Authorization", "Bearer "+client)
	refused(t, "a key both in Authorization and in the query", a.status, a.body, http.StatusBadRequest, "invalid_request")

	a = beacon(t, srv, url.Values{}, beaconContentType, body)
	refused(t, "no key", a.status, a.body, http.StatusUnauthorized, "sdk_key_missing")
}

func TestTheProtocolsBeaconExchangeIsAnsweredAsTheDocumentSays(t *testing.T) {
	srv, _, client := eventsServer(t)
	ex := protocolExchanges(t)["events from a closing page"]
	path, query, _ := strings.Cut(ex.Request.Path, "?")
	if ex.Request.Method != http.MethodPost || path != sdk.BaseURL+"/events" || query != "sdk_key=<client key>" ||
		ex.Request.Headers["Authorization"] != "" {
		t.Fatalf("exchange = %+v, want the client key in the query only", ex.Request)
	}
	var headers []string
	for h, v := range ex.Request.Headers {
		if h != "Content-Type" {
			headers = append(headers, h, v)
		}
	}
	a := beacon(t, srv, keyInQuery(client), ex.Request.Headers["Content-Type"], ex.Request.Body, headers...)
	if a.status != ex.Response.Status {
		t.Fatalf("status = %d, want %d: %s", a.status, ex.Response.Status, a.body)
	}
	for h, v := range ex.Response.Headers {
		// The documents count down one limit across every event exchange;
		// this test posts only this one.
		if h == "RateLimit-Remaining" {
			continue
		}
		if got := a.header.Get(h); got != v {
			t.Errorf("%s = %q, want %q", h, got, v)
		}
	}
	var got, want any
	readJSON(t, filepath.Join(protocolDir, ex.Response.Body), &want)
	if err := json.Unmarshal(a.body, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %s, want %v", a.body, want)
	}
}
