package sinks

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // a name-based UUID is defined over SHA-1; nothing secret rests on it
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SchemaVersion is the version of the export schema every row carries. Within
// a version columns are only appended, so a table or a bucket built for it
// survives upgrades; a change that is not additive is a new version exported
// beside this one.
const SchemaVersion = 1

// SampleCount is how many rows of each event kind a sample sends.
const SampleCount = 10

// Row is one row of the export schema: an event as every sink kind delivers
// it, flat, with the keys of its project and environment in place of the
// platform's own IDs, which are never exported.
type Row interface {
	// ID is the row's event ID, or a summary row's report ID, which the rows
	// of one SDK report share: what a delivery ID is computed over.
	ID() string
	// wire is the row as the schema lays it out, in the order of its columns.
	wire() any
}

// ExposureRow is an exposure. The text members are empty, never null, when
// the SDK sent nothing.
type ExposureRow struct {
	EventID          string
	ProjectKey       string
	EnvironmentKey   string
	ExperimentKey    string
	FlagKey          string
	VariationKey     string
	UnitType         string
	UnitID           string
	EvaluationReason string
	Override         bool
	Platform         string
	AppVersion       string
	SDKName          string
	SDKVersion       string
	RulesetVersion   int64
	OccurredAt       time.Time
	ReceivedAt       time.Time
}

// ID is the exposure's event ID.
func (r ExposureRow) ID() string { return r.EventID }

type exposureWire struct {
	SchemaVersion    int    `json:"schema_version"`
	EventID          string `json:"event_id"`
	ProjectKey       string `json:"project_key"`
	EnvironmentKey   string `json:"environment_key"`
	ExperimentKey    string `json:"experiment_key"`
	FlagKey          string `json:"flag_key"`
	VariationKey     string `json:"variation_key"`
	UnitType         string `json:"unit_type"`
	UnitID           string `json:"unit_id"`
	EvaluationReason string `json:"evaluation_reason"`
	Override         bool   `json:"override"`
	Platform         string `json:"platform"`
	AppVersion       string `json:"app_version"`
	SDKName          string `json:"sdk_name"`
	SDKVersion       string `json:"sdk_version"`
	RulesetVersion   int64  `json:"ruleset_version"`
	OccurredAt       int64  `json:"occurred_at"`
	ReceivedAt       int64  `json:"received_at"`
}

func (r ExposureRow) wire() any {
	return exposureWire{
		SchemaVersion: SchemaVersion, EventID: r.EventID, ProjectKey: r.ProjectKey, EnvironmentKey: r.EnvironmentKey,
		ExperimentKey: r.ExperimentKey, FlagKey: r.FlagKey, VariationKey: r.VariationKey, UnitType: r.UnitType,
		UnitID: r.UnitID, EvaluationReason: r.EvaluationReason, Override: r.Override, Platform: r.Platform,
		AppVersion: r.AppVersion, SDKName: r.SDKName, SDKVersion: r.SDKVersion, RulesetVersion: r.RulesetVersion,
		OccurredAt: r.OccurredAt.UnixMilli(), ReceivedAt: r.ReceivedAt.UnixMilli(),
	}
}

// AliasRow is an alias.
type AliasRow struct {
	EventID        string
	ProjectKey     string
	EnvironmentKey string
	FromUnitType   string
	FromUnitID     string
	ToUnitType     string
	ToUnitID       string
	Platform       string
	SDKName        string
	SDKVersion     string
	OccurredAt     time.Time
	ReceivedAt     time.Time
}

// ID is the alias's event ID.
func (r AliasRow) ID() string { return r.EventID }

type aliasWire struct {
	SchemaVersion  int    `json:"schema_version"`
	EventID        string `json:"event_id"`
	ProjectKey     string `json:"project_key"`
	EnvironmentKey string `json:"environment_key"`
	FromUnitType   string `json:"from_unit_type"`
	FromUnitID     string `json:"from_unit_id"`
	ToUnitType     string `json:"to_unit_type"`
	ToUnitID       string `json:"to_unit_id"`
	Platform       string `json:"platform"`
	SDKName        string `json:"sdk_name"`
	SDKVersion     string `json:"sdk_version"`
	OccurredAt     int64  `json:"occurred_at"`
	ReceivedAt     int64  `json:"received_at"`
}

func (r AliasRow) wire() any {
	return aliasWire{
		SchemaVersion: SchemaVersion, EventID: r.EventID, ProjectKey: r.ProjectKey, EnvironmentKey: r.EnvironmentKey,
		FromUnitType: r.FromUnitType, FromUnitID: r.FromUnitID, ToUnitType: r.ToUnitType, ToUnitID: r.ToUnitID,
		Platform: r.Platform, SDKName: r.SDKName, SDKVersion: r.SDKVersion,
		OccurredAt: r.OccurredAt.UnixMilli(), ReceivedAt: r.ReceivedAt.UnixMilli(),
	}
}

// SummaryRow is one flag, variation and reason of an evaluation summary.
type SummaryRow struct {
	// ReportID is the summary's event ID when an SDK reported it, and for an
	// evaluation the server answered itself, which has none, the ID ReportID
	// derives from the row's position.
	ReportID         string
	ProjectKey       string
	EnvironmentKey   string
	FlagKey          string
	VariationKey     string
	EvaluationReason string
	Count            int64
	WindowStart      time.Time
	WindowEnd        time.Time
	SDKName          string
	SDKVersion       string
	Platform         string
	AppVersion       string
	RulesetVersion   int64
	ReceivedAt       time.Time
	// Source says who counted: sdk, or remote_evaluation for the server.
	Source string
}

// ID is the row's report ID.
func (r SummaryRow) ID() string { return r.ReportID }

type summaryWire struct {
	SchemaVersion    int    `json:"schema_version"`
	ReportID         string `json:"report_id"`
	ProjectKey       string `json:"project_key"`
	EnvironmentKey   string `json:"environment_key"`
	FlagKey          string `json:"flag_key"`
	VariationKey     string `json:"variation_key"`
	EvaluationReason string `json:"evaluation_reason"`
	Count            int64  `json:"count"`
	WindowStart      int64  `json:"window_start"`
	WindowEnd        int64  `json:"window_end"`
	SDKName          string `json:"sdk_name"`
	SDKVersion       string `json:"sdk_version"`
	Platform         string `json:"platform"`
	AppVersion       string `json:"app_version"`
	RulesetVersion   int64  `json:"ruleset_version"`
	ReceivedAt       int64  `json:"received_at"`
	Source           string `json:"source"`
}

func (r SummaryRow) wire() any {
	return summaryWire{
		SchemaVersion: SchemaVersion, ReportID: r.ReportID, ProjectKey: r.ProjectKey, EnvironmentKey: r.EnvironmentKey,
		FlagKey: r.FlagKey, VariationKey: r.VariationKey, EvaluationReason: r.EvaluationReason, Count: r.Count,
		WindowStart: r.WindowStart.UnixMilli(), WindowEnd: r.WindowEnd.UnixMilli(), SDKName: r.SDKName,
		SDKVersion: r.SDKVersion, Platform: r.Platform, AppVersion: r.AppVersion, RulesetVersion: r.RulesetVersion,
		ReceivedAt: r.ReceivedAt.UnixMilli(), Source: r.Source,
	}
}

// reportNamespace is the namespace of the report IDs of rows that have no
// event ID, the UUID 7d4f8c1e-2a6b-4e5d-9c3f-0b1a8e6d5c42. It is part of the
// export schema and never changes.
var reportNamespace = [16]byte{
	0x7d, 0x4f, 0x8c, 0x1e, 0x2a, 0x6b, 0x4e, 0x5d, 0x9c, 0x3f, 0x0b, 0x1a, 0x8e, 0x6d, 0x5c, 0x42,
}

// ReportID is the report ID of an evaluation the server answered itself: a
// name-based UUID (version 5) of reportNamespace over the decimal transaction
// ID and sequence number of the raw row, joined by a colon. The raw row never
// changes, so a retry and a replay export the same ID, and the summaries of
// one flag, variation and reason that two rows hold are never folded into
// one where a table deduplicates on the report ID.
func ReportID(xid uint64, seq int64) string {
	h := sha1.New() //nolint:gosec // see the import
	h.Write(reportNamespace[:])
	h.Write([]byte(strconv.FormatUint(xid, 10) + ":" + strconv.FormatInt(seq, 10)))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | 0x50 // version 5
	u[8] = u[8]&0x3f | 0x80 // the variant of RFC 9562
	return id.FromBytes[struct{}](u).String()
}

// RowIDs lists the IDs of rows, in their order.
func RowIDs(rows []Row) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID()
	}
	return ids
}

// DeliveryID identifies one batch of a stream to the target, which can
// deduplicate on it: the first 26 hex digits of the SHA-256 of the sink, the
// event kind, the cursor range and the IDs of the rows, in order, one per
// line, behind "dlv_". The same rows give the same ID however often the batch
// is sent; other rows over the same range give another.
func DeliveryID(sink ID, kind EventKind, from, to events.Cursor, ids []string) string {
	lines := make([]string, 0, 4+len(ids))
	lines = append(lines, sink.String(), string(kind), cursorText(from), cursorText(to))
	lines = append(lines, ids...)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "dlv_" + hex.EncodeToString(sum[:])[:26]
}

func cursorText(c events.Cursor) string {
	return strconv.FormatUint(c.XID, 10) + ":" + strconv.FormatInt(c.Seq, 10)
}

// MarshalRow encodes row as one JSON object: the columns of its schema in
// order, timestamps as Unix milliseconds, and no HTML escaping, so the text
// a unit sent reaches the target as it was.
func MarshalRow(row Row) ([]byte, error) {
	var b bytes.Buffer
	if err := WriteJSONLines(&b, []Row{row}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// WriteJSONLines writes rows to w as JSON Lines: one object per line, each
// ended by a newline.
func WriteJSONLines(w io.Writer, rows []Row) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r.wire()); err != nil {
			return err
		}
	}
	return nil
}

// SampleRows returns the SampleCount rows of kind that a sample sends,
// marked as samples by their IDs, sample-1 to sample-10, and their keys, and
// received at now; none for a kind no sink exports.
func SampleRows(kind EventKind, projectKey string, now time.Time) []Row {
	if !slices.Contains(EventKinds, kind) {
		return nil
	}
	rows := make([]Row, SampleCount)
	for i := range rows {
		n := "sample-" + strconv.Itoa(i+1)
		switch kind {
		case EventExposure:
			rows[i] = ExposureRow{
				EventID: n, ProjectKey: projectKey, EnvironmentKey: "sample", ExperimentKey: "sample", FlagKey: "sample",
				VariationKey: "sample", UnitType: "sample", UnitID: "sample", EvaluationReason: "experiment",
				Platform: "sample", SDKName: "sample", SDKVersion: "sample", OccurredAt: now, ReceivedAt: now,
			}
		case EventAlias:
			rows[i] = AliasRow{
				EventID: n, ProjectKey: projectKey, EnvironmentKey: "sample", FromUnitType: "sample", FromUnitID: "sample",
				ToUnitType: "sample", ToUnitID: "sample", Platform: "sample", SDKName: "sample", SDKVersion: "sample",
				OccurredAt: now, ReceivedAt: now,
			}
		case EventEvaluationSummary:
			rows[i] = SummaryRow{
				ReportID: n, ProjectKey: projectKey, EnvironmentKey: "sample", FlagKey: "sample", VariationKey: "sample",
				EvaluationReason: "experiment", Count: 1, WindowStart: now, WindowEnd: now, SDKName: "sample",
				SDKVersion: "sample", Platform: "sample", ReceivedAt: now, Source: "sdk",
			}
		}
	}
	return rows
}
