package sinks_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	domain "github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/out/sinks"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

var received = time.Date(2026, 3, 4, 5, 6, 7, 891_000_000, time.UTC)

// objectStorageSink is a sink to bucket of the store, reached at endpoint.
func objectStorageSink(s *testkit.ObjectStorage, endpoint, bucket string, change func(*domain.ObjectStorage)) appsinks.Call {
	cfg := &domain.ObjectStorage{
		Endpoint: endpoint, Region: s.Region, Bucket: bucket, AccessKeyID: s.AccessKeyID, PathStyle: true,
		Format: "jsonl", Compression: "gzip",
	}
	if change != nil {
		change(cfg)
	}
	return appsinks.Call{
		Sink:       domain.Sink{Input: domain.Input{Name: "Lake", Kind: domain.KindObjectStorage, Config: domain.Config{ObjectStorage: cfg}}},
		ProjectKey: "shop",
		Secrets:    []string{s.SecretAccessKey},
	}
}

func objectStorage() *sinks.ObjectStorage {
	return sinks.NewObjectStorage(sinks.NewClient(domain.KindObjectStorage, sinks.Options{Policy: governance.TargetPolicy{}}))
}

func exposures(n int) []domain.Row {
	rows := make([]domain.Row, n)
	for i := range rows {
		rows[i] = domain.ExposureRow{
			EventID: fmt.Sprintf("e-%d", i+1), ProjectKey: "shop", EnvironmentKey: "production", ExperimentKey: "checkout_test",
			FlagKey: "checkout", VariationKey: "on", UnitType: "user", UnitID: fmt.Sprintf("u-%d", i+1),
			EvaluationReason: "experiment", Override: i%2 == 0, SDKName: "flagnetic-go", SDKVersion: "1.4.0", RulesetVersion: 7,
			OccurredAt: received, ReceivedAt: received, Seq: int64(100 + i),
		}
	}
	return rows
}

func TestEachFormatIsWrittenAsOneObjectUnderTheKeyAndReadsBackAsTheRows(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	rows := exposures(3)
	for name, tc := range map[string]struct {
		format, compression, key, contentType string
	}{
		"gzip": {"jsonl", "gzip", "lake/exposure/dt=2026-03-04/shop-100-102-abc.jsonl.gz", "application/gzip"},
		"zstd": {"jsonl", "zstd", "lake/exposure/dt=2026-03-04/shop-100-102-abc.jsonl.zst", "application/zstd"},
		"none": {"jsonl", "none", "lake/exposure/dt=2026-03-04/shop-100-102-abc.jsonl", "application/x-ndjson"},
	} {
		t.Run(name, func(t *testing.T) {
			bucket := store.NewBucket(t)
			c := objectStorageSink(store, store.Endpoint, bucket, func(c *domain.ObjectStorage) {
				c.Prefix, c.Format, c.Compression = "lake", tc.format, tc.compression
			})
			if f := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Rows: rows, Now: received}); f != nil {
				t.Fatalf("Deliver = %v", f)
			}
			if got := store.Keys(t, bucket); len(got) != 1 || got[0] != tc.key {
				t.Fatalf("keys = %v, want [%s]", got, tc.key)
			}
			content, contentType := store.Object(t, bucket, tc.key)
			if contentType != tc.contentType {
				t.Errorf("content type = %q, want %q", contentType, tc.contentType)
			}
			lines := decompressed(t, name, content)
			var want bytes.Buffer
			if err := domain.WriteJSONLines(&want, rows); err != nil {
				t.Fatal(err)
			}
			if string(lines) != want.String() {
				t.Errorf("content = %s\nwant %s", lines, want.String())
			}
		})
	}
}

func decompressed(t *testing.T, compression string, b []byte) []byte {
	t.Helper()
	var r io.Reader = bytes.NewReader(b)
	switch compression {
	case "gzip":
		zr, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		r = zr
	case "zstd":
		zr, err := zstd.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		r = zr
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type exposureFile struct {
	SchemaVersion  int64  `parquet:"schema_version"`
	EventID        string `parquet:"event_id"`
	Override       bool   `parquet:"override"`
	RulesetVersion int64  `parquet:"ruleset_version"`
	ReceivedAt     int64  `parquet:"received_at"`
}

func TestAParquetFileHoldsTheRowsInRowGroupsOfAHundredThousandWithTheDocumentedTypes(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	rows := exposures(100_001)
	c := objectStorageSink(store, store.Endpoint, bucket, func(c *domain.ObjectStorage) { c.Format, c.Compression = "parquet", "zstd" })
	if f := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Rows: rows, Now: received}); f != nil {
		t.Fatalf("Deliver = %v", f)
	}
	keys := store.Keys(t, bucket)
	if len(keys) != 1 || keys[0] != "exposure/dt=2026-03-04/shop-100-100100-abc.parquet" {
		t.Fatalf("keys = %v", keys)
	}
	content, contentType := store.Object(t, bucket, keys[0])
	if contentType != "application/vnd.apache.parquet" {
		t.Errorf("content type = %q", contentType)
	}
	file, err := parquet.OpenFile(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	groups := file.RowGroups()
	if len(groups) != 2 || groups[0].NumRows() != 100_000 || groups[1].NumRows() != 1 {
		t.Fatalf("row groups = %d, want two of 100000 and 1 rows", len(groups))
	}
	types := map[string]string{}
	for _, f := range file.Schema().Fields() {
		types[f.Name()] = f.Type().String()
	}
	for column, want := range map[string]string{
		"schema_version": "INT(64,true)", "event_id": "STRING", "override": "BOOLEAN", "ruleset_version": "INT(64,true)",
		"occurred_at": "TIMESTAMP(isAdjustedToUTC=true,unit=MILLIS)", "received_at": "TIMESTAMP(isAdjustedToUTC=true,unit=MILLIS)",
	} {
		if types[column] != want {
			t.Errorf("column %s is %q, want %q", column, types[column], want)
		}
	}
	read, err := parquet.Read[exposureFile](bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 100_001 {
		t.Fatalf("read %d rows", len(read))
	}
	if first := read[0]; first.SchemaVersion != 1 || first.EventID != "e-1" || !first.Override || first.RulesetVersion != 7 || first.ReceivedAt != received.UnixMilli() {
		t.Errorf("first row = %+v", first)
	}
}

func TestAnEmptyBatchIsAcknowledgedWithoutAFile(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	c := objectStorageSink(store, store.Endpoint, bucket, nil)
	if f := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Now: received}); f != nil {
		t.Fatalf("Deliver = %v", f)
	}
	if keys := store.Keys(t, bucket); len(keys) != 0 {
		t.Errorf("keys = %v, want none", keys)
	}
}

func TestASampleIsWrittenBesideTheBatchesUnderAMarkedName(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	c := objectStorageSink(store, store.Endpoint, bucket, nil)
	rows := domain.SampleRows(domain.EventAlias, "shop", received)
	if f := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "sample_1", Kind: domain.EventAlias, Rows: rows, Sample: true, Now: received}); f != nil {
		t.Fatalf("Deliver = %v", f)
	}
	keys := store.Keys(t, bucket)
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "alias/dt=2026-03-04/shop-sample-20260304T050607Z-") {
		t.Fatalf("keys = %v", keys)
	}
	content, _ := store.Object(t, bucket, keys[0])
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(decompressed(t, "gzip", content)))
	for sc.Scan() {
		var row map[string]any
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		n++
		if row["event_id"] != fmt.Sprintf("sample-%d", n) {
			t.Errorf("row %d is %v", n, row["event_id"])
		}
	}
	if n != domain.SampleCount {
		t.Errorf("%d rows, want %d", n, domain.SampleCount)
	}
}

func TestTheProbeWritesAnObjectUnderThePrefixAndDeletesIt(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	proxy := testkit.NewS3Proxy(t, store)
	c := objectStorageSink(store, proxy.Endpoint, bucket, func(c *domain.ObjectStorage) { c.Prefix = "/lake/events/" })
	if probed := objectStorage().Probe(t.Context(), c, received, false); probed.Failure != nil {
		t.Fatalf("Probe = %v", probed.Failure)
	}
	var calls []string
	for _, r := range proxy.Requests() {
		calls = append(calls, r.Method+" "+r.Path)
	}
	want := []string{"PUT /" + bucket + "/lake/events/_flagnetic_probe", "DELETE /" + bucket + "/lake/events/_flagnetic_probe"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", calls, want)
	}
	if keys := store.Keys(t, bucket); len(keys) != 0 {
		t.Errorf("the probe left %v", keys)
	}
}

func TestAnEmptyRegionIsAskedOfTheBucket(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	proxy := testkit.NewS3Proxy(t, store)
	c := objectStorageSink(store, proxy.Endpoint, bucket, func(c *domain.ObjectStorage) { c.Region = "" })
	if probed := objectStorage().Probe(t.Context(), c, received, false); probed.Failure != nil {
		t.Fatalf("Probe = %v", probed.Failure)
	}
	if first := proxy.Requests()[0]; first.Method != "GET" || first.Path != "/"+bucket+"/" {
		t.Errorf("first request = %+v, want the bucket's location asked", first)
	}
}

func TestWhatTheServiceAnswersIsReducedToAClassStatusAndCode(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	proxy := testkit.NewS3Proxy(t, store)
	bucket := store.NewBucket(t)
	rows := exposures(1)
	deliver := func(c appsinks.Call) *domain.Failure {
		return objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Rows: rows, Now: received})
	}
	for name, tc := range map[string]struct {
		call func() appsinks.Call
		want domain.Failure
	}{
		"a missing bucket": {
			func() appsinks.Call { return objectStorageSink(store, store.Endpoint, "no-such-bucket", nil) },
			domain.Failure{Class: domain.ClassRejected, Status: 404, Code: "NoSuchBucket"},
		},
		"wrong credentials": {
			func() appsinks.Call {
				c := objectStorageSink(store, store.Endpoint, bucket, nil)
				c.Secrets = []string{"not-the-secret"}
				return c
			},
			domain.Failure{Class: domain.ClassRejected, Status: 403, Code: "SignatureDoesNotMatch"},
		},
		"a server error": {
			func() appsinks.Call {
				proxy.FailNext("PUT", 1)
				return objectStorageSink(store, proxy.Endpoint, bucket, nil)
			},
			domain.Failure{Class: domain.ClassStatus, Status: 500, Code: "InternalError"},
		},
		"nothing listening": {
			func() appsinks.Call { return objectStorageSink(store, "http://127.0.0.1:1", bucket, nil) },
			domain.Failure{Class: domain.ClassConnect},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := deliver(tc.call())
			if got == nil || *got != tc.want {
				t.Errorf("Deliver = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestACallThatTakesLongerThanTheLimitIsATimeout(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	c := objectStorageSink(store, store.Endpoint, bucket, nil)
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	got := objectStorage().Deliver(ctx, c, appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Rows: exposures(1), Now: received})
	if got == nil || got.Class != domain.ClassTimeout {
		t.Errorf("Deliver = %v, want a timeout", got)
	}
}

func TestEveryEventKindReadsBackInEveryFormat(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	for _, kind := range domain.EventKinds {
		for _, format := range []struct{ format, compression string }{{"jsonl", "zstd"}, {"parquet", "zstd"}} {
			t.Run(string(kind)+" "+format.format, func(t *testing.T) {
				bucket := store.NewBucket(t)
				c := objectStorageSink(store, store.Endpoint, bucket, func(c *domain.ObjectStorage) { c.Format, c.Compression = format.format, format.compression })
				rows := domain.SampleRows(kind, "shop", received)
				for i := range rows {
					switch r := rows[i].(type) {
					case domain.ExposureRow:
						r.Seq = int64(i + 1)
						rows[i] = r
					case domain.AliasRow:
						r.Seq = int64(i + 1)
						rows[i] = r
					case domain.SummaryRow:
						r.Seq = int64(i + 1)
						rows[i] = r
					}
				}
				if f := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: kind, Rows: rows, Now: received}); f != nil {
					t.Fatalf("Deliver = %v", f)
				}
				keys := store.Keys(t, bucket)
				if len(keys) != 1 || !strings.HasPrefix(keys[0], string(kind)+"/dt=2026-03-04/shop-1-10-abc.") {
					t.Fatalf("keys = %v", keys)
				}
				content, _ := store.Object(t, bucket, keys[0])
				if format.format == "parquet" {
					file, err := parquet.OpenFile(bytes.NewReader(content), int64(len(content)))
					if err != nil || file.NumRows() != domain.SampleCount {
						t.Fatalf("the file holds %d rows, err %v, want %d", file.NumRows(), err, domain.SampleCount)
					}
				}
			})
		}
	}
}

func TestTheTargetsHoldAnObjectStorageTargetBesideTheWebhookOne(t *testing.T) {
	targets := sinks.NewTargets(governance.TargetPolicy{}, nil)
	for _, kind := range []domain.Kind{domain.KindWebhook, domain.KindObjectStorage} {
		if _, ok := targets.For(kind); !ok {
			t.Errorf("no target for %s sinks", kind)
		}
	}
	if _, ok := targets.For(domain.KindClickHouse); ok {
		t.Error("a ClickHouse target before ClickHouse is delivered to")
	}
}

func TestAProbeThatCannotWriteIsRefused(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	c := objectStorageSink(store, store.Endpoint, "no-such-bucket-at-all", nil)
	probed := objectStorage().Probe(t.Context(), c, received, false)
	want := domain.Failure{Class: domain.ClassRejected, Status: 404, Code: "NoSuchBucket"}
	if probed.Failure == nil || *probed.Failure != want {
		t.Errorf("Probe = %v, want %v", probed.Failure, want)
	}
}

func TestAServiceThatRedirectsIsNotFollowed(t *testing.T) {
	var followed atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	store := testkit.StartObjectStorage(t)
	c := objectStorageSink(store, redirecting.URL, "lake", nil)
	got := objectStorage().Deliver(t.Context(), c, appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Rows: exposures(1), Now: received})
	if got == nil || got.Class != domain.ClassRedirect || got.Status != http.StatusTemporaryRedirect {
		t.Errorf("Deliver = %v, want class redirect with status 307", got)
	}
	if followed.Load() {
		t.Error("the redirect was followed")
	}
}
