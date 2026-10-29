package sinks_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

func TestASinkIsDisabledBeforeItIsAnythingElse(t *testing.T) {
	stopped := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		sink sinks.Sink
		want sinks.Health
	}{
		"a new sink":                       {sinks.Sink{}, sinks.HealthDisabled},
		"a disabled sink that was stopped": {sinks.Sink{StoppedAt: &stopped}, sinks.HealthDisabled},
		"an enabled sink":                  {sinks.Sink{Input: sinks.Input{Enabled: true}}, sinks.HealthHealthy},
		"a stopped sink":                   {sinks.Sink{Input: sinks.Input{Enabled: true}, StoppedAt: &stopped}, sinks.HealthStopped},
	} {
		if got := tc.sink.Health(); got != tc.want {
			t.Errorf("%s: health = %s, want %s", name, got, tc.want)
		}
	}
}
