package sdk_test

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// sendSample asks the sink for a sample of every kind it exports.
func (f delivering) sendSample(t *testing.T, sink management.Sink) []management.SinkSampleKindResult {
	t.Helper()
	res := do(f.p.admin.SampleSinkWithResponse(t.Context(), f.p.key, sink.Id, management.SinkSample{}))
	if res.JSON200 == nil {
		t.Fatalf("POST sample = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Results
}

// sampleIDs are the IDs of the ten rows of a sample.
func sampleIDs() []string {
	ids := make([]string, 10)
	for i := range ids {
		ids[i] = "sample-" + strconv.Itoa(i+1)
	}
	slices.Sort(ids)
	return ids
}

func TestASampleLandsTenRowsPerKindInTheClickHouseTablesAndMovesNoCursor(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)
	// The sink was never tested or enabled: delivery creates the tables.
	created, _ := f.create(t, clickhouse(ch, db, true))

	for _, r := range f.sendSample(t, created) {
		if !r.Ok {
			t.Fatalf("sample of %s = %+v, want it taken", r.EventKind, r)
		}
	}
	for table, idColumn := range map[string]string{
		"flagnetic_exposures": "event_id", "flagnetic_aliases": "event_id", "flagnetic_evaluation_summaries": "report_id",
	} {
		if got := chCount(t, ch, db, table, false); got != 10 {
			t.Errorf("%s holds %d rows, want the ten of a sample", table, got)
		}
		if got := chStrings(t, ch, db, table, idColumn); !slices.Equal(got, sampleIDs()) {
			t.Errorf("%s.%s = %v, want sample-1 to sample-10", table, idColumn, got)
		}
		if got := chStrings(t, ch, db, table, "project_key"); !slices.Equal(got, []string{f.p.key}) {
			t.Errorf("%s.project_key = %v, want the project's own key", table, got)
		}
		if got := chStrings(t, ch, db, table, "environment_key"); !slices.Equal(got, []string{"sample"}) {
			t.Errorf("%s.environment_key = %v, want sample", table, got)
		}
	}
	if got := chStrings(t, ch, db, "flagnetic_evaluation_summaries", "source"); !slices.Equal(got, []string{"sdk"}) {
		t.Errorf("sample summaries come from %v, want sdk", got)
	}
	if got := f.sink(t, created); got.Enabled || len(got.Streams) != 0 {
		t.Errorf("sink after the sample %+v, want it still disabled with no stream", got)
	}
}

func TestARejectedClickHouseBatchIsDeadLetteredTheNextFlowsAndARedeliveryLandsItsRows(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)
	// unit_id is a number here, so a row whose unit is "u-bad" cannot be
	// parsed and ClickHouse refuses its insert, which a retry cannot cure.
	ch.Exec(t, fmt.Sprintf(`CREATE TABLE %s.flagnetic_exposures (
		schema_version UInt16, event_id String, project_key String, environment_key String,
		unit_id UInt64, received_at DateTime64(3, 'UTC')
	) ENGINE = MergeTree ORDER BY event_id`, db))
	body := clickhouse(ch, db, false, "exposure")
	body.BatchSize = ptr(1)
	created, _ := f.enabled(t, body)
	bad, good := f.exposure("u-bad", "true", f.clock.Now()), f.exposure("7", "true", f.clock.Now())
	f.post(t, bad, good)

	f.rounds(t, 5)
	if got := chStrings(t, ch, db, "flagnetic_exposures", "event_id"); !slices.Equal(got, []string{eventIDOf(good)}) {
		t.Fatalf("rows in ClickHouse %v, want only the batch behind the rejected one", got)
	}
	list := f.deadLetters(t, created)
	if len(list) != 1 || list[0].Status != management.SinkDeadLetterStatusPending || list[0].Attempts != 5 ||
		list[0].LastFailure.Class != management.SinkFailureClass("rejected") {
		t.Fatalf("dead letters %+v, want one pending after five rejections", list)
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthDegraded || s.DeadLetters != 1 {
		t.Errorf("sink %s, want degraded with the dead letter waiting", brief(s))
	}

	// Once the table can take the row, redelivering the dead letter lands it.
	ch.Exec(t, fmt.Sprintf("ALTER TABLE %s.flagnetic_exposures MODIFY COLUMN unit_id String", db))
	if dl := f.redeliver(t, created, list[0]); dl.Status != management.SinkDeadLetterStatusRedelivered {
		t.Errorf("dead letter after the redelivery %+v, want redelivered", dl)
	}
	want := []string{eventIDOf(bad), eventIDOf(good)}
	slices.Sort(want)
	if got := chStrings(t, ch, db, "flagnetic_exposures", "event_id"); !slices.Equal(got, want) {
		t.Errorf("rows in ClickHouse %v, want both events", got)
	}
}

// sampleObjectKey matches the key of a sample's file.
var sampleObjectKey = regexp.MustCompile(`^samples/(exposure|alias|evaluation_summary)/dt=\d{4}-\d{2}-\d{2}/(.+)-sample-\d{8}T\d{6}Z-[0-9a-f]{26}\.jsonl\.gz$`)

func TestASampleWritesOneFilePerKindUnderTheSampleKeyAndMovesNoCursor(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	f := startDelivering(t)
	created, _ := f.create(t, bucketSink(store, store.Endpoint, bucket, "samples"))

	for _, r := range f.sendSample(t, created) {
		if !r.Ok {
			t.Fatalf("sample of %s = %+v, want it taken", r.EventKind, r)
		}
	}
	keys := store.Keys(t, bucket)
	kinds := map[string]bool{}
	for _, key := range keys {
		m := sampleObjectKey.FindStringSubmatch(key)
		if m == nil {
			t.Fatalf("object %q does not follow {prefix}/{event_kind}/dt={day}/{project_key}-sample-{time}-{hash}.jsonl.gz", key)
		}
		if m[2] != f.p.key {
			t.Errorf("%s names project %q, want the project's own key %q", key, m[2], f.p.key)
		}
		kinds[m[1]] = true
		content, _ := store.Object(t, bucket, key)
		rows := fileRows(t, key, content)
		if len(rows) != 10 {
			t.Errorf("%s holds %d rows, want ten", key, len(rows))
		}
		idColumn := "event_id"
		if m[1] == "evaluation_summary" {
			idColumn = "report_id"
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r[idColumn])
		}
		slices.Sort(ids)
		if !slices.Equal(ids, sampleIDs()) {
			t.Errorf("%s holds %v, want sample-1 to sample-10", key, ids)
		}
	}
	if len(keys) != 3 || len(kinds) != 3 {
		t.Errorf("objects %v, want one file for each of the three kinds", keys)
	}
	if got := f.sink(t, created); got.Enabled || len(got.Streams) != 0 {
		t.Errorf("sink after the sample %+v, want it still disabled with no stream", got)
	}
}

func TestARejectedObjectStorageBatchIsDeadLetteredAndItsRedeliveryWritesAnObjectOfItsOwn(t *testing.T) {
	store := testkit.StartObjectStorage(t)
	bucket := store.NewBucket(t)
	f := startDelivering(t)
	body := bucketSink(store, store.Endpoint, bucket, "lake", "exposure")
	body.BatchSize = ptr(1)
	created, _ := f.enabled(t, body)

	// A key the store does not know signs nothing: every PUT is refused with a
	// 403, a rejection.
	const wrong = "rotated-secret-access-key-5528"
	testkit.RecordSecret(wrong)
	f.patch(t, created, management.SinkPatch{ObjectStorage: &management.SinkObjectStorageInput{
		Endpoint: ptr(store.Endpoint), Region: &store.Region, Bucket: &bucket, Prefix: ptr("lake"),
		AccessKeyId: &store.AccessKeyID, SecretAccessKey: ptr(wrong), PathStyle: ptr(true),
	}})
	first := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, first)
	f.rounds(t, 5)
	list := f.deadLetters(t, created)
	if len(list) != 1 || list[0].Status != management.SinkDeadLetterStatusPending || list[0].Attempts != 5 {
		t.Fatalf("dead letters %+v, want one pending after five rejections", list)
	}
	if keys := store.Keys(t, bucket); len(keys) != 0 {
		t.Fatalf("objects %v, want none: every PUT was refused", keys)
	}

	// With the right key again the next batch flows, and the redelivery writes
	// the dead letter's rows beside it, under a hash of its own.
	f.patch(t, created, management.SinkPatch{ObjectStorage: objectStorageInput(store, store.Endpoint, bucket, "lake")})
	next := f.exposure("u-2", "true", f.clock.Now())
	f.post(t, next)
	f.rounds(t, 1)
	if keys := objectKeys(t, store, bucket, "lake"); len(keys) != 1 {
		t.Fatalf("objects %v, want the batch behind the dead letter", keys)
	}
	if dl := f.redeliver(t, created, list[0]); dl.Status != management.SinkDeadLetterStatusRedelivered {
		t.Fatalf("dead letter after the redelivery %+v, want redelivered", dl)
	}
	keys := objectKeys(t, store, bucket, "lake")
	if len(keys) != 2 || keys[0].hash == keys[1].hash {
		t.Fatalf("objects %v, want two, each under a hash of its own", keys)
	}
	ids := map[string]string{}
	for _, k := range keys {
		content, _ := store.Object(t, bucket, k.key)
		for _, id := range fileEventIDs(fileRows(t, k.key, content)) {
			ids[id] = k.hash
		}
	}
	if len(ids) != 2 || ids[eventIDOf(first)] == "" || ids[eventIDOf(next)] == "" || ids[eventIDOf(first)] == ids[eventIDOf(next)] {
		t.Errorf("events by object %v, want each event in an object of its own", ids)
	}
}
