package sinks_test

import (
	"errors"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *apperrors.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not an application error", err)
	}
	return e.Code
}

func TestCheckReplayRefusesARangeThatEndsBeforeItStartsAndAStartBeforeRetention(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	retention := 30 * 24 * time.Hour
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	tests := []struct {
		name string
		from time.Time
		to   *time.Time
		code string
	}{
		{"open ended", now.Add(-time.Hour), nil, ""},
		{"bounded", now.Add(-2 * time.Hour), at(-time.Hour), ""},
		{"a single instant", now.Add(-time.Hour), at(-time.Hour), ""},
		{"to before from", now.Add(-time.Hour), at(-2 * time.Hour), "invalid_replay_range"},
		{"from at the edge of retention", now.Add(-retention), nil, ""},
		{"from before retention", now.Add(-retention - time.Second), nil, "out_of_retention"},
		{"the range is judged before retention", now.Add(-retention - time.Hour), at(-retention - 2*time.Hour), "invalid_replay_range"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sinks.CheckReplay(tc.from, tc.to, now, retention)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("CheckReplay() = %v, want nil", err)
				}
				return
			}
			if got := codeOf(t, err); got != tc.code {
				t.Fatalf("CheckReplay() code = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestSelectKindsDefaultsToWhatTheSinkExportsAndRefusesWhatItDoesNot(t *testing.T) {
	exported := []sinks.EventKind{sinks.EventExposure, sinks.EventAlias}

	got, err := sinks.SelectKinds(nil, exported)
	if err != nil || len(got) != 2 || got[0] != sinks.EventExposure || got[1] != sinks.EventAlias {
		t.Fatalf("SelectKinds(none) = %v, %v; want the sink's kinds", got, err)
	}
	got, err = sinks.SelectKinds([]sinks.EventKind{sinks.EventAlias, sinks.EventAlias}, exported)
	if err != nil || len(got) != 1 || got[0] != sinks.EventAlias {
		t.Fatalf("SelectKinds(alias twice) = %v, %v; want alias once", got, err)
	}

	_, err = sinks.SelectKinds([]sinks.EventKind{sinks.EventAlias, sinks.EventEvaluationSummary, "nope"}, exported)
	if code := codeOf(t, err); code != "invalid_sink" {
		t.Fatalf("SelectKinds(unexported) code = %q, want invalid_sink", code)
	}
	var e *apperrors.Error
	_ = errors.As(err, &e)
	if len(e.Violations) != 2 || e.Violations[0].Pointer != "/event_kinds/1" || e.Violations[1].Pointer != "/event_kinds/2" {
		t.Fatalf("violations = %+v, want pointers /event_kinds/1 and /event_kinds/2", e.Violations)
	}
}

func TestPlanReplay(t *testing.T) {
	c := func(seq int64) events.Cursor { return events.Cursor{XID: 10, Seq: seq} }
	live := c(100)
	tests := []struct {
		name  string
		in    sinks.ReplayAsk
		want  sinks.ReplayPlan
		moves bool
	}{
		{
			name:  "an open ended replay goes back and runs on into the live stream",
			in:    sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true},
			want:  sinks.ReplayPlan{Cursor: c(40)},
			moves: true,
		},
		{
			name:  "a bounded replay stops at its bound and resumes from the live position",
			in:    sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true, Stop: c(60), StopFound: true},
			want:  sinks.ReplayPlan{Cursor: c(40), Stop: ptr(c(60)), Resume: ptr(live)},
			moves: true,
		},
		{
			name:  "a bound at the live position is an open ended replay",
			in:    sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true, Stop: live, StopFound: true},
			want:  sinks.ReplayPlan{Cursor: c(40)},
			moves: true,
		},
		{
			name:  "a bound past the live position is an open ended replay",
			in:    sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true, Stop: c(150), StopFound: true},
			want:  sinks.ReplayPlan{Cursor: c(40)},
			moves: true,
		},
		{
			name: "no row at or after from leaves the stream alone",
			in:   sinks.ReplayAsk{Live: live},
		},
		{
			name: "a bound resolving to no row leaves the stream alone",
			in:   sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true},
		},
		{
			name: "a bound before the first row replayed leaves the stream alone",
			in:   sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true, Stop: c(40), StopFound: true},
		},
		{
			name: "a start at the live position has nothing to replay that live delivery will not send",
			in:   sinks.ReplayAsk{Live: live, Start: live, StartFound: true},
		},
		{
			name: "a start past the live position is not skipped to",
			in:   sinks.ReplayAsk{Live: live, Start: c(120), StartFound: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, moves := sinks.PlanReplay(tc.in)
			if moves != tc.moves {
				t.Fatalf("PlanReplay() moves = %v, want %v", moves, tc.moves)
			}
			if !moves {
				return
			}
			if got.Cursor != tc.want.Cursor || !samePtr(got.Stop, tc.want.Stop) || !samePtr(got.Resume, tc.want.Resume) {
				t.Fatalf("PlanReplay() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPlanReplayOrdersPositionsByTransactionNumberNotByItsText(t *testing.T) {
	// "9" sorts after "10" as text.
	live := events.Cursor{XID: 10, Seq: 5}
	ask := sinks.ReplayAsk{Live: live, Start: events.Cursor{XID: 9, Seq: 1}, StartFound: true,
		Bounded: true, Stop: events.Cursor{XID: 9, Seq: 7}, StopFound: true}
	got, moves := sinks.PlanReplay(ask)
	if !moves || got.Stop == nil || *got.Stop != ask.Stop || *got.Resume != live {
		t.Fatalf("PlanReplay() = %+v, %v; want a bounded replay of transaction 9", got, moves)
	}
}

func TestAStreamsLivePositionIsWhereItResumesWhileABoundedReplayRuns(t *testing.T) {
	cursor, resume := events.Cursor{XID: 1, Seq: 2}, events.Cursor{XID: 5, Seq: 6}
	if got := (sinks.Stream{Cursor: cursor}).Live(); got != cursor {
		t.Fatalf("Live() = %v, want the cursor", got)
	}
	if got := (sinks.Stream{Cursor: cursor, Stop: &cursor, Resume: &resume}).Live(); got != resume {
		t.Fatalf("Live() = %v, want the resume position", got)
	}
}

func samePtr(a, b *events.Cursor) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestAReplayLowersTheFloorOnlyWhenItRunsOnIntoTheLiveStreamFromEarlier(t *testing.T) {
	c := func(seq int64) events.Cursor { return events.Cursor{XID: 10, Seq: seq} }
	live := c(100)
	floor := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	earlier, later := floor.Add(-time.Hour), floor.Add(time.Hour)
	tests := []struct {
		name string
		in   sinks.ReplayAsk
		want *time.Time
	}{
		{
			name: "an open ended replay from before the floor lowers it to its start",
			in:   sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, From: earlier, Floor: &floor},
			want: &earlier,
		},
		{
			name: "an open ended replay from after the floor keeps it",
			in:   sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, From: later, Floor: &floor},
			want: &floor,
		},
		{
			name: "a stream with no floor gets none",
			in:   sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, From: earlier},
		},
		{
			name: "a bound past the live position runs on, and lowers it",
			in: sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true, Stop: c(150), StopFound: true,
				From: earlier, Floor: &floor},
			want: &earlier,
		},
		{
			name: "a bounded replay keeps it for the live stream it returns to",
			in: sinks.ReplayAsk{Live: live, Start: c(40), StartFound: true, Bounded: true, Stop: c(60), StopFound: true,
				From: earlier, Floor: &floor},
			want: &floor,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, moves := sinks.PlanReplay(tc.in)
			if !moves {
				t.Fatal("PlanReplay() does not move the stream")
			}
			if (got.Floor == nil) != (tc.want == nil) || (got.Floor != nil && !got.Floor.Equal(*tc.want)) {
				t.Fatalf("PlanReplay() floor = %v, want %v", got.Floor, tc.want)
			}
		})
	}
}

func TestAStreamReadsWithItsFloorExceptWhileABoundedReplayRuns(t *testing.T) {
	floor := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	cursor, resume := events.Cursor{XID: 1, Seq: 2}, events.Cursor{XID: 5, Seq: 6}
	scope := sinks.Scope{}
	live := sinks.Stream{Cursor: cursor, Floor: &floor}
	if got := live.ReadScope(scope).Floor; got == nil || !got.Equal(floor) {
		t.Errorf("ReadScope() floor = %v, want the stream's", got)
	}
	replaying := sinks.Stream{Cursor: cursor, Stop: &cursor, Resume: &resume, Floor: &floor}
	if got := replaying.ReadScope(scope).Floor; got != nil {
		t.Errorf("ReadScope() floor = %v while a bounded replay runs, want none", got)
	}
	if got := replaying.LiveScope(scope).Floor; got == nil || !got.Equal(floor) {
		t.Errorf("LiveScope() floor = %v while a bounded replay runs, want the stream's", got)
	}
}
