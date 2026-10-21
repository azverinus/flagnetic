package governance_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestAnEntryIsReadBackAsRecorded(t *testing.T) {
	ids := testkit.IDs(testkit.Clock())
	log := pggovernance.NewAuditLog(testkit.FreshPool(t))
	actor := governance.Actor{Kind: governance.ActorToken, ID: "0190d2a0-0000-7000-8000-0000000000aa", Name: "deploy",
		MemberID: "0190d2a0-0000-7000-8000-0000000000bb"}
	r := governance.Record{
		Project:     id.New[workspace.Project](ids),
		Environment: id.New[workspace.Environment](ids),
		Action:      "environment.updated",
		Entity:      governance.Entity{Kind: "environment", ID: "0190d2a0-0000-7000-8000-0000000000cc", Key: "staging"},
		Before:      map[string]any{"name": "Staging"},
		After:       map[string]any{"name": "Pre-production"},
	}
	recorded, err := governance.NewEntry(id.New[governance.Entry](ids), testkit.Start, governance.Request{Actor: actor, ID: "req-7"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = log.Record(t.Context(), recorded); err != nil {
		t.Fatal(err)
	}
	entries, err := log.Latest(t.Context(), 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Latest = %d entries, %v; want 1", len(entries), err)
	}
	e := entries[0]
	if e.ID != recorded.ID || e.Actor != actor || e.RequestID != "req-7" || e.Project != r.Project || e.Environment != r.Environment ||
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
	ids := testkit.IDs(testkit.Clock())
	log := pggovernance.NewAuditLog(testkit.FreshPool(t))
	system := governance.Request{Actor: governance.Actor{Kind: governance.ActorSystem, ID: "system", Name: "system"}}
	entity := governance.Entity{Kind: "member", ID: "x", Key: "x"}
	for name, r := range map[string]governance.Record{
		"before": {Action: "member.updated", Entity: entity, Before: make(chan int)},
		"after":  {Action: "member.updated", Entity: entity, After: func() {}},
	} {
		e, err := governance.NewEntry(id.New[governance.Entry](ids), testkit.Start, system, r)
		if err != nil {
			t.Fatal(err)
		}
		if err := log.Record(t.Context(), e); err == nil {
			t.Errorf("%s: an unencodable value was recorded", name)
		}
	}
}
