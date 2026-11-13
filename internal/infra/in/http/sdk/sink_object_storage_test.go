package sdk_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// objectStorageInput is an object storage sink's configuration for bucket of
// the store, reached at endpoint (the store itself, or a proxy in front of
// it), by path.
func objectStorageInput(s *testkit.ObjectStorage, endpoint, bucket, prefix string) *management.SinkObjectStorageInput {
	return &management.SinkObjectStorageInput{
		Endpoint: &endpoint, Region: &s.Region, Bucket: &bucket, Prefix: &prefix,
		AccessKeyId: &s.AccessKeyID, SecretAccessKey: &s.SecretAccessKey, PathStyle: ptr(true),
	}
}

// bucketSink is an object storage sink of kinds to bucket.
func bucketSink(s *testkit.ObjectStorage, endpoint, bucket, prefix string, kinds ...string) management.SinkCreate {
	body := management.SinkCreate{Name: "Lake", Kind: "object_storage", ObjectStorage: objectStorageInput(s, endpoint, bucket, prefix)}
	if len(kinds) > 0 {
		body.EventKinds = &kinds
	}
	testkit.RecordSecret(s.SecretAccessKey)
	return body
}

// objectKeyPattern matches the key of a batch's file:
// {prefix}/{event_kind}/dt={day}/{project_key}-{first_seq}-{last_seq}-{hash}.{ext}.
var objectKeyPattern = regexp.MustCompile(`^(?:(.+)/)?(exposure|alias|evaluation_summary)/dt=(\d{4}-\d{2}-\d{2})/(.+)-(\d+)-(\d+)-([0-9a-f]{26})\.(jsonl\.gz|jsonl\.zst|jsonl|parquet)$`)

// objectKey is the key of a batch's file, taken apart.
type objectKey struct {
	key, prefix, eventKind, day, project string
	firstSeq, lastSeq                    int64
	hash, ext                            string
}

func parseObjectKey(t testing.TB, key string) objectKey {
	t.Helper()
	m := objectKeyPattern.FindStringSubmatch(key)
	if m == nil {
		t.Fatalf("object key %q does not follow {prefix}/{event_kind}/dt={day}/{project}-{first_seq}-{last_seq}-{hash}.{ext}", key)
	}
	first, _ := strconv.ParseInt(m[5], 10, 64)
	last, _ := strconv.ParseInt(m[6], 10, 64)
	return objectKey{key: key, prefix: m[1], eventKind: m[2], day: m[3], project: m[4], firstSeq: first, lastSeq: last, hash: m[7], ext: m[8]}
}

// objectKeys lists the keys in bucket under prefix, taken apart.
func objectKeys(t testing.TB, s *testkit.ObjectStorage, bucket, prefix string) []objectKey {
	t.Helper()
	var out []objectKey
	for _, k := range s.Keys(t, bucket) {
		if strings.HasPrefix(k, prefix+"/") {
			out = append(out, parseObjectKey(t, k))
		}
	}
	return out
}

func keyOfKind(t testing.TB, keys []objectKey, kind string) objectKey {
	t.Helper()
	for _, k := range keys {
		if k.eventKind == kind {
			return k
		}
	}
	t.Fatalf("no %s object among %v", kind, keys)
	return objectKey{}
}

// fileRows reads a batch's file back as rows, every value in its canonical
// text form, whatever its format and compression.
func fileRows(t testing.TB, key string, body []byte) []map[string]string {
	t.Helper()
	switch {
	case strings.HasSuffix(key, ".parquet"):
		return parquetRows(t, body)
	case strings.HasSuffix(key, ".jsonl.gz"):
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%s is not gzip: %v", key, err)
		}
		return jsonLines(t, zr)
	case strings.HasSuffix(key, ".jsonl.zst"):
		zr, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%s is not zstd: %v", key, err)
		}
		defer zr.Close()
		return jsonLines(t, zr)
	default:
		return jsonLines(t, bytes.NewReader(body))
	}
}

func jsonLines(t testing.TB, r io.Reader) []map[string]string {
	t.Helper()
	var rows []map[string]string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		d := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		d.UseNumber()
		var row map[string]any
		if err := d.Decode(&row); err != nil {
			t.Fatalf("decoding line %q: %v", sc.Text(), err)
		}
		rows = append(rows, canonicalRow(row))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func parquetRows(t testing.TB, body []byte) []map[string]string {
	t.Helper()
	f, err := parquet.OpenFile(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("opening a Parquet file: %v", err)
	}
	columns := f.Schema().Columns()
	var rows []map[string]string
	for _, rg := range f.RowGroups() {
		rr := rg.Rows()
		buf := make([]parquet.Row, 1024)
		for {
			n, err := rr.ReadRows(buf)
			for _, row := range buf[:n] {
				out := map[string]string{}
				for _, v := range row {
					name := columns[v.Column()][0]
					switch v.Kind() {
					case parquet.Boolean:
						out[name] = strconv.FormatBool(v.Boolean())
					case parquet.Int32:
						out[name] = strconv.FormatInt(int64(v.Int32()), 10)
					case parquet.Int64:
						out[name] = strconv.FormatInt(v.Int64(), 10)
					case parquet.ByteArray:
						out[name] = string(v.ByteArray())
					default:
						t.Fatalf("column %s holds a %v", name, v.Kind())
					}
				}
				rows = append(rows, out)
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		_ = rr.Close()
	}
	return rows
}

// canonicalRow is row with every value in the text form fileRows gives it, so
// a webhook sink's events, a JSON Lines file and a Parquet file compare.
func canonicalRow(row map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range row {
		switch v := v.(type) {
		case float64:
			out[k] = strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			out[k] = v.String()
		case nil:
			out[k] = ""
		default:
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

func canonicalRows(rows []map[string]any) []map[string]string {
	out := make([]map[string]string, len(rows))
	for i, r := range rows {
		out[i] = canonicalRow(r)
	}
	return out
}

func rowsEqual(a, b []map[string]string) bool {
	return slices.EqualFunc(a, b, func(x, y map[string]string) bool {
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if w, ok := y[k]; !ok || w != v {
				return false
			}
		}
		return true
	})
}

func fileEventIDs(rows []map[string]string) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r["event_id"]
	}
	return ids
}

// checkParquetTypes checks the Parquet types every export column has: strings
// UTF8, the override a boolean, integers INT64, timestamps TIMESTAMP(MILLIS,
// UTC), none optional, every column chunk zstd, and row groups of the row
// counts wantRowGroups.
func checkParquetTypes(t testing.TB, body []byte, wantRowGroups []int64) {
	t.Helper()
	f, err := parquet.OpenFile(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("opening a Parquet file: %v", err)
	}
	for _, field := range f.Schema().Fields() {
		name, typ := field.Name(), field.Type()
		var logical any
		if lt := typ.LogicalType(); lt != nil {
			logical = lt.Value
		}
		switch name {
		case "override":
			if typ.Kind() != parquet.Boolean {
				t.Errorf("%s is %v, want boolean", name, typ)
			}
		case "schema_version", "ruleset_version", "count":
			it, isInt := logical.(*format.IntType)
			if typ.Kind() != parquet.Int64 || (logical != nil && (!isInt || !it.IsSigned || it.BitWidth != 64)) {
				t.Errorf("%s is %v, want int64", name, typ)
			}
		case "occurred_at", "received_at", "window_start", "window_end":
			ts, isTimestamp := logical.(*format.TimestampType)
			if typ.Kind() != parquet.Int64 || !isTimestamp || !ts.IsAdjustedToUTC {
				t.Errorf("%s is %v, want TIMESTAMP(MILLIS, UTC)", name, typ)
			} else if _, millis := ts.Unit.Value.(*format.MilliSeconds); !millis {
				t.Errorf("%s is %v, want TIMESTAMP(MILLIS, UTC)", name, typ)
			}
		default:
			if _, isString := logical.(*format.StringType); typ.Kind() != parquet.ByteArray || !isString {
				t.Errorf("%s is %v, want a UTF8 string", name, typ)
			}
		}
		if field.Optional() || field.Repeated() {
			t.Errorf("%s is optional or repeated", name)
		}
	}
	var groups []int64
	for _, rg := range f.Metadata().RowGroups {
		groups = append(groups, rg.NumRows)
		for _, col := range rg.Columns {
			if col.MetaData.Codec != format.Zstd {
				t.Errorf("column %v is %v, want zstd", col.MetaData.PathInSchema, col.MetaData.Codec)
			}
		}
	}
	if !slices.Equal(groups, wantRowGroups) {
		t.Errorf("row groups of %v rows, want %v", groups, wantRowGroups)
	}
}

func (f delivering) day() string { return f.clock.Now().UTC().Format("2006-01-02") }

func TestAnObjectStorageSinkWritesEachBatchUnderTheKeySchemeInEveryFormatAndItReadsBackAsTheExportedRows(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	f := startDelivering(t)

	// A webhook sink over the same rows is the reference: its events are the
	// exported rows every file must read back as.
	r := testkit.StartReceiver(t)
	_, secret := f.enabled(t, webhook(r))

	formats := []struct{ name, format, compression, ext string }{
		{"default", "", "", "jsonl.gz"},
		{"gzip", "jsonl", "gzip", "jsonl.gz"},
		{"zstd", "jsonl", "zstd", "jsonl.zst"},
		{"none", "jsonl", "none", "jsonl"},
		{"parquet", "parquet", "", "parquet"},
	}
	sinksByName := map[string]management.Sink{}
	for _, fm := range formats {
		body := bucketSink(store, store.Endpoint, bucket, "exports/"+fm.name)
		body.ObjectStorage.Format, body.ObjectStorage.Compression = ptr(fm.format), ptr(fm.compression)
		created, _ := f.create(t, body)
		sk := f.sink(t, created)
		if sk.BatchSize != 500_000 || sk.FlushInterval != 300 {
			t.Errorf("%s: batching %d every %ds, want the object storage defaults 500000 every 300s", fm.name, sk.BatchSize, sk.FlushInterval)
		}
		sinksByName[fm.name] = f.enable(t, created, management.SinkEnable{})
	}

	now := f.clock.Now()
	f.post(t, f.exposure("u-1", "true", now), f.exposure("u-2", "false", now), f.alias(1),
		summaryEvent(eventID(now, 9001),
			map[string]any{"flag": "checkout", "variation": "true", "reason": "rule", "count": 7},
			map[string]any{"flag": "checkout", "variation": "false", "reason": "rule", "count": 3}))
	tick(t, f.srv)

	want := map[string][]map[string]string{}
	for _, call := range batchCalls(r) {
		b := decodeBatch(t, call, secret)
		want[b.EventKind] = canonicalRows(b.Events)
	}
	if len(want) != 3 {
		t.Fatalf("the reference webhook sink got %v, want every event kind", want)
	}

	ranges := map[string][2]int64{}
	for _, fm := range formats {
		keys := objectKeys(t, store, bucket, "exports/"+fm.name)
		if len(keys) != 3 {
			t.Fatalf("%s: objects %v, want one per event kind", fm.name, keys)
		}
		for kind, rows := range want {
			k := keyOfKind(t, keys, kind)
			if k.prefix != "exports/"+fm.name || k.day != f.day() || k.project != f.p.key || k.ext != fm.ext {
				t.Errorf("%s: key %s, want prefix exports/%s, dt=%s, project %s and .%s", fm.name, k.key, fm.name, f.day(), f.p.key, fm.ext)
			}
			if k.firstSeq > k.lastSeq || (len(rows) == 1 && k.firstSeq != k.lastSeq) {
				t.Errorf("%s: key %s names seq %d-%d for %d rows", fm.name, k.key, k.firstSeq, k.lastSeq, len(rows))
			}
			if seen, ok := ranges[kind]; ok && seen != [2]int64{k.firstSeq, k.lastSeq} {
				t.Errorf("%s: key %s names seq %d-%d, where another format's file names %d-%d", fm.name, k.key, k.firstSeq, k.lastSeq, seen[0], seen[1])
			}
			ranges[kind] = [2]int64{k.firstSeq, k.lastSeq}
			body, _ := store.Object(t, bucket, k.key)
			if got := fileRows(t, k.key, body); !rowsEqual(got, rows) {
				t.Errorf("%s: %s reads back as %v, want the exported rows %v", fm.name, k.key, got, rows)
			}
			if fm.format == "parquet" {
				checkParquetTypes(t, body, []int64{int64(len(rows))})
			}
		}
	}
	if a, b := ranges["exposure"], ranges["alias"]; a[0] >= a[1] || b[0] != b[1] {
		t.Errorf("seq ranges %v, want two exposures apart and one alias", ranges)
	}
	if got := want["exposure"][0]; got["event_id"] == "" || got["unit_id"] != "u-1" || got["project_key"] != f.p.key || got["received_at"] != strconv.FormatInt(now.UnixMilli(), 10) {
		t.Errorf("the first exported exposure is %v", got)
	}
	for name, sk := range sinksByName {
		if got := f.sink(t, sk); got.Health != management.SinkHealthHealthy {
			t.Errorf("%s: health = %s, want healthy", name, got.Health)
		}
	}
}

func TestAParquetFileHoldsOneRowGroupPerHundredThousandRows(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	f := startDelivering(t)
	body := bucketSink(store, store.Endpoint, bucket, "big", "evaluation_summary")
	body.ObjectStorage.Format = ptr("parquet")
	f.enabled(t, body)

	// A summary is a row for each flag, variation and reason it counts: 100
	// flags of two variations counted under five reasons are 1000 rows to a
	// report, so 101 reports are 101,000 rows.
	const flags, reports = 100, 101
	reasons := []string{"fallthrough", "rollout", "off", "override", "rule"}
	var counts []map[string]any
	for i := range flags {
		key := fmt.Sprintf("flag_%03d", i)
		f.p.flag(t, key)
		for _, v := range []string{"true", "false"} {
			for _, reason := range reasons {
				counts = append(counts, map[string]any{"flag": key, "variation": v, "reason": reason, "count": 1})
			}
		}
	}
	var evs []any
	for i := range reports {
		evs = append(evs, summaryEvent(eventID(f.clock.Now(), 100+i), counts...))
		if len(evs) == 10 || i == reports-1 {
			f.post(t, evs...)
			evs = nil
		}
	}
	tick(t, f.srv)

	keys := objectKeys(t, store, bucket, "big")
	if len(keys) != 1 {
		t.Fatalf("objects %v, want one file", keys)
	}
	content, _ := store.Object(t, bucket, keys[0].key)
	checkParquetTypes(t, content, []int64{100_000, int64(reports*len(counts)) - 100_000})
}

func TestAFailedPutRetriedLeavesExactlyOneObjectAndSoDoesALostCursorCommit(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	proxy := testkit.NewS3Proxy(t, store)
	f := startDelivering(t)
	created, _ := f.enabled(t, bucketSink(store, proxy.Endpoint, bucket, "retry", "exposure"))
	e1, e2 := f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "true", f.clock.Now())
	f.post(t, e1, e2)

	// The store writes the object, but its answer is lost: the attempt fails
	// and is retried with backoff.
	proxy.FailNext(http.MethodPut, 1)
	tick(t, f.srv)
	failing := f.sink(t, created)
	lf := streamOf(failing, "exposure").LastFailure
	if failing.Health != management.SinkHealthFailing || lf == nil || lf.Class != management.SinkFailureClass("status") ||
		lf.Status == nil || *lf.Status != 500 || lf.Code == nil || *lf.Code != "InternalError" {
		t.Fatalf("after a failed PUT: %+v, want failing on a 500 InternalError", failing)
	}
	f.clock.Advance(10 * time.Minute)
	tick(t, f.srv)

	keys := objectKeys(t, store, bucket, "retry")
	if len(keys) != 1 {
		t.Fatalf("objects %v, want exactly one after the retry", keys)
	}
	puts := putPaths(proxy)
	if len(puts) < 3 || puts[len(puts)-2] != puts[len(puts)-1] || puts[len(puts)-1] != "/"+bucket+"/"+keys[0].key {
		t.Errorf("PUTs %v, want the batch written twice to /%s/%s", puts, bucket, keys[0].key)
	}
	content, _ := store.Object(t, bucket, keys[0].key)
	if got := fileEventIDs(fileRows(t, keys[0].key, content)); !slices.Equal(got, []string{eventIDOf(e1), eventIDOf(e2)}) {
		t.Errorf("the object holds %v, want both events", got)
	}
	if got := f.sink(t, created); got.Health != management.SinkHealthHealthy {
		t.Errorf("health = %s, want healthy", got.Health)
	}

	// Acknowledged but not recorded, as a crash leaves it: the same rows are
	// sent again once the lease lapses, to the same key.
	f.clock.Advance(10 * time.Minute)
	before := len(putPaths(proxy))
	e3 := f.exposure("u-3", "true", f.clock.Now())
	f.post(t, e3)
	f.srv.LoseNextSinkCommit()
	tick(t, f.srv)
	f.clock.Advance(appsinks.LeaseDuration + time.Second)
	tick(t, f.srv)
	if keys = objectKeys(t, store, bucket, "retry"); len(keys) != 2 {
		t.Fatalf("objects %v, want the first and one for the third event", keys)
	}
	if puts = putPaths(proxy)[before:]; len(puts) != 2 || puts[0] != puts[1] {
		t.Errorf("PUTs %v, want the lost commit's batch written twice to one key", puts)
	}
}

// putPaths lists the path of every PUT the proxy forwarded, in order.
func putPaths(proxy *testkit.S3Proxy) []string {
	var puts []string
	for _, req := range proxy.Requests() {
		if req.Method == http.MethodPut {
			puts = append(puts, req.Path)
		}
	}
	return puts
}

func TestAReplayUnderANarrowerFilterLeavesASecondObjectAndNeverOverwritesTheFirst(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	f := startDelivering(t)
	created, _ := f.enabled(t, bucketSink(store, store.Endpoint, bucket, "replay", "exposure"))

	// A production, a staging and a production row: under environments =
	// [production] the batch keeps the same first and last row and drops the
	// one between.
	from := f.clock.Now()
	p1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, p1)
	f.clock.Advance(time.Second)
	s1 := f.exposure("u-2", "true", f.clock.Now())
	f.postIn(t, "staging", s1)
	f.clock.Advance(time.Second)
	p2 := f.exposure("u-3", "true", f.clock.Now())
	f.post(t, p2)
	tick(t, f.srv)

	keys := objectKeys(t, store, bucket, "replay")
	if len(keys) != 1 {
		t.Fatalf("objects %v, want the live batch's one", keys)
	}
	first := keys[0]
	content, _ := store.Object(t, bucket, first.key)
	all := []string{eventIDOf(p1), eventIDOf(s1), eventIDOf(p2)}
	if got := fileEventIDs(fileRows(t, first.key, content)); !slices.Equal(got, all) {
		t.Fatalf("the live file holds %v, want %v", got, all)
	}

	f.patch(t, created, management.SinkPatch{Environments: &[]string{"production"}})
	f.replay(t, created, management.SinkReplay{From: from})
	f.clock.Advance(6 * time.Minute)
	tick(t, f.srv)

	keys = objectKeys(t, store, bucket, "replay")
	if len(keys) != 2 {
		t.Fatalf("objects %v, want the live file and the replay's beside it", keys)
	}
	narrow := keys[0]
	if narrow.key == first.key {
		narrow = keys[1]
	}
	if narrow.firstSeq != first.firstSeq || narrow.lastSeq != first.lastSeq || narrow.hash == first.hash {
		t.Errorf("replay key %s beside %s, want the same seq range under another hash", narrow.key, first.key)
	}
	content, _ = store.Object(t, bucket, narrow.key)
	if got := fileEventIDs(fileRows(t, narrow.key, content)); !slices.Equal(got, []string{eventIDOf(p1), eventIDOf(p2)}) {
		t.Errorf("the replay's file holds %v, want the two production events", got)
	}
	content, _ = store.Object(t, bucket, first.key)
	if got := fileEventIDs(fileRows(t, first.key, content)); !slices.Equal(got, all) {
		t.Errorf("the live file now holds %v, want it as it was", got)
	}
}

func TestTheProbePutsThenDeletesAnObjectAndPathStyleAndPrefixAreHonoured(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	proxy := testkit.NewS3Proxy(t, store)
	f := startDelivering(t)
	proxyHost := strings.TrimPrefix(proxy.Endpoint, "http://")

	// By path under a prefix: the bucket is the path's first segment.
	pathBody := bucketSink(store, proxy.Endpoint, bucket, "/team/flagnetic/", "exposure")
	pathSink, _ := f.create(t, pathBody)
	probe := do(f.p.admin.TestSinkWithResponse(t.Context(), f.p.key, pathSink.Id, management.SinkTest{}))
	if probe.JSON200 == nil || !probe.JSON200.Ok || probe.JSON200.Class != nil {
		t.Fatalf("test of the stored sink = %d: %s, want ok", probe.StatusCode(), probe.Body)
	}
	probePath := "/" + bucket + "/team/flagnetic/_flagnetic_probe"
	got := proxy.Requests()
	if len(got) < 2 || got[0].Method != http.MethodPut || got[0].Host != proxyHost || got[0].Path != probePath ||
		got[1].Method != http.MethodDelete || got[1].Host != proxyHost || got[1].Path != probePath || got[1].Status/100 != 2 {
		t.Errorf("probe requests %+v, want a PUT then a DELETE of %s on %s", got, probePath, proxyHost)
	}
	if keys := store.Keys(t, bucket); len(keys) != 0 {
		t.Errorf("objects after the test: %v, want the probe gone", keys)
	}

	// By virtual host with no prefix: the bucket is the host's first label.
	hostBody := bucketSink(store, proxy.Endpoint, bucket, "")
	hostBody.ObjectStorage.PathStyle = ptr(false)
	seen := len(proxy.Requests())
	unsaved := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, hostBody))
	if unsaved.JSON200 == nil || !unsaved.JSON200.Ok {
		t.Fatalf("test of an unsaved configuration = %d: %s", unsaved.StatusCode(), unsaved.Body)
	}
	hosted := proxy.Requests()[seen:]
	if len(hosted) != 2 || hosted[0].Host != bucket+"."+proxyHost || hosted[0].Path != "/_flagnetic_probe" || hosted[1].Method != http.MethodDelete {
		t.Errorf("virtual-hosted probe requests %+v, want a PUT then a DELETE of /_flagnetic_probe on %s.%s", hosted, bucket, proxyHost)
	}

	// Delivery goes the same ways.
	hostedBody := bucketSink(store, proxy.Endpoint, bucket, "hosted", "exposure")
	hostedBody.ObjectStorage.PathStyle = ptr(false)
	f.enable(t, pathSink, management.SinkEnable{})
	f.enabled(t, hostedBody)
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)
	hostedKeys, pathKeys := objectKeys(t, store, bucket, "hosted"), objectKeys(t, store, bucket, "team/flagnetic")
	if len(hostedKeys) != 1 || len(pathKeys) != 1 {
		t.Fatalf("objects %v, want one under hosted/ and one under team/flagnetic/", store.Keys(t, bucket))
	}
	var sawHosted, sawPath bool
	for _, req := range proxy.Requests() {
		if req.Method != http.MethodPut {
			continue
		}
		sawHosted = sawHosted || req.Host == bucket+"."+proxyHost && req.Path == "/"+hostedKeys[0].key
		sawPath = sawPath || req.Host == proxyHost && req.Path == "/"+bucket+"/"+pathKeys[0].key
	}
	if !sawHosted || !sawPath {
		t.Errorf("PUTs %+v, want %s by virtual host and %s by path", proxy.Requests(), hostedKeys[0].key, pathKeys[0].key)
	}
}

func TestAnObjectStorageTargetThatRefusesIsToldByClassStatusAndCodeAndNeverByItsWords(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	f := startDelivering(t)

	const wrong = "not-the-secret-access-key-5521"
	testkit.RecordSecret(wrong)
	badSecret := bucketSink(store, store.Endpoint, bucket, "")
	badSecret.ObjectStorage.SecretAccessKey = ptr(wrong)
	for name, tc := range map[string]struct {
		body   management.SinkCreate
		status int
		code   string
	}{
		"a wrong secret":   {badSecret, 403, "SignatureDoesNotMatch"},
		"a missing bucket": {bucketSink(store, store.Endpoint, bucket+"-missing", ""), 404, "NoSuchBucket"},
	} {
		res := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, tc.body))
		r := res.JSON200
		if r == nil || r.Ok || r.Class == nil || *r.Class != management.SinkFailureClass("rejected") || r.Status == nil || *r.Status != tc.status ||
			r.Code == nil || *r.Code != tc.code {
			t.Errorf("%s: test = %d: %s, want class rejected, %d and %s", name, res.StatusCode(), res.Body, tc.status, tc.code)
		}
		if strings.Contains(string(res.Body), wrong) || strings.Contains(string(res.Body), store.SecretAccessKey) {
			t.Errorf("%s: the answer carries a secret: %s", name, res.Body)
		}
	}

	// Enabling probes the bucket, and refuses the sink while it fails.
	created, _ := f.create(t, bucketSink(store, store.Endpoint, bucket+"-missing", ""))
	enable := do(f.p.admin.EnableSinkWithResponse(t.Context(), f.p.key, created.Id, management.SinkEnable{}))
	if enable.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(enable.Body), "sink_not_ready") ||
		!strings.Contains(string(enable.Body), "NoSuchBucket") {
		t.Errorf("enabling a sink of a missing bucket = %d: %s, want 422 sink_not_ready naming NoSuchBucket", enable.StatusCode(), enable.Body)
	}

	// An endpoint with a path, a compression Parquet cannot take, or an
	// endpoint that is no URL is refused before any request.
	for name, change := range map[string]func(*management.SinkObjectStorageInput){
		"an endpoint with a path":    func(c *management.SinkObjectStorageInput) { c.Endpoint = ptr(store.Endpoint + "/bucket") },
		"an endpoint that is no URL": func(c *management.SinkObjectStorageInput) { c.Endpoint = ptr("minio:9000") },
		"parquet with gzip":          func(c *management.SinkObjectStorageInput) { c.Format, c.Compression = ptr("parquet"), ptr("gzip") },
		"an endpoint with a secret in it": func(c *management.SinkObjectStorageInput) {
			c.Endpoint = ptr(strings.Replace(store.Endpoint, "http://", "http://id:secret@", 1))
		},
	} {
		body := bucketSink(store, store.Endpoint, bucket, "")
		change(body.ObjectStorage)
		res := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, body))
		if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), "invalid_sink") {
			t.Errorf("%s: test = %d: %s, want 422 invalid_sink", name, res.StatusCode(), res.Body)
		}
	}
	if got := f.sink(t, created); got.ObjectStorage == nil || got.ObjectStorage.SecretAccessKey != "***" {
		t.Errorf("the secret access key reads back as %+v, want ***", got.ObjectStorage)
	}
}

func TestARejectedPutIsCountedAsARejection(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	proxy := testkit.NewS3Proxy(t, store)
	f := startDelivering(t)
	created, _ := f.enabled(t, bucketSink(store, proxy.Endpoint, bucket, "rejected", "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))

	// The key the store knows no longer signs: every PUT is refused with a
	// 403, a rejection.
	const wrong = "rotated-secret-access-key-4417"
	testkit.RecordSecret(wrong)
	f.patch(t, created, management.SinkPatch{ObjectStorage: &management.SinkObjectStorageInput{
		Endpoint: ptr(proxy.Endpoint), Region: &store.Region, Bucket: &bucket, Prefix: ptr("rejected"),
		AccessKeyId: &store.AccessKeyID, SecretAccessKey: ptr(wrong), PathStyle: ptr(true),
	}})
	tick(t, f.srv)
	failing := f.sink(t, created)
	lf := streamOf(failing, "exposure").LastFailure
	if failing.Health != management.SinkHealthFailing || lf == nil || lf.Class != management.SinkFailureClass("rejected") ||
		lf.Status == nil || *lf.Status != 403 || lf.Code == nil || *lf.Code != "SignatureDoesNotMatch" {
		t.Fatalf("sink after a refused PUT %+v, want failing with class rejected, 403 and SignatureDoesNotMatch", failing)
	}
	series := `flagnetic_sink_batches_total{event_kind="exposure",result="rejected",sink="` + created.Id.String() + `"}`
	if got := testkit.Metric(t, f.srv.Scrape(t), series); got != 1 {
		t.Errorf("%s = %v, want 1", series, got)
	}
	if keys := store.Keys(t, bucket); len(keys) != 0 {
		t.Errorf("objects %v, want none: the PUT was refused", keys)
	}
	if strings.Contains(fmt.Sprintf("%+v", failing), wrong) {
		t.Error("the secret was answered")
	}
}
