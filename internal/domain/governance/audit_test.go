package governance_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var now = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func entryID() governance.EntryID {
	return id.New[governance.Entry](id.NewDeterministicSource(clock.NewFake(now)))
}

var created = governance.Record{
	Action: "member.created",
	Entity: governance.Entity{Kind: "member", ID: "0190d2a0-0000-7000-8000-000000000001", Key: "ada@example.com"},
	After:  map[string]any{"email": "ada@example.com"},
}

var system = governance.Request{Actor: governance.Actor{Kind: governance.ActorSystem, ID: "system", Name: "system"}}

func TestAnEntryNamesTheActorAndTheRequestItIsMadeIn(t *testing.T) {
	actor := governance.CLIActor("admin bootstrap")
	e, err := governance.NewEntry(entryID(), now, governance.Request{Actor: actor, ID: "req-1"}, created)
	if err != nil {
		t.Fatal(err)
	}
	if e.Actor != actor || e.RequestID != "req-1" || !e.OccurredAt.Equal(now) || e.Action != "member.created" {
		t.Errorf("entry = %+v", e)
	}
}

func TestTheCommandLineActsAsTheCLIActor(t *testing.T) {
	want := governance.Actor{Kind: governance.ActorCLI, ID: "cli", Name: "flagnetic admin bootstrap"}
	if got := governance.CLIActor("admin bootstrap"); got != want {
		t.Errorf("CLIActor = %+v, want %+v", got, want)
	}
}

func TestAnEntryWithoutAnActorIsRefused(t *testing.T) {
	for name, req := range map[string]governance.Request{
		"no actor":        {},
		"no actor's kind": {Actor: governance.Actor{ID: "x", Name: "x"}},
	} {
		if _, err := governance.NewEntry(entryID(), now, req, created); err == nil {
			t.Errorf("%s: an entry was made", name)
		}
	}
}

func TestAnEntryNeedsAnActionAndAnEntity(t *testing.T) {
	for name, r := range map[string]governance.Record{
		"no action":    {Entity: created.Entity},
		"no verb":      {Action: "member", Entity: created.Entity},
		"no entity":    {Action: "member.created"},
		"no entity id": {Action: "member.created", Entity: governance.Entity{Kind: "member"}},
	} {
		if _, err := governance.NewEntry(entryID(), now, system, r); err == nil {
			t.Errorf("%s: an entry was made", name)
		}
	}
}
