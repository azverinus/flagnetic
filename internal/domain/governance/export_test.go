package governance_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestNeutralizeCellPrefixesWhatASpreadsheetReadsAsAFormula(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"plain":             "plain",
		"a=b":               "a=b",
		"=SUM(A1)":          "'=SUM(A1)",
		"+1":                "'+1",
		"-1":                "'-1",
		"@cmd":              "'@cmd",
		"\tindented":        "'\tindented",
		"\rreturn":          "'\rreturn",
		`=HYPERLINK("x")`:   `'=HYPERLINK("x")`,
		"'=already a quote": "'=already a quote",
	} {
		if got := governance.NeutralizeCell(in); got != want {
			t.Errorf("NeutralizeCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestACSVRowHoldsTheEntryInTheHeaderColumnsOrder(t *testing.T) {
	project := id.New[workspace.Project](id.NewDeterministicSource(clock.NewFake(now)))
	e, err := governance.NewEntry(entryID(), now, governance.Request{
		Actor: governance.Actor{Kind: governance.ActorToken, ID: "tok-1", Name: "=pipeline", MemberID: "m-1"}, ID: "req-9",
	}, governance.Record{
		Project: project, Action: "flag.kill_switch",
		Entity:     governance.Entity{Kind: "flag", ID: "f-1", Key: "-checkout"},
		Before:     json.RawMessage("{\n  \"on\": false\n}"),
		After:      map[string]any{"on": true},
		KillSwitch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := governance.CSVRow(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(row) != len(governance.CSVHeader) {
		t.Fatalf("row has %d cells for %d columns", len(row), len(governance.CSVHeader))
	}
	cell := func(column string) string { return row[slices.Index(governance.CSVHeader, column)] }
	for column, want := range map[string]string{
		"id": e.ID.String(), "occurred_at": "2026-03-01T12:00:00Z", "project_id": project.String(), "environment_id": "",
		"actor_kind": "token", "actor_id": "tok-1", "actor_name": "'=pipeline", "actor_member_id": "m-1", "request_id": "req-9",
		"change_request_id": "", "kill_switch": "true", "action": "flag.kill_switch", "entity_kind": "flag", "entity_id": "f-1",
		"entity_key": "'-checkout", "before": `{"on":false}`, "after": `{"on":true}`,
	} {
		if got := cell(column); got != want {
			t.Errorf("%s = %q, want %q", column, got, want)
		}
	}
}

func TestACSVRowRefusesABeforeOrAfterThatIsNotJSON(t *testing.T) {
	for name, set := range map[string]func(*governance.Entry){
		"before not encodable": func(e *governance.Entry) { e.Before = make(chan int) },
		"after not encodable":  func(e *governance.Entry) { e.After = make(chan int) },
		"before malformed":     func(e *governance.Entry) { e.Before = json.RawMessage("{") },
		"after malformed":      func(e *governance.Entry) { e.After = json.RawMessage("{") },
	} {
		t.Run(name, func(t *testing.T) {
			e, err := governance.NewEntry(entryID(), now, system, governance.Record{Action: "member.created", Entity: created.Entity})
			if err != nil {
				t.Fatal(err)
			}
			set(&e)
			if row, err := governance.CSVRow(e); err == nil {
				t.Errorf("CSVRow = %q, want an error", row)
			}
		})
	}
}

func TestACSVRowOfAnEntryWithoutValuesHasEmptyBeforeAndAfter(t *testing.T) {
	e, err := governance.NewEntry(entryID(), now, system, governance.Record{Action: "member.created", Entity: created.Entity})
	if err != nil {
		t.Fatal(err)
	}
	row, err := governance.CSVRow(e)
	if err != nil {
		t.Fatal(err)
	}
	if row[slices.Index(governance.CSVHeader, "before")] != "" || row[slices.Index(governance.CSVHeader, "after")] != "" {
		t.Errorf("row = %q, want empty before and after", row)
	}
}
