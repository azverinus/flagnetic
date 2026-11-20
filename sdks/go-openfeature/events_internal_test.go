package openfeature

import (
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// next returns the event type queued, or "" when none is.
func next(p *Provider) of.EventType {
	select {
	case e := <-p.events:
		return e.EventType
	default:
		return ""
	}
}

// A key_revoked log event fires PROVIDER_ERROR once Init has returned: it
// is the one case an ordinary stage last_good does not distinguish from a
// revoked key. White-box, because the wrapped client never exposes why a
// fetch failed beyond the stage it left it in.
func TestObserveFiresProviderErrorOnKeyRevoked(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 4), initialized: true}
	p.observe(0, flagnetic.EventKeyRevoked, map[string]any{"status": 401})
	if got := next(p); got != of.ProviderError {
		t.Errorf("event = %q, want PROVIDER_ERROR", got)
	}
}

// Every other log event is observed (so the provider's logger still logs)
// but emits nothing.
func TestObserveIgnoresOtherLogEvents(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 4), initialized: true}
	p.observe(0, flagnetic.EventFetchFailed, map[string]any{"cause": "network"})
	if got := next(p); got != "" {
		t.Errorf("unexpected event %q", got)
	}
}

// Before Init has returned the OpenFeature SDK sets the status itself:
// stage moves and a revoked key send nothing, though the key is
// remembered.
func TestNothingIsSentBeforeInitReturns(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 4)}
	p.observe(0, flagnetic.EventKeyRevoked, nil)
	p.onStage(flagnetic.StageLastGood)
	p.onStage(flagnetic.StageFresh)
	if got := next(p); got != "" {
		t.Errorf("unexpected event %q before Init returned", got)
	}
	if p.revoked {
		t.Error("a move to fresh before Init returned left the key marked refused")
	}
}

// READY on every move to fresh, STALE on entering last_good; a refused key
// keeps the status ERROR, so no STALE while it is refused, and the move to
// fresh clears it.
func TestStageMovesFollowTheStatusRules(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 8), initialized: true}

	p.onStage(flagnetic.StageLastGood)
	if got := next(p); got != of.ProviderStale {
		t.Errorf("entering last_good sent %q, want PROVIDER_STALE", got)
	}
	p.onStage(flagnetic.StageFresh)
	if got := next(p); got != of.ProviderReady {
		t.Errorf("entering fresh sent %q, want PROVIDER_READY", got)
	}

	p.observe(0, flagnetic.EventKeyRevoked, nil)
	if got := next(p); got != of.ProviderError {
		t.Fatalf("a refused key sent %q, want PROVIDER_ERROR", got)
	}
	p.onStage(flagnetic.StageLastGood)
	if got := next(p); got != "" {
		t.Errorf("last_good while the key is refused sent %q, want nothing", got)
	}
	p.onStage(flagnetic.StageFresh)
	if got := next(p); got != of.ProviderReady {
		t.Errorf("recovery sent %q, want PROVIDER_READY", got)
	}
	p.onStage(flagnetic.StageLastGood)
	if got := next(p); got != of.ProviderStale {
		t.Errorf("last_good after recovery sent %q, want PROVIDER_STALE", got)
	}
}

// emit never sends once closed is set, whatever the channel's state: a
// closed provider must never panic sending on a channel it does not close
// itself (see Provider.emit's doc).
func TestEmitStopsAfterClose(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 1)}
	p.closed.Store(true)
	p.emit(of.Event{EventType: of.ProviderStale})
	select {
	case e := <-p.events:
		t.Fatalf("emit sent after close: %+v", e)
	case <-time.After(10 * time.Millisecond):
	}
}
