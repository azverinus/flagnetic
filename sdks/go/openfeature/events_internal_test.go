package openfeature

import (
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// A key_revoked log event fires PROVIDER_ERROR: it is the one case an
// ordinary stage last_good does not distinguish from a revoked key.
// White-box, because the wrapped client
// never exposes why a fetch failed beyond the stage it left it in.
func TestObserveFiresProviderErrorOnKeyRevoked(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 1)}
	p.observe(0, flagnetic.EventKeyRevoked, map[string]any{"status": 401})

	select {
	case e := <-p.events:
		if e.EventType != of.ProviderError {
			t.Errorf("EventType = %q, want PROVIDER_ERROR", e.EventType)
		}
		if e.ProviderName != providerName {
			t.Errorf("ProviderName = %q, want %q", e.ProviderName, providerName)
		}
	default:
		t.Fatal("no event emitted")
	}
}

// Every other log event is observed (so the provider's logger still logs)
// but emits nothing.
func TestObserveIgnoresOtherLogEvents(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 1)}
	p.observe(0, flagnetic.EventFetchFailed, map[string]any{"cause": "network"})

	select {
	case e := <-p.events:
		t.Errorf("unexpected event: %+v", e)
	default:
	}
}

// Entering stage last_good fires STALE; a client that stays fresh, or one
// that moves between two other stages, does not: ready is Init's job, not
// onStage's.
func TestOnStageFiresStaleOnlyEnteringLastGood(t *testing.T) {
	p := &Provider{events: make(chan of.Event, 1)}
	p.onStage(flagnetic.StageFresh)
	select {
	case e := <-p.events:
		t.Fatalf("unexpected event on fresh: %+v", e)
	default:
	}

	p.onStage(flagnetic.StageLastGood)
	select {
	case e := <-p.events:
		if e.EventType != of.ProviderStale {
			t.Errorf("EventType = %q, want PROVIDER_STALE", e.EventType)
		}
	default:
		t.Fatal("no event emitted entering last_good")
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
