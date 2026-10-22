package sdk_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// inventory lists the SDK inventory of environment as client.
func inventory(t *testing.T, client *management.ClientWithResponses, p project, environment string) []management.SdkInventoryItem {
	t.Helper()
	res := do(client.ListSdkInventoryWithResponse(t.Context(), p.key, environment))
	if res.JSON200 == nil {
		t.Fatalf("GET …/environments/%s/sdk-inventory = %d: %s", environment, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func valueOf[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func TestEveryKindOfSDKRequestIsInTheInventory(t *testing.T) {
	start := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	p.flag(t, "checkout")
	serverKey, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	clientKey, client := p.sdkKey(t, "production", management.SdkKeyKindClient)
	fetched := ruleset(t, srv, server, header("User-Agent", "flagnetic-go/1.4.0 (linux)"))
	if fetched.StatusCode() != http.StatusOK {
		t.Fatalf("ruleset = %d", fetched.StatusCode())
	}
	version, err := strconv.ParseInt(strings.Trim(fetched.HTTPResponse.Header.Get("ETag"), `"`), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	s := openStream(t, srv, nil, bearer(client), header("User-Agent", "flagnetic-js/1.0.0 (web)"))
	s.started(t)
	c.Advance(time.Second)
	evaluationWith(t, srv, client, `{"context":{"units":{"user":"u-1"},"attributes":{"app_version":"3.1.0"}}}`,
		"User-Agent", "flagnetic-swift/2.0.0 (ios)")
	c.Advance(time.Second)
	if a := postEvents(t, srv, server, batch(), "User-Agent", "curl/8.4.0"); a.status != http.StatusAccepted {
		t.Fatalf("events = %d: %s", a.status, a.body)
	}
	if got := inventory(t, p.admin, p, "production"); len(got) != 0 {
		t.Errorf("before the round = %+v, want nothing written yet", got)
	}
	if err := srv.Tick(t, worker.SDKInventoryName); err != nil {
		t.Fatal(err)
	}

	got := inventory(t, p.admin, p, "production")
	type row struct {
		name, version, platform, app, key string
		seen                              time.Time
		ruleset                           int64
	}
	want := []row{
		{"unknown", "", "", "", serverKey.KeyId, start.Add(3 * time.Second), 0},
		{"flagnetic-swift", "2.0.0", "ios", "3.1.0", clientKey.KeyId, start.Add(2 * time.Second), version},
		{"flagnetic-js", "1.0.0", "web", "", clientKey.KeyId, start.Add(time.Second), version},
		{"flagnetic-go", "1.4.0", "linux", "", serverKey.KeyId, start, version},
	}
	if len(got) != len(want) {
		t.Fatalf("inventory = %+v, want one row per request kind", got)
	}
	for i, w := range want {
		g := got[i]
		r := row{g.SdkName, g.SdkVersion, valueOf(g.Platform), valueOf(g.AppVersion), g.SdkKey.KeyId, g.LastSeen.UTC(), valueOf(g.LastRulesetVersion)}
		if r != w || !g.FirstSeen.Equal(g.LastSeen) {
			t.Errorf("row %d = %+v, want %+v", i, r, w)
		}
	}
	if got[0].SdkKey.Kind != management.SdkKeyKindServer || got[0].SdkKey.Name != "checkout" || got[0].Platform != nil || got[0].LastRulesetVersion != nil {
		t.Errorf("events row = %+v, want the server key, null platform and ruleset version", got[0])
	}
	if other := inventory(t, p.admin, p, "staging"); len(other) != 0 {
		t.Errorf("staging = %+v, want empty", other)
	}
	res := do(p.admin.ListSdkInventoryWithResponse(t.Context(), p.key, "nowhere"))
	refused(t, "an unknown environment", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	viewer := testkit.Client(t, srv, testkit.GrantedToken(t, testkit.Pool(t), p.projectID(t), access.RoleViewer))
	if rows := inventory(t, viewer, p, "production"); len(rows) != 4 {
		t.Errorf("a viewer reads %d rows, want 4", len(rows))
	}
}

func TestAnSDKSeenAgainKeepsItsRowAndMovesItsLastSeen(t *testing.T) {
	start := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	p.flag(t, "checkout")
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	var last string
	fetch := func() {
		res := ruleset(t, srv, server, header("User-Agent", "flagnetic-go/1.4.0 (linux)"))
		if res.StatusCode() != http.StatusOK {
			t.Fatalf("ruleset = %d", res.StatusCode())
		}
		last = strings.Trim(res.HTTPResponse.Header.Get("ETag"), `"`)
	}
	fetch()
	c.Advance(10 * time.Second)
	fetch()
	if err := srv.Tick(t, worker.SDKInventoryName); err != nil {
		t.Fatal(err)
	}
	c.Advance(2 * time.Minute)
	p.kill(t, "checkout", "production")
	fetch()
	if err := srv.Tick(t, worker.SDKInventoryName); err != nil {
		t.Fatal(err)
	}
	got := inventory(t, p.admin, p, "production")
	if len(got) != 1 {
		t.Fatalf("inventory = %+v, want one row", got)
	}
	if !got[0].FirstSeen.Equal(start) || !got[0].LastSeen.Equal(start.Add(2*time.Minute+10*time.Second)) ||
		strconv.FormatInt(valueOf(got[0].LastRulesetVersion), 10) != last {
		t.Errorf("row = first %v, last %v, ruleset %v", got[0].FirstSeen, got[0].LastSeen, valueOf(got[0].LastRulesetVersion))
	}
}

func TestTheInventoryForgetsSDKsNotSeenForTheRetention(t *testing.T) {
	start := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	ruleset(t, srv, server, header("User-Agent", "flagnetic-go/1.4.0 (linux)"))
	c.Advance(30 * 24 * time.Hour)
	ruleset(t, srv, server, header("User-Agent", "flagnetic-go/1.5.0 (linux)"))
	if err := srv.Tick(t, worker.SDKInventoryName); err != nil {
		t.Fatal(err)
	}
	c.Advance(61 * 24 * time.Hour)
	if err := srv.Tick(t, worker.SDKInventoryRetentionName); err != nil {
		t.Fatal(err)
	}
	got := inventory(t, p.admin, p, "production")
	if len(got) != 1 || got[0].SdkVersion != "1.5.0" {
		t.Errorf("91 and 61 days after = %+v, want only 1.5.0 kept", got)
	}
}

func TestAKeyHoldsAtMost200InventoryRows(t *testing.T) {
	start := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	_, server := p.sdkKey(t, "production", management.SdkKeyKindServer)
	otherKey, other := p.sdkKey(t, "production", management.SdkKeyKindServer)
	fetch := func(value, version string) {
		t.Helper()
		if res := ruleset(t, srv, value, header("User-Agent", "flagnetic-go/"+version+" (linux)")); res.StatusCode() != http.StatusOK {
			t.Fatalf("ruleset = %d", res.StatusCode())
		}
	}
	version := func(n int) string { return fmt.Sprintf("1.0.%03d", n) }
	for n := range 201 {
		fetch(server, version(n))
	}
	fetch(other, version(0))
	if err := srv.Tick(t, worker.SDKInventoryName); err != nil {
		t.Fatal(err)
	}
	rows := func() map[string][]string {
		out := map[string][]string{}
		for _, r := range inventory(t, p.admin, p, "production") {
			out[r.SdkKey.KeyId] = append(out[r.SdkKey.KeyId], r.SdkVersion)
		}
		return out
	}
	got := rows()
	if len(got[otherKey.KeyId]) != 1 || len(got) != 2 {
		t.Fatalf("keys = %d, other key's rows = %v, want each key bounded on its own", len(got), got[otherKey.KeyId])
	}
	var full []string
	for k, versions := range got {
		if k != otherKey.KeyId {
			full = versions
		}
	}
	if len(full) != 200 || slices.Contains(full, version(200)) {
		t.Errorf("the key's rows = %d, holding %s: %t; want the first 200 in order", len(full), version(200), slices.Contains(full, version(200)))
	}
	if n := testkit.Metric(t, srv.Scrape(t), `flagnetic_sdk_inventory_dropped_total{reason="key_full"}`); n != 1 {
		t.Errorf("key_full drops = %v, want 1", n)
	}
	warnings := srv.Logs.Find("SDK inventory sightings dropped")
	if len(warnings) != 1 || warnings[0].Level != slog.LevelWarn || warnings[0].Attrs["dropped"] != int64(1) || warnings[0].Attrs["key_full"] != int64(1) {
		t.Fatalf("warnings = %+v, want one counting the sighting beyond the key's 200 rows", warnings)
	}

	c.Advance(2 * time.Minute)
	fetch(server, version(5))
	fetch(server, version(201))
	if err := srv.Tick(t, worker.SDKInventoryName); err != nil {
		t.Fatal(err)
	}
	for _, r := range inventory(t, p.admin, p, "production") {
		if r.SdkVersion == version(201) {
			t.Errorf("a new row of a full key was recorded: %+v", r)
		}
		if r.SdkVersion == version(5) && !r.LastSeen.Equal(start.Add(2*time.Minute)) {
			t.Errorf("a row the full key holds was not updated: last seen %v", r.LastSeen)
		}
	}
	if n := testkit.Metric(t, srv.Scrape(t), `flagnetic_sdk_inventory_dropped_total{reason="key_full"}`); n != 2 {
		t.Errorf("key_full drops = %v, want 2", n)
	}
}
