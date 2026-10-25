package events_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
)

func TestTheAliasStreamCountsAliasesPerProjectAndDayOfReceipt(t *testing.T) {
	late := day1.Add(23*time.Hour + 59*time.Minute)
	got := events.CountAliasEvents([]events.AliasRow{
		{Project: rollupProject, ReceivedAt: day1}, {Project: rollupProject, ReceivedAt: late},
		{Project: rollupProject, ReceivedAt: late.Add(time.Minute)},
	})
	want := map[events.EventDay]int64{
		{Project: rollupProject, Day: day1, Type: events.EventAlias}:                  2,
		{Project: rollupProject, Day: day1.AddDate(0, 0, 1), Type: events.EventAlias}: 1,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestTheVolumeListsDaysThenTypesAndLeavesEmptyOnesOut(t *testing.T) {
	day2 := day1.AddDate(0, 0, 1)
	got := events.VolumeDays(map[events.EventDay]int64{
		{Project: rollupProject, Day: day2, Type: events.EventAlias}:             1,
		{Project: rollupProject, Day: day1, Type: events.EventExposure}:          4,
		{Project: rollupProject, Day: day1, Type: events.EventEvaluationSummary}: 2,
		{Project: rollupProject, Day: day1, Type: events.EventAlias}:             0,
	})
	want := []events.VolumeDay{
		{Day: day1, Type: events.EventEvaluationSummary, Events: 2},
		{Day: day1, Type: events.EventExposure, Events: 4},
		{Day: day2, Type: events.EventAlias, Events: 1},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("days = %v, want %v", got, want)
	}
}
