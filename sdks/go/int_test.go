package flagnetic_test

import (
	"context"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// numbers is a ruleset whose number flags serve whole, fractional and
// out-of-range values, beside a boolean flag.
const numbers = `{
  "schema_version": 1, "version": 3, "scope": "server",
  "flags": [
    {"key": "retries", "type": "number", "on": true,
     "variations": [{"key": "v", "value": 42}], "off_variation": "v", "fallthrough": "v"},
    {"key": "negative", "type": "number", "on": true,
     "variations": [{"key": "v", "value": -7}], "off_variation": "v", "fallthrough": "v"},
    {"key": "smallest", "type": "number", "on": true,
     "variations": [{"key": "v", "value": -9223372036854775808}], "off_variation": "v", "fallthrough": "v"},
    {"key": "ratio", "type": "number", "on": true,
     "variations": [{"key": "v", "value": 2.5}], "off_variation": "v", "fallthrough": "v"},
    {"key": "too_large", "type": "number", "on": true,
     "variations": [{"key": "v", "value": 9223372036854775808}], "off_variation": "v", "fallthrough": "v"},
    {"key": "enabled", "type": "boolean", "on": true,
     "variations": [{"key": "v", "value": true}], "off_variation": "v", "fallthrough": "v"}
  ]
}`

func TestIntServesWholeNumbers(t *testing.T) {
	c := offline(t, []byte(numbers))
	for key, want := range map[string]int64{"retries": 42, "negative": -7, "smallest": -1 << 63} {
		if got := c.Int(t.Context(), flagnetic.Context{}, key, 0); got != want {
			t.Errorf("Int(%s) = %d, want %d", key, got, want)
		}
	}
	if got := c.Recorder().Summary(); !hasCount(got, "retries", flagnetic.ReasonFallthrough) {
		t.Errorf("summary %+v does not count retries as fallthrough", got)
	}
}

// TestIntRefusesWhatItWouldTruncate: a fractional number, or one outside
// the int64 range, serves the default with a type mismatch instead of a
// silently truncated or wrapped value, and the mismatch is logged once.
func TestIntRefusesWhatItWouldTruncate(t *testing.T) {
	var l logs
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(numbers)), flagnetic.WithLogger(l.logger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	for _, key := range []string{"ratio", "too_large", "enabled", "absent"} {
		for range 2 {
			if got := c.Int(t.Context(), flagnetic.Context{}, key, 5); got != 5 {
				t.Errorf("Int(%s) = %d, want the default 5", key, got)
			}
		}
	}
	summary := c.Recorder().Summary()
	for _, key := range []string{"ratio", "too_large", "enabled", "absent"} {
		if !hasCount(summary, key, flagnetic.ReasonFallback) {
			t.Errorf("summary %+v does not count %s as fallback", summary, key)
		}
	}
	if got := len(l.named(flagnetic.EventTypeMismatch)); got != 3 {
		t.Errorf("type_mismatch logged %d times, want once for each of ratio, too_large and enabled", got)
	}
}

// TestIntUsesLocalOverrides: a local override of a number flag is served
// through Int when it is whole, and refused like a served value when not.
func TestIntUsesLocalOverrides(t *testing.T) {
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(numbers)), flagnetic.WithLocalOverrides())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if err := c.LocalOverrides().Set("retries", 3.0); err != nil {
		t.Fatal(err)
	}
	if got := c.Int(t.Context(), flagnetic.Context{}, "retries", 0); got != 3 {
		t.Errorf("Int(retries) with a local override of 3 = %d", got)
	}
	if err := c.LocalOverrides().Set("retries", 3.5); err != nil {
		t.Fatal(err)
	}
	if got := c.Int(t.Context(), flagnetic.Context{}, "retries", 9); got != 9 {
		t.Errorf("Int(retries) with a local override of 3.5 = %d, want the default 9", got)
	}
}

// TestDetailWithAnInt64DefaultReadsLikeInt: an int64 default makes Detail
// apply Int's rule, so a caller that needs the reason, such as an
// OpenFeature provider, gets the same answer as Int.
func TestDetailWithAnInt64DefaultReadsLikeInt(t *testing.T) {
	c := offline(t, []byte(numbers))
	if d := c.Detail(t.Context(), flagnetic.Context{}, "retries", int64(0)); d.Value != int64(42) || d.Reason != flagnetic.ReasonFallthrough {
		t.Errorf("Detail(retries, int64) = %+v, want int64 42 with fallthrough", d)
	}
	d := c.Detail(t.Context(), flagnetic.Context{}, "ratio", int64(5))
	if d.Value != int64(5) || d.Reason != flagnetic.ReasonFallback || d.ErrorCode != flagnetic.ErrorTypeMismatch {
		t.Errorf("Detail(ratio, int64) = %+v, want the default with type_mismatch", d)
	}
	if d := c.Detail(t.Context(), flagnetic.Context{}, "retries", 0.0); d.Value != 42.0 {
		t.Errorf("Detail(retries, float64) = %+v, want float64 42", d)
	}
}

func TestIntOnANilClientServesTheDefault(t *testing.T) {
	var c *flagnetic.Client
	if got := c.Int(t.Context(), flagnetic.Context{}, "retries", 11); got != 11 {
		t.Errorf("Int on a nil client = %d", got)
	}
}

func hasCount(summary []flagnetic.SummaryCount, flag string, reason flagnetic.Reason) bool {
	for _, s := range summary {
		if s.Flag == flag && s.Reason == reason && s.Count > 0 {
			return true
		}
	}
	return false
}
