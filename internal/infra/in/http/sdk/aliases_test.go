package sdk_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// aliasOf is an alias numbered n, occurred at at, linking fromID of
// fromType to toID of toType.
func aliasOf(n int, at time.Time, fromType, fromID, toType, toID string) map[string]any {
	return map[string]any{
		"type": "alias", "event_id": eventID(protocolTime, n), "occurred_at": at.Format(time.RFC3339),
		"from_unit_type": fromType, "from_unit_id": fromID, "to_unit_type": toType, "to_unit_id": toID,
	}
}

// aliasLinks reads the alias links of environment.
func aliasLinks(t *testing.T, p project, environment string) []events.AliasLink {
	t.Helper()
	links, err := pgevents.NewStore(testkit.Pool(t)).AliasLinks(t.Context(), p.environmentID(t, environment))
	if err != nil {
		t.Fatal(err)
	}
	return links
}

func TestAliasesAreCheckedAgainstTheDeclaredUnitTypes(t *testing.T) {
	srv, p, value := eventsServer(t)
	res := batchResult(t, srv, value, batch(
		aliasOf(1, protocolTime, "device", "d-1", "user", "u-1"),
		aliasOf(2, protocolTime, "anonymous", "d-1", "device", "u-1"),
		aliasOf(3, protocolTime, "anonymous", "d-1", "anonymous", "d-2"),
		// The structure is judged before the declared unit types: one
		// undeclared type on both sides is still the same type twice.
		aliasOf(4, protocolTime, "ghost", "d-1", "ghost", "d-2"),
		aliasOf(5, protocolTime, "anonymous", "d-1", "user", "u-1"),
	))
	if res.Accepted != 1 || len(res.Rejected) != 4 {
		t.Fatalf("aliases = %+v, want the last accepted and the others refused", res)
	}
	for i, want := range []string{"undeclared_unit_type", "undeclared_unit_type", "event_invalid", "event_invalid"} {
		if r := res.Rejected[i]; r.Index != i || r.Code != want {
			t.Errorf("rejection %d = %+v, want index %d refused %s", i, r, i, want)
		}
	}
	for _, r := range res.Rejected[2:] {
		if r.Detail != "from_unit_type and to_unit_type must differ" {
			t.Errorf("detail of rejection %d = %q", r.Index, r.Detail)
		}
	}
	links := aliasLinks(t, p, "production")
	if len(links) != 1 || links[0].Alias.FromUnitID != "d-1" || links[0].Alias.ToUnitID != "u-1" {
		t.Errorf("links = %+v, want only the accepted alias's", links)
	}
}

func TestAnAliasLinksItsPairOnceKeepingTheEarliestTime(t *testing.T) {
	srv, p, value := eventsServer(t)
	_, staging := p.sdkKey(t, "staging", management.SdkKeyKindClient)
	earliest := protocolTime.Add(-3 * time.Hour)

	// The pair arrives three times under different event IDs, as an app
	// signing the same user in again would send it: later first, then two
	// in one batch, the earliest among them.
	if res := batchResult(t, srv, value, batch(aliasOf(1, protocolTime.Add(-time.Hour), "anonymous", "d-1", "user", "u-1"))); res.Accepted != 1 {
		t.Fatalf("first alias = %+v", res)
	}
	res := batchResult(t, srv, value, batch(
		aliasOf(2, protocolTime.Add(-2*time.Hour), "anonymous", "d-1", "user", "u-1"),
		aliasOf(3, earliest, "anonymous", "d-1", "user", "u-1"),
		aliasOf(4, protocolTime, "anonymous", "d-1", "user", "u-2"),
	))
	if res.Accepted != 3 {
		t.Fatalf("second batch = %+v", res)
	}
	// A later alias of the pair moves nothing.
	if res := batchResult(t, srv, value, batch(aliasOf(5, protocolTime, "anonymous", "d-1", "user", "u-1"))); res.Accepted != 1 {
		t.Fatalf("later alias = %+v", res)
	}
	// Another environment links the pair for itself.
	if res := batchResult(t, srv, staging, batch(aliasOf(6, protocolTime, "anonymous", "d-1", "user", "u-1"))); res.Accepted != 1 {
		t.Fatalf("staging alias = %+v", res)
	}

	aliases, err := pgevents.NewStore(testkit.Pool(t)).Aliases(t.Context(), p.environmentID(t, "production"))
	if err != nil || len(aliases) != 5 {
		t.Fatalf("raw aliases = %d, %v, want every one of the 5 stored", len(aliases), err)
	}
	links := aliasLinks(t, p, "production")
	if len(links) != 2 {
		t.Fatalf("links = %+v, want one per pair", links)
	}
	byTo := map[string]events.AliasLink{}
	for _, l := range links {
		byTo[l.Alias.ToUnitID] = l
	}
	if l := byTo["u-1"]; !l.Alias.OccurredAt.Equal(earliest) || l.EventID.String() != eventID(protocolTime, 3) ||
		l.Alias.FromUnitType != "anonymous" || l.Alias.FromUnitID != "d-1" || l.Alias.ToUnitType != "user" {
		t.Errorf("link of d-1 and u-1 = %+v, want the earliest alias's", l)
	}
	if l := byTo["u-2"]; !l.Alias.OccurredAt.Equal(protocolTime) {
		t.Errorf("link of d-1 and u-2 = %+v", l)
	}
	if links := aliasLinks(t, p, "staging"); len(links) != 1 || !links[0].Alias.OccurredAt.Equal(protocolTime) {
		t.Errorf("staging links = %+v, want its own", links)
	}
}
