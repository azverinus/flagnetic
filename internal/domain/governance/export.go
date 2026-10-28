package governance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CSVHeader is the header row of an audit download in CSV, fixed so that a
// script reading one download reads every later one.
var CSVHeader = []string{
	"id", "occurred_at", "project_id", "environment_id", "actor_kind", "actor_id", "actor_name", "actor_member_id",
	"request_id", "change_request_id", "kill_switch", "action", "entity_kind", "entity_id", "entity_key", "before", "after",
}

// CSVRow returns e as a row of CSVHeader's columns. Before and after are
// compact JSON, empty when the entry holds none, and every cell a
// spreadsheet would read as a formula is made text.
func CSVRow(e Entry) ([]string, error) {
	before, err := compactJSON(e.Before)
	if err != nil {
		return nil, fmt.Errorf("audit entry %s: before: %w", e.ID, err)
	}
	after, err := compactJSON(e.After)
	if err != nil {
		return nil, fmt.Errorf("audit entry %s: after: %w", e.ID, err)
	}
	row := []string{
		e.ID.String(), e.OccurredAt.UTC().Format(time.RFC3339Nano), idCell(e.Project),
		idCell(e.Environment), string(e.Actor.Kind), e.Actor.ID, e.Actor.Name,
		e.Actor.MemberID, e.RequestID, idCell(e.ChangeRequest),
		strconv.FormatBool(e.KillSwitch), e.Action, e.Entity.Kind, e.Entity.ID, e.Entity.Key, before, after,
	}
	for i, cell := range row {
		row[i] = NeutralizeCell(cell)
	}
	return row, nil
}

// idCell returns an identifier's text, or "" for the zero identifier.
func idCell[T any](v id.Of[T]) string {
	if v.IsZero() {
		return ""
	}
	return v.String()
}

// compactJSON returns v as JSON on one line, or "" for no value.
func compactJSON(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	raw, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return "", err
		}
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return "", err
	}
	return b.String(), nil
}

// NeutralizeCell prefixes with a quote a cell that begins with =, +, -, @, a
// tab or a carriage return, which a spreadsheet would evaluate as a formula:
// a name or a key typed as one stays text for whoever opens the download.
func NeutralizeCell(cell string) string {
	if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
		return "'" + cell
	}
	return cell
}
