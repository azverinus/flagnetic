package flagnetic

import "testing"

// The context fingerprint an exposure call is looked up by, which no
// public call reaches directly. The bounded map behind the suppression set
// and the detail lookup has its own file's test.

func TestFingerprintTellsContextsApartAndIsStable(t *testing.T) {
	one := Context{Units: map[string]string{"user": "u-1", "anonymous": "d-1"},
		Attributes: map[string]any{"platform": "linux", "app_version": "1.0.0"}}
	same := Context{Units: map[string]string{"anonymous": "d-1", "user": "u-1"},
		Attributes: map[string]any{"app_version": "1.0.0", "platform": "linux"}}
	if fingerprint(one) != fingerprint(same) {
		t.Error("the same context in another map order fingerprints differently")
	}
	others := []Context{
		{Units: map[string]string{"user": "u-2", "anonymous": "d-1"},
			Attributes: map[string]any{"platform": "linux", "app_version": "1.0.0"}},
		{Units: map[string]string{"user": "u-1", "anonymous": "d-1"},
			Attributes: map[string]any{"platform": "macos", "app_version": "1.0.0"}},
		{Units: map[string]string{"user": "u-1"}},
		{},
	}
	for _, other := range others {
		if fingerprint(one) == fingerprint(other) {
			t.Errorf("%+v fingerprints like the first context", other)
		}
	}
	// Lengths prefix every part, so a unit ID cannot be written to look
	// like another unit type and ID.
	forged := Context{Units: map[string]string{"user": "u-1", "anonymous": "d-1"}}
	honest := Context{Units: map[string]string{"anonymous": "d-1user", "u": "-1"}}
	if fingerprint(forged) == fingerprint(honest) {
		t.Error("a unit ID forged a separator")
	}
}
