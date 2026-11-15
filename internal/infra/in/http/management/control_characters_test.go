package management_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestATextHoldingNULIsRefusedWithItsPointer(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	res := do(admin.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{
		Key: "checkout", Type: management.FlagTypeString, Name: "Check\x00out", Description: ptr("fine\nwith a line break"),
		Variations: variations("a", `"x\u0000y"`, "b", `"z"`),
	}))
	got := violationsOf(t, "creating a flag with NUL", res.StatusCode(), res.Body, "invalid_character")
	want := []string{"/name invalid_character", "/variations/0/value invalid_character"}
	if !slices.Equal(got, want) {
		t.Errorf("violations = %q, want %q", got, want)
	}
}

func TestAJSONBodyHoldingNULIsRefusedWhateverMediaTypeItClaims(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	token := testkit.InstanceAdminToken(t)
	for _, media := range []string{"text/plain", ""} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+management.BaseURL+"/projects/"+p.Key+"/flags",
			strings.NewReader(`{"key":"checkout","type":"boolean","name":"Check\u0000out"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		if media != "" {
			req.Header.Set("Content-Type", media)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got := violationsOf(t, "a body sent as "+media, resp.StatusCode, body, "invalid_character")
		if want := []string{"/name invalid_character"}; !slices.Equal(got, want) {
			t.Errorf("%q: violations = %q, want %q", media, got, want)
		}
	}
}

func TestATextWithLineBreaksAndTabsIsKept(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := createFlag(t, admin, p.Key, management.FlagCreate{
		Key: "checkout", Type: management.FlagTypeBoolean, Name: "Checkout", Description: ptr("one\n\ttwo\r\nthree"),
	})
	if f.Description != "one\n\ttwo\r\nthree" {
		t.Errorf("description = %v", f.Description)
	}
}

func TestSearchTextAndCursorsHoldingNULAreBadRequests(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Type: management.FlagTypeBoolean, Name: "Checkout"})
	forged := func(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }
	token := testkit.InstanceAdminToken(t)
	for name, query := range map[string]string{
		"a search text":          "q=a%00b",
		"a tag":                  "tag=a%00b",
		"a name cursor with NUL": "sort=name&cursor=" + forged(`{"s":"name","k":"a","n":"b\u0000"}`),
		"a key cursor with NUL":  "cursor=" + forged(`{"s":"key","k":"a\u0000"}`),
	} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+management.BaseURL+"/projects/"+p.Key+"/flags?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assertProblem(t, name, resp.StatusCode, body, http.StatusBadRequest, "invalid_request")
	}
}

func TestExperimentAndChangeRequestCursorsHoldingNULAreBadRequests(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	cursor := base64.RawURLEncoding.EncodeToString([]byte("a\x00b"))
	token := testkit.InstanceAdminToken(t)
	for _, path := range []string{"experiments", "change-requests"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+management.BaseURL+"/projects/"+p.Key+"/"+path+"?cursor="+cursor, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assertProblem(t, path, resp.StatusCode, body, http.StatusBadRequest, "invalid_request")
	}
}

// TestNoOperationAcceptsAControlCharacterInABody posts a body with NUL to
// every operation that is not a read, as an instance admin signed in by a
// session, who is authorized for all of them: each is refused as
// invalid_character before its handler runs, so no text a request can reach
// is left to PostgreSQL.
func TestNoOperationAcceptsAControlCharacterInABody(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	m := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := signedIn(t, srv, m)
	checked := 0
	for pattern, op := range operations.Table {
		method, path, _ := strings.Cut(pattern, " ")
		if method == http.MethodGet || method == http.MethodHead {
			continue
		}
		var segments []string
		for _, s := range strings.Split(path, "/") {
			switch {
			case s == "{project}":
				s = p.Key
			case strings.HasPrefix(s, "{"):
				s = "x"
			}
			segments = append(segments, s)
		}
		res, body := b.Do(t, method, management.BaseURL+strings.Join(segments, "/"), "application/json", `{"name":"a\u0000b"}`)
		checked++
		var pr struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(body), &pr)
		if res.StatusCode != http.StatusUnprocessableEntity || pr.Code != "invalid_character" {
			t.Errorf("%s (%s) = %d %s, want 422 invalid_character", pattern, op.Name, res.StatusCode, body)
		}
	}
	if checked < 50 {
		t.Errorf("checked %d operations, want every write", checked)
	}
}
