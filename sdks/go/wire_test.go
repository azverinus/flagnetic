package flagnetic_test

import (
	"testing"
	"time"
)

// The events envelope as the events endpoint receives it, shared by every
// test that reads what the SDK sent.

// wireBatch is an ingest batch as the events endpoint receives it.
type wireBatch struct {
	SchemaVersion int `json:"schema_version"`
	SDK           struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Platform string `json:"platform"`
	} `json:"sdk"`
	SentAt time.Time   `json:"sent_at"`
	Events []wireEvent `json:"events"`
}

// wireEvent is an evaluation summary event of a batch.
type wireEvent struct {
	Type           string      `json:"type"`
	EventID        string      `json:"event_id"`
	IntervalStart  time.Time   `json:"interval_start"`
	IntervalEnd    time.Time   `json:"interval_end"`
	RulesetVersion int64       `json:"ruleset_version"`
	AppVersion     *string     `json:"app_version"`
	Counts         []wireCount `json:"counts"`
}

// wireCount is one row of an evaluation summary.
type wireCount struct {
	Flag      string  `json:"flag"`
	Variation *string `json:"variation"`
	Reason    string  `json:"reason"`
	Count     int64   `json:"count"`
}

// row is a wireCount in the form a test compares, the variation's absence
// spelled out.
type row struct {
	flag, variation, reason string
	count                   int64
}

func rowsOf(e wireEvent) []row {
	out := make([]row, 0, len(e.Counts))
	for _, c := range e.Counts {
		variation := "«none»"
		if c.Variation != nil {
			variation = *c.Variation
		}
		out = append(out, row{c.Flag, variation, c.Reason, c.Count})
	}
	return out
}

// assertRows compares two row sets, order aside.
func assertRows(t *testing.T, got, want []row) {
	t.Helper()
	index := func(rows []row) map[row]bool {
		m := map[row]bool{}
		for _, r := range rows {
			m[r] = true
		}
		return m
	}
	g, w := index(got), index(want)
	for r := range w {
		if !g[r] {
			t.Errorf("row %+v missing", r)
		}
	}
	for r := range g {
		if !w[r] {
			t.Errorf("row %+v unexpected", r)
		}
	}
}
