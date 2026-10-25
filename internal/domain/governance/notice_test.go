package governance_test

import (
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// notice returns the notice of entry n of the entity with entityID in
// environment.
func notice(n byte, kind, entityID, environment string) governance.ChangeNotice {
	return governance.ChangeNotice{
		Entry:       id.FromBytes[governance.Entry]([16]byte{0x01, 0x90, 15: n}),
		Entity:      governance.Entity{Kind: kind, ID: entityID, Key: kind + "-" + entityID},
		Environment: environment,
	}
}

// entries returns the entry numbers of notices, in order.
func entries(notices []governance.ChangeNotice) []byte {
	out := []byte{}
	for _, n := range notices {
		out = append(out, n.Entry.Bytes()[15])
	}
	return out
}

func TestPendingNoticesKeepEachEntityOnceAtThePlaceOfItsLatestChange(t *testing.T) {
	var p governance.PendingNotices
	p.Offer(notice(1, "flag", "checkout", "production"), notice(2, "flag", "banner", "production"))
	p.Offer(notice(3, "flag", "checkout", "production"), notice(4, "flag", "checkout", "staging"))
	p.Offer(notice(5, "segment", "checkout", ""))

	got, refetch := p.Take()
	if refetch {
		t.Error("refetch, want none")
	}
	if want := []byte{2, 3, 4, 5}; !slices.Equal(entries(got), want) {
		t.Errorf("notices = %v, want %v: one per entity and environment, at its latest change", entries(got), want)
	}
	if got, refetch := p.Take(); len(got) != 0 || refetch {
		t.Errorf("second take = %v %v, want nothing: what was taken is sent", entries(got), refetch)
	}
}

func TestEntitiesWithTheSameKeyButNotTheSameIdentifierAreNotCoalesced(t *testing.T) {
	var p governance.PendingNotices
	a, b := notice(1, "change_request", "a", ""), notice(2, "change_request", "b", "")
	b.Entity.Key = a.Entity.Key // two change requests with the same title
	p.Offer(a, b)
	if got, _ := p.Take(); !slices.Equal(entries(got), []byte{1, 2}) {
		t.Errorf("notices = %v, want both", entries(got))
	}
}

func TestPendingNoticesBeyondTheLimitBecomeARefetch(t *testing.T) {
	var p governance.PendingNotices
	for i := range governance.MaxPendingNotices {
		p.Offer(notice(byte(i), "flag", "f"+string(rune('a'+i%26))+string(rune('a'+i/26)), ""))
	}
	if got, refetch := p.Take(); len(got) != governance.MaxPendingNotices || refetch {
		t.Fatalf("at the limit: %d notices, refetch %v; want them all and no refetch", len(got), refetch)
	}

	for i := range governance.MaxPendingNotices + 1 {
		p.Offer(notice(byte(i), "flag", "f"+string(rune('a'+i%26))+string(rune('a'+i/26)), ""))
	}
	got, refetch := p.Take()
	if len(got) != 0 || !refetch {
		t.Errorf("beyond the limit: %d notices, refetch %v; want a refetch alone", len(got), refetch)
	}
}

func TestNoticesAfterARefetchFollowIt(t *testing.T) {
	var p governance.PendingNotices
	p.Offer(notice(1, "flag", "checkout", ""))
	p.Refetch()
	p.Offer(notice(2, "flag", "banner", ""))
	got, refetch := p.Take()
	if !refetch || !slices.Equal(entries(got), []byte{2}) {
		t.Errorf("take = %v %v, want a refetch, then the notice offered after it", entries(got), refetch)
	}
}
