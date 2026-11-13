package sinks_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

func cursor(xid uint64, seq int64) events.Cursor { return events.Cursor{XID: xid, Seq: seq} }

func TestAFileIsNamedByPartitionRangeAndTheHashOfItsDelivery(t *testing.T) {
	received := time.Date(2026, 3, 4, 23, 59, 59, 0, time.FixedZone("late", -5*3600))
	rows := []sinks.Row{
		sinks.ExposureRow{EventID: "a", Seq: 41, ReceivedAt: received},
		sinks.ExposureRow{EventID: "b", Seq: 90, ReceivedAt: received.Add(48 * time.Hour)},
	}
	for name, tc := range map[string]struct {
		cfg  sinks.ObjectStorage
		want string
	}{
		"jsonl with gzip under a prefix": {
			sinks.ObjectStorage{Prefix: "lake/events", Format: "jsonl", Compression: "gzip"},
			"lake/events/exposure/dt=2026-03-05/shop-41-90-0123456789abcdef0123456789.jsonl.gz",
		},
		"jsonl with zstd": {
			sinks.ObjectStorage{Format: "jsonl", Compression: "zstd"},
			"exposure/dt=2026-03-05/shop-41-90-0123456789abcdef0123456789.jsonl.zst",
		},
		"jsonl uncompressed": {
			sinks.ObjectStorage{Format: "jsonl", Compression: "none"},
			"exposure/dt=2026-03-05/shop-41-90-0123456789abcdef0123456789.jsonl",
		},
		"parquet": {
			sinks.ObjectStorage{Format: "parquet", Compression: "zstd"},
			"exposure/dt=2026-03-05/shop-41-90-0123456789abcdef0123456789.parquet",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := tc.cfg.BatchKey(sinks.EventExposure, "shop", "dlv_0123456789abcdef0123456789", rows)
			if got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOtherRowsOverTheSameRangeLandBesideTheFirstFile(t *testing.T) {
	cfg := sinks.ObjectStorage{Format: "jsonl", Compression: "gzip"}
	rows := []sinks.Row{sinks.AliasRow{EventID: "a", Seq: 1, ReceivedAt: rowTime}, sinks.AliasRow{EventID: "c", Seq: 9, ReceivedAt: rowTime}}
	narrow := []sinks.Row{rows[0], rows[1]}
	first := cfg.BatchKey(sinks.EventAlias, "shop", sinks.DeliveryID(sinks.ID{}, sinks.EventAlias, cursor(0, 0), cursor(1, 9), []string{"a", "b", "c"}), rows)
	second := cfg.BatchKey(sinks.EventAlias, "shop", sinks.DeliveryID(sinks.ID{}, sinks.EventAlias, cursor(0, 0), cursor(1, 9), []string{"a", "c"}), narrow)
	if first == second {
		t.Fatalf("a narrower replay over the same range names the same file %q", first)
	}
	if !strings.HasPrefix(first, "alias/dt=2026-03-04/shop-1-9-") || !strings.HasPrefix(second, "alias/dt=2026-03-04/shop-1-9-") {
		t.Errorf("the range is part of both names: %q, %q", first, second)
	}
}

func TestASampleFileCarriesAMarkerAndTheTimeInPlaceOfTheRange(t *testing.T) {
	cfg := sinks.ObjectStorage{Prefix: "/lake/", Format: "jsonl", Compression: "gzip"}
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	rows := sinks.SampleRows(sinks.EventExposure, "shop", now)
	got := cfg.SampleKey(sinks.EventExposure, "shop", "sample_5c8d7c30-9a0e-4f55-8d0e-0a1b2c3d4e5f", now, rows)
	want := "lake/exposure/dt=2026-03-04/shop-sample-20260304T050607Z-"
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, ".jsonl.gz") {
		t.Fatalf("sample key = %q, want %s<hash>.jsonl.gz", got, want)
	}
	if hash := strings.TrimSuffix(strings.TrimPrefix(got, want), ".jsonl.gz"); len(hash) != 26 {
		t.Errorf("hash %q is %d digits, want 26", hash, len(hash))
	}
	if again := cfg.SampleKey(sinks.EventExposure, "shop", "sample_other", now, rows); again == got {
		t.Error("two samples at one instant share a name")
	}
}

func TestTheProbeObjectSitsUnderThePrefix(t *testing.T) {
	if got := (sinks.ObjectStorage{Prefix: "lake"}).ProbeKey(); got != "lake/_flagnetic_probe" {
		t.Errorf("probe key = %q", got)
	}
	if got := (sinks.ObjectStorage{}).ProbeKey(); got != "_flagnetic_probe" {
		t.Errorf("probe key without a prefix = %q", got)
	}
}

func TestEachFormatHasItsExtensionAndContentType(t *testing.T) {
	for _, tc := range []struct {
		cfg                sinks.ObjectStorage
		extension, content string
	}{
		{sinks.ObjectStorage{Format: "jsonl", Compression: "gzip"}, "jsonl.gz", "application/gzip"},
		{sinks.ObjectStorage{Format: "jsonl", Compression: "zstd"}, "jsonl.zst", "application/zstd"},
		{sinks.ObjectStorage{Format: "jsonl", Compression: "none"}, "jsonl", "application/x-ndjson"},
		{sinks.ObjectStorage{Format: "parquet", Compression: "zstd"}, "parquet", "application/vnd.apache.parquet"},
	} {
		if ext, content := tc.cfg.FileType(); ext != tc.extension || content != tc.content {
			t.Errorf("%s %s = %q %q, want %q %q", tc.cfg.Format, tc.cfg.Compression, ext, content, tc.extension, tc.content)
		}
	}
}
