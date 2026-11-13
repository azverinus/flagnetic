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
		if got := tc.sink.Health(15 * time.Minute); got != tc.want {
			t.Errorf("%s: health = %s, want %s", name, got, tc.want)
		}
	}
}

func TestAnEnabledSinkIsJudgedFromItsStreamsDeadLettersAndLag(t *testing.T) {
	over, under := 16*time.Minute, 14*time.Minute
	enabled := func(change func(*sinks.Sink)) sinks.Sink {
		s := sinks.Sink{Input: sinks.Input{Enabled: true}}
		change(&s)
		return s
	}
	failing := []sinks.Stream{{EventKind: sinks.EventExposure}, {EventKind: sinks.EventAlias, ConsecutiveFailures: 1}}
	for name, tc := range map[string]struct {
		sink sinks.Sink
		want sinks.Health
	}{
		"a stream failing": {enabled(func(s *sinks.Sink) { s.Streams = failing }), sinks.HealthFailing},
		"failing before degraded and lagging": {
			enabled(func(s *sinks.Sink) { s.Streams, s.DeadLetters, s.Lag = failing, 1, &over }), sinks.HealthFailing,
		},
		"a dead letter waiting":       {enabled(func(s *sinks.Sink) { s.DeadLetters, s.Lag = 1, &over }), sinks.HealthDegraded},
		"lag over the warning":        {enabled(func(s *sinks.Sink) { s.Lag = &over }), sinks.HealthLagging},
		"lag under the warning":       {enabled(func(s *sinks.Sink) { s.Lag = &under }), sinks.HealthHealthy},
		"lag at the warning":          {enabled(func(s *sinks.Sink) { s.Lag = ptr(15 * time.Minute) }), sinks.HealthHealthy},
		"a disabled sink that failed": {sinks.Sink{Streams: failing}, sinks.HealthDisabled},
		"streams that all delivered":  {enabled(func(s *sinks.Sink) { s.Streams = failing[:1] }), sinks.HealthHealthy},
	} {
		if got := tc.sink.Health(15 * time.Minute); got != tc.want {
			t.Errorf("%s: health = %s, want %s", name, got, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestBackoffDoublesFromASecondToFiveMinutesWithinItsSpread(t *testing.T) {
	for failures, want := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 5: 16 * time.Second, 9: 256 * time.Second,
		10: 5 * time.Minute, 11: 5 * time.Minute, 500: 5 * time.Minute,
	} {
		for name, tc := range map[string]struct {
			random float64
			factor float64
		}{"lowest": {0, 0.8}, "middle": {0.5, 1}, "highest": {0.999999, 1.2}} {
			got := sinks.Backoff(failures, func() float64 { return tc.random })
			low, high := time.Duration(float64(want)*tc.factor*0.999), time.Duration(float64(want)*tc.factor*1.001)
			if high > 5*time.Minute {
				low, high = min(low, 5*time.Minute), 5*time.Minute
			}
			if got < low || got > high {
				t.Errorf("failures %d, %s jitter: backoff = %s, want %s to %s", failures, name, got, low, high)
			}
		}
	}
	if got := sinks.Backoff(0, func() float64 { return 0.5 }); got != time.Second {
		t.Errorf("backoff before any failure = %s, want the first second", got)
	}
}

func TestASinkIsAtRiskOnceItsOldestUndeliveredRowIsWithinTheLossWarningOfRetentionsEdge(t *testing.T) {
	const retention, warning = 30 * 24 * time.Hour, 72 * time.Hour
	edge := retention - warning
	for name, tc := range map[string]struct {
		lag       time.Duration
		retention time.Duration
		warning   time.Duration
		want      bool
	}{
		"far from the edge":                  {lag: 24 * time.Hour, retention: retention, warning: warning, want: false},
		"a second short of the warning":      {lag: edge - time.Second, retention: retention, warning: warning, want: false},
		"exactly at the warning":             {lag: edge, retention: retention, warning: warning, want: true},
		"past the warning":                   {lag: edge + time.Hour, retention: retention, warning: warning, want: true},
		"a warning as long as retention":     {lag: time.Second, retention: 48 * time.Hour, warning: 48 * time.Hour, want: true},
		"a warning longer than retention":    {lag: 0, retention: 24 * time.Hour, warning: 72 * time.Hour, want: true},
		"no warning means only past the end": {lag: retention - time.Second, retention: retention, warning: 0, want: false},
	} {
		if got := sinks.AtRisk(tc.lag, tc.retention, tc.warning); got != tc.want {
			t.Errorf("%s: AtRisk(%s, %s, %s) = %v, want %v", name, tc.lag, tc.retention, tc.warning, got, tc.want)
		}
	}
}
