package governance_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestAnEntryIsReadBackAsRecorded(t *testing.T) {
	c := testkit.Clock()
	ids := testkit.IDs(c)
	log := pggovernance.NewAuditLog(testkit.FreshPool(t), c, ids)
	actor := governance.Actor{Kind: governance.ActorToken, ID: "0190d2a0-0000-7000-8000-0000000000aa", Name: "deploy",
		MemberID: "0190d2a0-0000-7000-8000-0000000000bb"}
	ctx := governance.WithRequest(t.Context(), governance.Request{Actor: actor, ID: "req-7"})
	r := governance.Record{
		Project:     id.New[governance.Project](ids),
		Environment: id.New[governance.Environment](ids),
		Action:      "environment.updated",
		Entity:      governance.Entity{Kind: "environment", ID: "0190d2a0-0000-7000-8000-0000000000cc", Key: "staging"},
		Before:      map[string]any{"name": "Staging"},
		After:       map[string]any{"name": "Pre-production"},
	}
	if err := log.Record(ctx, r); err != nil {
		t.Fatal(err)
	}
	entries, err := log.Latest(t.Context(), 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Latest = %d entries, %v; want 1", len(entries), err)
	}
	e := entries[0]
	if e.Actor != actor || e.RequestID != "req-7" || e.Project != r.Project || e.Environment != r.Environment ||
		e.Action != r.Action || e.Entity != r.Entity || !e.OccurredAt.Equal(testkit.Start) {
		t.Errorf("entry = %+v, want %+v by %+v", e, r, actor)
	}
	before, _ := json.Marshal(e.Before)
	after, _ := json.Marshal(e.After)
	if string(before) != `{"name":"Staging"}` || string(after) != `{"name":"Pre-production"}` {
		t.Errorf("before %s after %s", before, after)
	}
}

func TestAnEntryOfValuesThatAreNotJSONIsRefused(t *testing.T) {
	c := testkit.Clock()
	log := pggovernance.NewAuditLog(testkit.FreshPool(t), c, testkit.IDs(c))
	ctx := governance.WithRequest(t.Context(), governance.Request{Actor: governance.Actor{Kind: governance.ActorSystem, ID: "system", Name: "system"}})
	entity := governance.Entity{Kind: "member", ID: "x", Key: "x"}
	for name, r := range map[string]governance.Record{
		"before": {Action: "member.updated", Entity: entity, Before: make(chan int)},
		"after":  {Action: "member.updated", Entity: entity, After: func() {}},
	} {
		if err := log.Record(ctx, r); err == nil {
			t.Errorf("%s: an unencodable value was recorded", name)
		}
	}
	if err := log.Record(t.Context(), governance.Record{Action: "member.updated", Entity: entity}); err == nil {
		t.Error("an entry without an actor was recorded")
	}
}
