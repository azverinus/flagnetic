package sinks

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

// parquetRowGroupRows is the most rows one row group of a Parquet file holds.
const parquetRowGroupRows = 100_000

// encodeFile writes rows as the one file c says: JSON Lines, compressed with
// gzip or zstd or not at all, or zstd-compressed Parquet.
func encodeFile(c sinks.ObjectStorage, rows []sinks.Row) ([]byte, error) {
	if c.Format == "parquet" {
		return parquetFile(rows)
	}
	return jsonLinesFile(rows, c.Compression)
}

// jsonLinesFile is rows as JSON Lines, each line a row exactly as the export
// schema lays it out, which is how a webhook sink's events read.
func jsonLinesFile(rows []sinks.Row, compression string) ([]byte, error) {
	var buf bytes.Buffer
	var w io.Writer = &buf
	var closer io.Closer
	switch compression {
	case "none":
	case "zstd":
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			return nil, err
		}
		w, closer = zw, zw
	default:
		zw := gzip.NewWriter(&buf)
		w, closer = zw, zw
	}
	if err := sinks.WriteJSONLines(w, rows); err != nil {
		return nil, err
	}
	if closer != nil {
		if err := closer.Close(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// parquetFile is rows as one Parquet file compressed with zstd, a row group to
// each parquetRowGroupRows rows. Every row of a batch is of one event kind.
func parquetFile(rows []sinks.Row) ([]byte, error) {
	switch rows[0].(type) {
	case sinks.ExposureRow:
		return writeParquet(rows, exposureParquet)
	case sinks.AliasRow:
		return writeParquet(rows, aliasParquet)
	case sinks.SummaryRow:
		return writeParquet(rows, summaryParquet)
	default:
		return nil, fmt.Errorf("no file holds rows of %T", rows[0])
	}
}

func writeParquet[T any](rows []sinks.Row, convert func(sinks.Row) (T, bool)) ([]byte, error) {
	typed := make([]T, len(rows))
	for i, r := range rows {
		row, ok := convert(r)
		if !ok {
			return nil, fmt.Errorf("a batch holds rows of %T among rows of another event kind", r)
		}
		typed[i] = row
	}
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf,
		parquet.Compression(&parquet.Zstd),
		parquet.MaxRowsPerRowGroup(parquetRowGroupRows),
		parquet.CreatedBy("flagnetic", "", ""),
	)
	if _, err := w.Write(typed); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// The Parquet columns of each event kind, in the order of the export schema.
// Strings are UTF8, the override a boolean, integers INT64 and timestamps
// TIMESTAMP(MILLIS, UTC).
type exposureColumns struct {
	SchemaVersion    int64  `parquet:"schema_version"`
	EventID          string `parquet:"event_id"`
	ProjectKey       string `parquet:"project_key"`
	EnvironmentKey   string `parquet:"environment_key"`
	ExperimentKey    string `parquet:"experiment_key"`
	FlagKey          string `parquet:"flag_key"`
	VariationKey     string `parquet:"variation_key"`
	UnitType         string `parquet:"unit_type"`
	UnitID           string `parquet:"unit_id"`
	EvaluationReason string `parquet:"evaluation_reason"`
	Override         bool   `parquet:"override"`
	Platform         string `parquet:"platform"`
	AppVersion       string `parquet:"app_version"`
	SDKName          string `parquet:"sdk_name"`
	SDKVersion       string `parquet:"sdk_version"`
	RulesetVersion   int64  `parquet:"ruleset_version"`
	OccurredAt       int64  `parquet:"occurred_at,timestamp(millisecond)"`
	ReceivedAt       int64  `parquet:"received_at,timestamp(millisecond)"`
}

func exposureParquet(row sinks.Row) (exposureColumns, bool) {
	r, ok := row.(sinks.ExposureRow)
	return exposureColumns{
		SchemaVersion: sinks.SchemaVersion, EventID: r.EventID, ProjectKey: r.ProjectKey, EnvironmentKey: r.EnvironmentKey,
		ExperimentKey: r.ExperimentKey, FlagKey: r.FlagKey, VariationKey: r.VariationKey, UnitType: r.UnitType,
		UnitID: r.UnitID, EvaluationReason: r.EvaluationReason, Override: r.Override, Platform: r.Platform,
		AppVersion: r.AppVersion, SDKName: r.SDKName, SDKVersion: r.SDKVersion, RulesetVersion: r.RulesetVersion,
		OccurredAt: r.OccurredAt.UnixMilli(), ReceivedAt: r.ReceivedAt.UnixMilli(),
	}, ok
}

type aliasColumns struct {
	SchemaVersion  int64  `parquet:"schema_version"`
	EventID        string `parquet:"event_id"`
	ProjectKey     string `parquet:"project_key"`
	EnvironmentKey string `parquet:"environment_key"`
	FromUnitType   string `parquet:"from_unit_type"`
	FromUnitID     string `parquet:"from_unit_id"`
	ToUnitType     string `parquet:"to_unit_type"`
	ToUnitID       string `parquet:"to_unit_id"`
	Platform       string `parquet:"platform"`
	SDKName        string `parquet:"sdk_name"`
	SDKVersion     string `parquet:"sdk_version"`
	OccurredAt     int64  `parquet:"occurred_at,timestamp(millisecond)"`
	ReceivedAt     int64  `parquet:"received_at,timestamp(millisecond)"`
}

func aliasParquet(row sinks.Row) (aliasColumns, bool) {
	r, ok := row.(sinks.AliasRow)
	return aliasColumns{
		SchemaVersion: sinks.SchemaVersion, EventID: r.EventID, ProjectKey: r.ProjectKey, EnvironmentKey: r.EnvironmentKey,
		FromUnitType: r.FromUnitType, FromUnitID: r.FromUnitID, ToUnitType: r.ToUnitType, ToUnitID: r.ToUnitID,
		Platform: r.Platform, SDKName: r.SDKName, SDKVersion: r.SDKVersion,
		OccurredAt: r.OccurredAt.UnixMilli(), ReceivedAt: r.ReceivedAt.UnixMilli(),
	}, ok
}

type summaryColumns struct {
	SchemaVersion    int64  `parquet:"schema_version"`
	ReportID         string `parquet:"report_id"`
	ProjectKey       string `parquet:"project_key"`
	EnvironmentKey   string `parquet:"environment_key"`
	FlagKey          string `parquet:"flag_key"`
	VariationKey     string `parquet:"variation_key"`
	EvaluationReason string `parquet:"evaluation_reason"`
	Count            int64  `parquet:"count"`
	WindowStart      int64  `parquet:"window_start,timestamp(millisecond)"`
	WindowEnd        int64  `parquet:"window_end,timestamp(millisecond)"`
	SDKName          string `parquet:"sdk_name"`
	SDKVersion       string `parquet:"sdk_version"`
	Platform         string `parquet:"platform"`
	AppVersion       string `parquet:"app_version"`
	RulesetVersion   int64  `parquet:"ruleset_version"`
	ReceivedAt       int64  `parquet:"received_at,timestamp(millisecond)"`
	Source           string `parquet:"source"`
}

func summaryParquet(row sinks.Row) (summaryColumns, bool) {
	r, ok := row.(sinks.SummaryRow)
	return summaryColumns{
		SchemaVersion: sinks.SchemaVersion, ReportID: r.ReportID, ProjectKey: r.ProjectKey, EnvironmentKey: r.EnvironmentKey,
		FlagKey: r.FlagKey, VariationKey: r.VariationKey, EvaluationReason: r.EvaluationReason, Count: r.Count,
		WindowStart: r.WindowStart.UnixMilli(), WindowEnd: r.WindowEnd.UnixMilli(), SDKName: r.SDKName,
		SDKVersion: r.SDKVersion, Platform: r.Platform, AppVersion: r.AppVersion, RulesetVersion: r.RulesetVersion,
		ReceivedAt: r.ReceivedAt.UnixMilli(), Source: r.Source,
	}, ok
}
