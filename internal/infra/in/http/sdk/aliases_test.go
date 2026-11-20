package sdk_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
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

// linkedPairs returns which of pairs the events store reports linked in
// environment, in the order given: what the store answers an ingest asking
// whether a pair is new.
func linkedPairs(t *testing.T, p project, environment string, pairs ...events.AliasPair) []events.AliasPair {
	t.Helper()
	linked, err := pgevents.NewStore(testkit.Pool(t)).LinkedPairs(t.Context(), p.environmentID(t, environment), pairs)
	if err != nil {
		t.Fatal(err)
	}
	var out []events.AliasPair
	for _, pair := range pairs {
		if linked[pair] {
			out = append(out, pair)
		}
	}
	return out
}

// pair is the pair of units an alias of anonymous fromID to user toID links.
func pair(fromID, toID string) events.AliasPair {
	return events.AliasPair{FromUnitType: "anonymous", FromUnitID: fromID, ToUnitType: "user", ToUnitID: toID}
}

func TestAClientKeyIsToldNoMoreOfAnUndeclaredUnitTypeThanThatTheAliasIsInvalid(t *testing.T) {
	srv, _, client := eventsServer(t)
	res := batchResult(t, srv, client, batch(
		aliasOf(1, protocolTime, "device", "d-1", "user", "u-1"),
		aliasOf(2, protocolTime, "anonymous", "d-1", "device", "u-1"),
		aliasOf(3, protocolTime, "anonymous", "d-1", "anonymous", "d-2"),
	))
	if res.Accepted != 0 || len(res.Rejected) != 3 {
		t.Fatalf("aliases = %+v, want all refused", res)
	}
	for _, r := range res.Rejected[:2] {
		if r.Code != "event_invalid" || r.Detail != "the alias is not valid" {
			t.Errorf("rejection %d = %+v, want event_invalid saying nothing of the unit type", r.Index, r)
		}
	}
}

func TestAliasesAreCheckedAgainstTheDeclaredUnitTypes(t *testing.T) {
	srv, p, _ := eventsServer(t)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
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
	links := linkedPairs(t, p, "production", pair("d-1", "u-1"), events.AliasPair{FromUnitType: "anonymous", FromUnitID: "d-1", ToUnitType: "device", ToUnitID: "u-1"},
		events.AliasPair{FromUnitType: "anonymous", FromUnitID: "d-1", ToUnitType: "anonymous", ToUnitID: "d-2"})
	if len(links) != 1 || links[0] != pair("d-1", "u-1") {
		t.Errorf("links = %+v, want only the accepted alias's", links)
	}
}

func TestAnAliasLinksItsPairOnce(t *testing.T) {
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

	aliases := testkit.ExportedAliases(t, testkit.Pool(t), p.projectID(t), p.environmentID(t, "production"))
	if len(aliases) != 5 {
		t.Fatalf("raw aliases = %d, want every one of the 5 stored", len(aliases))
	}
	pairs := []events.AliasPair{pair("d-1", "u-1"), pair("d-1", "u-2")}
	if links := linkedPairs(t, p, "production", pairs...); len(links) != 2 {
		t.Fatalf("links = %+v, want one per pair", links)
	}
	if links := linkedPairs(t, p, "staging", pairs...); len(links) != 1 || links[0] != pair("d-1", "u-1") {
		t.Errorf("staging links = %+v, want its own", links)
	}
}

func TestAnEnvironmentAtItsLimitOfAliasLinksRefusesNewPairsOnly(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(clock.NewFake(protocolTime)), testkit.WithEventLimits(2, 0))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
	_, staging := p.sdkKey(t, "staging", management.SdkKeyKindClient)

	if res := batchResult(t, srv, value, batch(
		aliasOf(1, protocolTime, "anonymous", "d-1", "user", "u-1"),
		aliasOf(2, protocolTime, "anonymous", "d-2", "user", "u-2"),
	)); res.Accepted != 2 {
		t.Fatalf("aliases up to the limit = %+v", res)
	}
	// The limit is reached: a pair already linked is still taken, and a new
	// one is refused, in the same batch.
	res := batchResult(t, srv, value, batch(
		aliasOf(3, protocolTime.Add(-time.Hour), "anonymous", "d-1", "user", "u-1"),
		aliasOf(4, protocolTime, "anonymous", "d-3", "user", "u-3"),
	))
	if res.Accepted != 1 || len(res.Rejected) != 1 || res.Rejected[0].Index != 1 || res.Rejected[0].Code != "quota_exceeded" {
		t.Fatalf("aliases at the limit = %+v, want the new pair refused quota_exceeded", res)
	}
	if links := linkedPairs(t, p, "production", pair("d-1", "u-1"), pair("d-2", "u-2"), pair("d-3", "u-3")); len(links) != 2 || links[0] != pair("d-1", "u-1") || links[1] != pair("d-2", "u-2") {
		t.Fatalf("links = %+v, want the two within the limit", links)
	}
	// A refused alias's ID is not claimed: it is refused again, not
	// counted as received.
	if again := batchResult(t, srv, value, batch(aliasOf(4, protocolTime, "anonymous", "d-3", "user", "u-3"))); again.Duplicates != 0 ||
		len(again.Rejected) != 1 {
		t.Errorf("refused alias sent again = %+v", again)
	}
	// Another environment counts its own links.
	if res := batchResult(t, srv, staging, batch(aliasOf(5, protocolTime, "anonymous", "d-3", "user", "u-3"))); res.Accepted != 1 {
		t.Errorf("staging alias = %+v, want accepted", res)
	}
}

func TestABatchOfNewPairsTakesOnlyWhatTheLimitLeaves(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(clock.NewFake(protocolTime)), testkit.WithEventLimits(2, 0))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)

	// Three new pairs, one of them twice, against room for two: the pair
	// sent twice takes one place, and the third pair is refused.
	res := batchResult(t, srv, value, batch(
		aliasOf(1, protocolTime, "anonymous", "d-1", "user", "u-1"),
		aliasOf(2, protocolTime, "anonymous", "d-2", "user", "u-2"),
		aliasOf(3, protocolTime.Add(-time.Hour), "anonymous", "d-1", "user", "u-1"),
		aliasOf(4, protocolTime, "anonymous", "d-3", "user", "u-3"),
	))
	if res.Accepted != 3 || len(res.Rejected) != 1 || res.Rejected[0].Index != 3 || res.Rejected[0].Code != "quota_exceeded" {
		t.Fatalf("batch past the limit = %+v, want the third pair refused quota_exceeded", res)
	}
	if links := linkedPairs(t, p, "production", pair("d-1", "u-1"), pair("d-2", "u-2"), pair("d-3", "u-3")); len(links) != 2 {
		t.Errorf("links = %+v, want the two within the limit", links)
	}
}
