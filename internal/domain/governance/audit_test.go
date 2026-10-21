package governance_test

import (
	"context"
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

func TestAnEntryNamesTheActorAndTheRequestOfItsContext(t *testing.T) {
	actor := governance.Actor{Kind: governance.ActorCLI, ID: "cli", Name: "flagnetic admin bootstrap"}
	ctx := governance.WithRequest(context.Background(), governance.Request{Actor: actor, ID: "req-1"})
	e, err := governance.NewEntry(ctx, entryID(), now, created)
	if err != nil {
		t.Fatal(err)
	}
	if e.Actor != actor || e.RequestID != "req-1" || !e.OccurredAt.Equal(now) || e.Action != "member.created" {
		t.Errorf("entry = %+v", e)
	}
}

func TestAnEntryWithoutAnActorIsRefused(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no request":      context.Background(),
		"no actor's kind": governance.WithRequest(context.Background(), governance.Request{Actor: governance.Actor{ID: "x", Name: "x"}}),
	} {
		if _, err := governance.NewEntry(ctx, entryID(), now, created); err == nil {
			t.Errorf("%s: an entry was made", name)
		}
	}
}

func TestAnEntryNeedsAnActionAndAnEntity(t *testing.T) {
	ctx := governance.WithRequest(context.Background(), governance.Request{Actor: governance.Actor{Kind: governance.ActorSystem, ID: "system", Name: "system"}})
	for name, r := range map[string]governance.Record{
		"no action":    {Entity: created.Entity},
		"no verb":      {Action: "member", Entity: created.Entity},
		"no entity":    {Action: "member.created"},
		"no entity id": {Action: "member.created", Entity: governance.Entity{Kind: "member"}},
	} {
		if _, err := governance.NewEntry(ctx, entryID(), now, r); err == nil {
			t.Errorf("%s: an entry was made", name)
		}
	}
}
