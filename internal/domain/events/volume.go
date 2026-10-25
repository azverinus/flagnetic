package events

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// StreamAliases counts the aliases received per day. Links are written as
// aliases arrive, so counting them is all this stream does.
const StreamAliases Stream = "aliases"

// AliasRow is a stored alias as the alias stream reads it.
type AliasRow struct {
	Project    workspace.ProjectID
	ReceivedAt time.Time
}

// CountAliasEvents returns the aliases rows hold per project and UTC day of
// receipt.
func CountAliasEvents(rows []AliasRow) map[EventDay]int64 {
	out := map[EventDay]int64{}
	for _, r := range rows {
		out[EventDay{Project: r.Project, Day: day(r.ReceivedAt), Type: EventAlias}]++
	}
	return out
}

// VolumeDay is how many events of one type a project received on one UTC
// day.
type VolumeDay struct {
	Day    time.Time
	Type   EventType
	Events int64
}

// EventVolume is what a project received per day over a range of days, and
// the gaps retention left in that range.
type EventVolume struct {
	Days []VolumeDay
	Gaps []Gap
}

// VolumeDays returns counts as days, by day then type, leaving out a day
// without events of a type.
func VolumeDays(counts map[EventDay]int64) []VolumeDay {
	out := make([]VolumeDay, 0, len(counts))
	for k, n := range counts {
		if n > 0 {
			out = append(out, VolumeDay{Day: k.Day, Type: k.Type, Events: n})
		}
	}
	slices.SortFunc(out, func(a, b VolumeDay) int {
		return cmp.Or(a.Day.Compare(b.Day), strings.Compare(string(a.Type), string(b.Type)))
	})
	return out
}
