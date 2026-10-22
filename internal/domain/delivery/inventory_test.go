package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
)

func TestSightingsOfOneRowMerge(t *testing.T) {
	key := delivery.SDKKey{Kind: delivery.SDKKeyServer}
	sdk := delivery.SDKIdentity{Name: "flagnetic-go", Version: "1.4.0", Platform: "linux"}
	at := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	first := delivery.Sight(key, sdk, "", 5, at)
	events := delivery.Sight(key, sdk, "", 0, at.Add(time.Minute))
	if first.InventoryIdentity != events.InventoryIdentity {
		t.Fatalf("a fetch and an events batch of one SDK and key are two rows: %+v %+v", first, events)
	}
	merged := events.Merge(first)
	if !merged.First.Equal(at) || !merged.At.Equal(at.Add(time.Minute)) {
		t.Errorf("merged = %v to %v, want the earliest and latest times", merged.First, merged.At)
	}
	if merged = first.Merge(events); merged.RulesetVersion != 5 {
		t.Errorf("an events batch moved the ruleset version to %d, want 5 kept", merged.RulesetVersion)
	}
	if delivery.Sight(key, sdk, "3.1.0", 0, at).InventoryIdentity == first.InventoryIdentity {
		t.Error("another app version is the same row")
	}
	if long := delivery.Sight(key, sdk, strings.Repeat("9", 300), 0, at); len(long.AppVersion) != delivery.MaxEventString {
		t.Errorf("an app version of 300 bytes is kept as %d", len(long.AppVersion))
	}
}
