package management_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func clickHouseSink() management.SinkCreate {
	return management.SinkCreate{
		Name: "Warehouse", Kind: "clickhouse",
		Clickhouse: &management.SinkClickHouseInput{
			Url: ptr("http://clickhouse.internal:8123"), Database: ptr("analytics"), Username: ptr("writer"),
			Password: ptr("ch-password-1"),
		},
	}
}

func objectStorageSink() management.SinkCreate {
	return management.SinkCreate{
		Name: "Lake", Kind: "object_storage",
		ObjectStorage: &management.SinkObjectStorageInput{
			Bucket: ptr("exports"), AccessKeyId: ptr("AKIAEXAMPLE"), SecretAccessKey: ptr("s3-secret-key-1"),
		},
	}
}

func webhookSink() management.SinkCreate {
	return management.SinkCreate{
		Name: "Ingest", Kind: "webhook",
		Webhook: &management.SinkWebhookInput{Url: ptr("https://receiver.example/sink")},
	}
}

func createSink(t *testing.T, c *management.ClientWithResponses, project string, body management.SinkCreate) management.SinkWithSecret {
	t.Helper()
	res := do(c.CreateSinkWithResponse(t.Context(), project, body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST sinks = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	if res.JSON201.Secret != nil {
		testkit.RecordSecret(*res.JSON201.Secret)
	}
	return *res.JSON201
}

func updateSink(t *testing.T, c *management.ClientWithResponses, project string, sink management.Sink, body management.SinkPatch) *management.UpdateSinkResponse {
	t.Helper()
	return do(c.UpdateSinkWithResponse(t.Context(), project, sink.Id, &management.UpdateSinkParams{IfMatch: ifMatch(sink.Version)}, body))
}

// storedSink reads the sink as its repository does.
func storedSink(t *testing.T, p management.Project, sink management.Sink) sinks.Sink {
	t.Helper()
	s, err := pgsinks.NewStore(testkit.Pool(t)).Sink(t.Context(), projectIDOf(p), id.FromBytes[sinks.Sink](sink.Id))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// openedSecret returns the secret s holds, opened for its kind's purpose.
func openedSecret(t *testing.T, s sinks.Sink) string {
	t.Helper()
	plain, err := secret.NewKeyring(secret.New(testkit.SecretKey)).Open(s.Kind.SecretPurpose(), s.SealedSecret)
	if err != nil {
		t.Fatalf("opening the secret of the %s sink: %v", s.Kind, err)
	}
	return string(plain)
}

func TestCreatingASinkOfEachKindSealsItsSecretMasksItAndStartsDisabled(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	for name, tc := range map[string]struct {
		body      management.SinkCreate
		plaintext string
		batch     int
		flush     int
	}{
		"clickhouse":     {clickHouseSink(), "ch-password-1", 5_000, 10},
		"object storage": {objectStorageSink(), "s3-secret-key-1", 500_000, 300},
		"webhook":        {webhookSink(), "", 500, 5},
	} {
		testkit.RecordSecret(tc.plaintext)
		created := createSink(t, admin, p.Key, tc.body)

		if created.Enabled || created.Health != management.SinkHealthDisabled || created.Kind != management.SinkKind(tc.body.Kind) ||
			created.BatchSize != tc.batch || created.FlushInterval != tc.flush || created.Version != 1 {
			t.Errorf("%s: new sink %+v", name, created)
		}
		if created.AtRisk || created.LagSeconds != nil || created.DeadLetters != 0 || created.Gaps != 0 || created.StoppedAt != nil ||
			len(created.Streams) != 0 || len(created.Warnings) != 0 || len(created.EventKinds) != 0 || len(created.Environments) != 0 {
			t.Errorf("%s: a sink nothing was delivered to %+v", name, created)
		}
		switch tc.body.Kind {
		case "clickhouse":
			c := created.Clickhouse
			if c == nil || c.Password != "***" || c.TablePrefix != "flagnetic" || !c.CreateTables || c.Url != "http://clickhouse.internal:8123" ||
				created.ObjectStorage != nil || created.Webhook != nil {
				t.Errorf("clickhouse sub-object %+v", created.Clickhouse)
			}
		case "object_storage":
			o := created.ObjectStorage
			if o == nil || o.SecretAccessKey != "***" || o.Format != "jsonl" || o.Compression != "gzip" || created.Clickhouse != nil {
				t.Errorf("object storage sub-object %+v", created.ObjectStorage)
			}
		default:
			if created.Webhook == nil || created.Secret == nil || created.Clickhouse != nil {
				t.Fatalf("webhook sub-object %+v, secret %v", created.Webhook, created.Secret)
			}
			if raw, err := base64.RawURLEncoding.DecodeString(*created.Secret); err != nil || len(raw) != 32 {
				t.Errorf("secret %q: want base64url of 32 random bytes", *created.Secret)
			}
			tc.plaintext = *created.Secret
		}
		if tc.body.Kind != "webhook" && created.Secret != nil {
			t.Errorf("%s: the answer carries a secret %q the member supplied", name, *created.Secret)
		}

		// Stored sealed for its kind's purpose, and for no other.
		stored := storedSink(t, p, sinkOf(created))
		if strings.Contains(stored.SealedSecret, tc.plaintext) || openedSecret(t, stored) != tc.plaintext {
			t.Errorf("%s: stored %q, opening it = %q, want %q", name, stored.SealedSecret, openedSecret(t, stored), tc.plaintext)
		}
		for _, other := range sinks.Kinds {
			if other == stored.Kind {
				continue
			}
			if _, err := secret.NewKeyring(secret.New(testkit.SecretKey)).Open(other.SecretPurpose(), stored.SealedSecret); err == nil {
				t.Errorf("%s: the secret opens as a %s sink's", name, other)
			}
		}

		one := do(admin.GetSinkWithResponse(t.Context(), p.Key, created.Id))
		if one.JSON200 == nil || one.JSON200.Id != created.Id || one.HTTPResponse.Header.Get("ETag") != `"1"` ||
			(tc.plaintext != "" && strings.Contains(string(one.Body), tc.plaintext)) {
			t.Errorf("%s: get = %d: %s", name, one.StatusCode(), one.Body)
		}

		entries := auditOf(t, srv, "sink.created", created.Id.String())
		if len(entries) != 1 {
			t.Fatalf("%s: %d sink.created entries, want 1", name, len(entries))
		}
		raw, _ := json.Marshal(entries[0])
		after, _ := entries[0].After.(map[string]any)
		sub, _ := after[tc.body.Kind].(map[string]any)
		field := map[string]string{"clickhouse": "password", "object_storage": "secret_access_key", "webhook": "secret"}[tc.body.Kind]
		if entries[0].Entity.Kind != "sink" || entries[0].Before != nil || sub[field] != "[redacted]" || strings.Contains(string(raw), tc.plaintext) {
			t.Errorf("%s: audit entry %s", name, raw)
		}
	}

	list := do(admin.ListSinksWithResponse(t.Context(), p.Key))
	if list.JSON200 == nil || len(list.JSON200.Items) != 3 || strings.Contains(string(list.Body), "ch-password-1") ||
		strings.Contains(string(list.Body), "s3-secret-key-1") {
		t.Errorf("list = %d: %s", list.StatusCode(), list.Body)
	}
	if list.JSON200 != nil && !slices.IsSortedFunc(list.JSON200.Items, func(a, b management.Sink) int { return strings.Compare(a.Id.String(), b.Id.String()) }) {
		t.Error("the list is not oldest first")
	}
}

// sinkOf returns s as a plain Sink, which the helpers take.
func sinkOf(s management.SinkWithSecret) management.Sink {
	return management.Sink{
		Id: s.Id, Name: s.Name, Kind: s.Kind, Clickhouse: s.Clickhouse, ObjectStorage: s.ObjectStorage, Webhook: s.Webhook,
		EventKinds: s.EventKinds, Environments: s.Environments, BatchSize: s.BatchSize, FlushInterval: s.FlushInterval,
		Enabled: s.Enabled, Health: s.Health, Version: s.Version, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func TestAMaskedOrAbsentSecretKeepsTheStoredOneAndANewOneReplacesIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	testkit.RecordSecret("ch-password-1")
	testkit.RecordSecret("ch-password-2")
	created := sinkOf(createSink(t, admin, p.Key, clickHouseSink()))
	sealed := storedSink(t, p, created).SealedSecret

	// "***", as every read answers it, keeps the password.
	masked := updateSink(t, admin, p.Key, created, management.SinkPatch{Clickhouse: &management.SinkClickHouseInput{
		Url: ptr("http://clickhouse.internal:8124"), Database: ptr("analytics"), Username: ptr("writer"), Password: ptr("***"),
	}})
	if masked.JSON200 == nil || masked.JSON200.Clickhouse.Url != "http://clickhouse.internal:8124" || masked.JSON200.Clickhouse.Password != "***" ||
		masked.JSON200.Version != 2 || masked.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("a masked password = %d: %s", masked.StatusCode(), masked.Body)
	}
	if got := storedSink(t, p, created).SealedSecret; got != sealed {
		t.Errorf("the masked edit changed the sealed secret: %q -> %q", sealed, got)
	}

	// So does leaving it out.
	omitted := updateSink(t, admin, p.Key, *masked.JSON200, management.SinkPatch{Clickhouse: &management.SinkClickHouseInput{
		Url: ptr("http://clickhouse.internal:8125"), Database: ptr("analytics"), Username: ptr("writer"),
	}})
	if omitted.JSON200 == nil {
		t.Fatalf("an absent password = %d: %s", omitted.StatusCode(), omitted.Body)
	}
	if got := storedSink(t, p, created).SealedSecret; got != sealed {
		t.Errorf("the edit without a password changed the sealed secret: %q -> %q", sealed, got)
	}

	// A new value replaces it.
	replaced := updateSink(t, admin, p.Key, *omitted.JSON200, management.SinkPatch{Clickhouse: &management.SinkClickHouseInput{
		Url: ptr("http://clickhouse.internal:8125"), Database: ptr("analytics"), Username: ptr("writer"), Password: ptr("ch-password-2"),
	}})
	if replaced.JSON200 == nil || replaced.JSON200.Clickhouse.Password != "***" {
		t.Fatalf("a new password = %d: %s", replaced.StatusCode(), replaced.Body)
	}
	if s := storedSink(t, p, created); s.SealedSecret == sealed || openedSecret(t, s) != "ch-password-2" {
		t.Errorf("after a new password the stored secret opens as %q", openedSecret(t, s))
	}

	// Nothing the edits recorded holds a secret, the old ones included.
	for _, e := range auditOf(t, srv, "sink.updated", created.Id.String()) {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "ch-password") {
			t.Errorf("an audit entry holds a password: %s", raw)
		}
	}
}

func TestAnEditReplacesAConfigurationWholeAndAnEditChangingNothingRecordsNothing(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	body := clickHouseSink()
	body.Clickhouse.TablePrefix = ptr("events")
	body.Clickhouse.CreateTables = ptr(false)
	body.Clickhouse.Settings = &map[string]string{"max_threads": "2"}
	created := sinkOf(createSink(t, admin, p.Key, body))

	// Naming the sub-object again without them takes their defaults: nothing
	// of the earlier configuration lingers.
	res := updateSink(t, admin, p.Key, created, management.SinkPatch{Clickhouse: &management.SinkClickHouseInput{
		Url: ptr("http://clickhouse.internal:8123"), Database: ptr("analytics"), Username: ptr("writer"),
	}})
	if res.JSON200 == nil {
		t.Fatalf("edit = %d: %s", res.StatusCode(), res.Body)
	}
	if c := res.JSON200.Clickhouse; c.TablePrefix != "flagnetic" || !c.CreateTables || len(c.Settings) != 0 {
		t.Errorf("the replaced configuration %+v kept something of the earlier one", c)
	}

	// An edit leaving the sub-object out leaves it as it is.
	renamed := updateSink(t, admin, p.Key, *res.JSON200, management.SinkPatch{Name: ptr("Renamed")})
	if renamed.JSON200 == nil || renamed.JSON200.Name != "Renamed" || renamed.JSON200.Clickhouse.Url != "http://clickhouse.internal:8123" {
		t.Fatalf("a rename = %d: %s", renamed.StatusCode(), renamed.Body)
	}

	same := updateSink(t, admin, p.Key, *renamed.JSON200, management.SinkPatch{Name: ptr("Renamed"), Enabled: ptr(false)})
	if same.JSON200 == nil || same.JSON200.Version != renamed.JSON200.Version {
		t.Errorf("an edit changing nothing = %d: %s, want the version to stay %d", same.StatusCode(), same.Body, renamed.JSON200.Version)
	}
	if n := len(auditOf(t, srv, "sink.updated", created.Id.String())); n != 2 {
		t.Errorf("%d sink.updated entries, want 2: the no-op edit recorded one", n)
	}
}

func TestASinksKindNeverChanges(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := sinkOf(createSink(t, admin, p.Key, webhookSink()))

	for name, patch := range map[string]management.SinkPatch{
		"another kind named":          {Kind: ptr("clickhouse")},
		"another kind's sub-object":   {ObjectStorage: objectStorageSink().ObjectStorage},
		"its own beside another kind": {Webhook: webhookSink().Webhook, Clickhouse: clickHouseSink().Clickhouse},
	} {
		res := updateSink(t, admin, p.Key, created, patch)
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "kind_immutable")
	}
	if res := updateSink(t, admin, p.Key, created, management.SinkPatch{Kind: ptr("webhook"), Name: ptr("Same kind")}); res.JSON200 == nil {
		t.Errorf("naming its own kind = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestEnablingASinkIsAnOperationOfItsOwnAndAnEditOnlyDisables(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := sinkOf(createSink(t, admin, p.Key, webhookSink()))

	res := updateSink(t, admin, p.Key, created, management.SinkPatch{Enabled: ptr(true)})
	pr := assertProblem(t, "enabling by an edit", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_sink")
	if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != "/enabled" {
		t.Errorf("violations %+v", pr.Errors)
	}
}

func TestEverySinkWriteNeedsTheSinksCurrentVersion(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := sinkOf(createSink(t, admin, p.Key, webhookSink()))

	patch := management.SinkPatch{Name: ptr("Renamed")}
	missing := do(admin.UpdateSinkWithResponse(t.Context(), p.Key, created.Id, &management.UpdateSinkParams{}, patch))
	assertProblem(t, "an edit without If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := do(admin.UpdateSinkWithResponse(t.Context(), p.Key, created.Id, &management.UpdateSinkParams{IfMatch: ifMatch(7)}, patch))
	assertProblem(t, "an edit of a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")

	noDelete := do(admin.DeleteSinkWithResponse(t.Context(), p.Key, created.Id, &management.DeleteSinkParams{}))
	assertProblem(t, "a delete without If-Match", noDelete.StatusCode(), noDelete.Body, http.StatusPreconditionRequired, "precondition_required")
	staleDelete := do(admin.DeleteSinkWithResponse(t.Context(), p.Key, created.Id, &management.DeleteSinkParams{IfMatch: ifMatch(7)}))
	assertProblem(t, "a delete of a stale version", staleDelete.StatusCode(), staleDelete.Body, http.StatusPreconditionFailed, "version_mismatch")
	if got := do(admin.GetSinkWithResponse(t.Context(), p.Key, created.Id)); got.JSON200 == nil || got.JSON200.Name != "Ingest" {
		t.Errorf("a refused write changed the sink: %d %s", got.StatusCode(), got.Body)
	}

	// The precondition is judged after the request is validated.
	invalid := do(admin.UpdateSinkWithResponse(t.Context(), p.Key, created.Id, &management.UpdateSinkParams{IfMatch: ifMatch(7)},
		management.SinkPatch{Name: ptr(" ")}))
	assertProblem(t, "an invalid edit of a stale version", invalid.StatusCode(), invalid.Body, http.StatusUnprocessableEntity, "invalid_sink")

	deleted := do(admin.DeleteSinkWithResponse(t.Context(), p.Key, created.Id, &management.DeleteSinkParams{IfMatch: ifMatch(1)}))
	status(t, "the delete", deleted.StatusCode(), deleted.Body, http.StatusNoContent)
	gone := do(admin.GetSinkWithResponse(t.Context(), p.Key, created.Id))
	assertProblem(t, "reading the deleted sink", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	again := do(admin.DeleteSinkWithResponse(t.Context(), p.Key, created.Id, &management.DeleteSinkParams{IfMatch: ifMatch(1)}))
	assertProblem(t, "deleting it again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")

	entries := auditOf(t, srv, "sink.deleted", created.Id.String())
	if len(entries) != 1 || entries[0].After != nil || entries[0].Before == nil {
		t.Errorf("sink.deleted entries %+v, want one with only a before", entries)
	}
}

func TestASinkOfAnotherProjectIsNotFound(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mine, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	created := createSink(t, admin, mine.Key, webhookSink())

	got := do(admin.GetSinkWithResponse(t.Context(), other.Key, created.Id))
	assertProblem(t, "reading it in another project", got.StatusCode(), got.Body, http.StatusNotFound, "not_found")
	if list := do(admin.ListSinksWithResponse(t.Context(), other.Key)); list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Errorf("another project lists %s", list.Body)
	}
}

func TestASinkIsRefusedAnEnvironmentTheProjectDoesNotHave(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	body := webhookSink()
	body.Environments = &[]string{"production", "nowhere"}
	res := do(admin.CreateSinkWithResponse(t.Context(), p.Key, body))
	pr := assertProblem(t, "an unknown environment", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "unknown_environment")
	if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != "/environments/1" {
		t.Errorf("violations %+v", pr.Errors)
	}

	body.Environments = &[]string{"production"}
	created := sinkOf(createSink(t, admin, p.Key, body))
	res2 := updateSink(t, admin, p.Key, created, management.SinkPatch{Environments: &[]string{"nowhere"}})
	assertProblem(t, "an edit naming an unknown environment", res2.StatusCode(), res2.Body, http.StatusUnprocessableEntity, "unknown_environment")
	if list := do(admin.ListSinksWithResponse(t.Context(), p.Key)); list.JSON200 == nil || len(list.JSON200.Items) != 1 {
		t.Errorf("refused writes left %s", list.Body)
	}
}

func TestASinkStaysEditableAfterAnEnvironmentItNamesIsDeleted(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	body := webhookSink()
	body.Environments = &[]string{"staging", "production"}
	created := sinkOf(createSink(t, admin, p.Key, body))
	deleted := do(admin.DeleteEnvironmentWithResponse(t.Context(), p.Key, "staging", &management.DeleteEnvironmentParams{IfMatch: ifMatch(1)}))
	status(t, "delete staging", deleted.StatusCode(), deleted.Body, http.StatusNoContent)

	renamed := updateSink(t, admin, p.Key, created, management.SinkPatch{Name: ptr("Security")})
	if renamed.JSON200 == nil || renamed.JSON200.Name != "Security" {
		t.Fatalf("an edit leaving the environments alone = %d: %s", renamed.StatusCode(), renamed.Body)
	}
	again := updateSink(t, admin, p.Key, *renamed.JSON200, management.SinkPatch{Environments: &[]string{"production", "staging"}})
	assertProblem(t, "the deleted environment named again", again.StatusCode(), again.Body, http.StatusUnprocessableEntity, "unknown_environment")
}

// problemViolations returns the pointer-to-code map of the violations a problem names.
func problemViolations(pr management.Problem) map[string]string {
	out := map[string]string{}
	if pr.Errors != nil {
		for _, e := range *pr.Errors {
			out[e.Pointer] = e.Code
		}
	}
	return out
}

func TestASinkOfEachKindRefusesWhatCannotWorkNamingEachField(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	for name, tc := range map[string]struct {
		body management.SinkCreate
		want map[string]string
	}{
		"clickhouse with nothing": {
			management.SinkCreate{Name: "CH", Kind: "clickhouse", Clickhouse: &management.SinkClickHouseInput{}},
			map[string]string{
				"/clickhouse/url": "required", "/clickhouse/database": "required", "/clickhouse/username": "required",
				"/clickhouse/password": "required",
			},
		},
		"clickhouse without its configuration": {
			management.SinkCreate{Name: "CH", Kind: "clickhouse"},
			map[string]string{"/clickhouse": "required", "/clickhouse/password": "required"},
		},
		"clickhouse with a masked password to keep, nothing being stored yet": {
			func() management.SinkCreate { b := clickHouseSink(); b.Clickhouse.Password = ptr("***"); return b }(),
			map[string]string{"/clickhouse/password": "required"},
		},
		"clickhouse with a password in its url": {
			func() management.SinkCreate {
				b := clickHouseSink()
				b.Clickhouse.Url = ptr("http://u:p@ch:8123")
				return b
			}(),
			map[string]string{"/clickhouse/url": "invalid_url"},
		},
		"clickhouse with a setting delivery sets": {
			func() management.SinkCreate {
				b := clickHouseSink()
				b.Clickhouse.Settings = &map[string]string{"async_insert": "1"}
				return b
			}(),
			map[string]string{"/clickhouse/settings/async_insert": "reserved"},
		},
		"object storage with nothing": {
			management.SinkCreate{Name: "S3", Kind: "object_storage", ObjectStorage: &management.SinkObjectStorageInput{}},
			map[string]string{
				"/object_storage/bucket": "required", "/object_storage/access_key_id": "required",
				"/object_storage/secret_access_key": "required",
			},
		},
		"object storage with a format nobody reads": {
			func() management.SinkCreate { b := objectStorageSink(); b.ObjectStorage.Format = ptr("xml"); return b }(),
			map[string]string{"/object_storage/format": "invalid_format"},
		},
		"object storage with gzip parquet": {
			func() management.SinkCreate {
				b := objectStorageSink()
				b.ObjectStorage.Format, b.ObjectStorage.Compression = ptr("parquet"), ptr("gzip")
				return b
			}(),
			map[string]string{"/object_storage/compression": "invalid_compression"},
		},
		"webhook without a url": {
			management.SinkCreate{Name: "Hook", Kind: "webhook", Webhook: &management.SinkWebhookInput{}},
			map[string]string{"/webhook/url": "required"},
		},
		"webhook with a header of ours": {
			func() management.SinkCreate {
				b := webhookSink()
				b.Webhook.Headers = &map[string]string{"X-Flagnetic-Signature": "v1=00"}
				return b
			}(),
			map[string]string{"/webhook/headers/X-Flagnetic-Signature": "reserved"},
		},
		"a kind we do not have": {
			management.SinkCreate{Name: "K", Kind: "kafka"}, map[string]string{"/kind": "invalid_kind"},
		},
		"the configuration of another kind": {
			func() management.SinkCreate { b := webhookSink(); b.Clickhouse = clickHouseSink().Clickhouse; return b }(),
			map[string]string{"/clickhouse": "not_allowed"},
		},
		"a blank name": {
			func() management.SinkCreate { b := webhookSink(); b.Name = "  "; return b }(),
			map[string]string{"/name": "invalid_name"},
		},
		"an event kind nobody exports": {
			func() management.SinkCreate {
				b := webhookSink()
				b.EventKinds = &[]string{"exposure", "click"}
				return b
			}(),
			map[string]string{"/event_kinds/1": "invalid_event_kind"},
		},
	} {
		res := do(admin.CreateSinkWithResponse(t.Context(), p.Key, tc.body))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_sink")
		if got := problemViolations(pr); !equalViolations(got, tc.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.want)
		}
	}
	if list := do(admin.ListSinksWithResponse(t.Context(), p.Key)); list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Errorf("refused creates left %s", list.Body)
	}
}

func equalViolations(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestBatchingTakesItsKindsDefaultsAndRefusesWhatIsOutOfItsBounds(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	for name, tc := range map[string]struct {
		body    management.SinkCreate
		batch   *int
		flush   *int
		pointer string
	}{
		"object storage flushing every half minute": {objectStorageSink(), nil, ptr(30), "/flush_interval"},
		"object storage flushing every two hours":   {objectStorageSink(), nil, ptr(7_201), "/flush_interval"},
		"a webhook batch of 5,001":                  {webhookSink(), ptr(5_001), nil, "/batch_size"},
		"a batch of none":                           {clickHouseSink(), ptr(0), nil, ""},
		"a negative flush interval":                 {clickHouseSink(), nil, ptr(-1), "/flush_interval"},
	} {
		tc.body.BatchSize, tc.body.FlushInterval = tc.batch, tc.flush
		res := do(admin.CreateSinkWithResponse(t.Context(), p.Key, tc.body))
		if tc.pointer == "" { // zero means the default
			if res.JSON201 == nil || res.JSON201.BatchSize != 5_000 {
				t.Errorf("%s: %d %s, want the default", name, res.StatusCode(), res.Body)
			}
			continue
		}
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_sink")
		if got := problemViolations(pr); got[tc.pointer] != "out_of_range" {
			t.Errorf("%s: violations %v, want out_of_range at %s", name, got, tc.pointer)
		}
	}

	custom := objectStorageSink()
	custom.BatchSize, custom.FlushInterval = ptr(1_000_000), ptr(3_600)
	if created := createSink(t, admin, p.Key, custom); created.BatchSize != 1_000_000 || created.FlushInterval != 3_600 {
		t.Errorf("batching %d every %ds", created.BatchSize, created.FlushInterval)
	}
}

func TestEventKindsAndEnvironmentsLoseTheirRepeats(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	body := webhookSink()
	body.EventKinds = &[]string{"alias", "exposure", "alias"}
	body.Environments = &[]string{"staging", "staging"}
	created := createSink(t, admin, p.Key, body)
	if !slices.Equal(created.EventKinds, []management.SinkEventKind{"alias", "exposure"}) || !slices.Equal(created.Environments, []string{"staging"}) {
		t.Errorf("event kinds %v, environments %v", created.EventKinds, created.Environments)
	}
}

func TestATargetASinkMayNotReachIsRefusedByKind(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	webhookAt := func(url string) management.SinkCreate {
		b := webhookSink()
		b.Webhook.Url = &url
		return b
	}
	clickHouseAt := func(url string) management.SinkCreate {
		b := clickHouseSink()
		b.Clickhouse.Url = &url
		return b
	}
	objectStorageAt := func(url string) management.SinkCreate {
		b := objectStorageSink()
		b.ObjectStorage.Endpoint = &url
		return b
	}
	for name, tc := range map[string]struct {
		body    management.SinkCreate
		code    string
		pointer string
	}{
		"a webhook over plain http":              {webhookAt("http://receiver.example/hook"), "insecure_url", "/webhook/url"},
		"a webhook on loopback":                  {webhookAt("https://127.0.0.1:9/hook"), "private_network_target", "/webhook/url"},
		"a webhook on IPv6 loopback":             {webhookAt("https://[::1]:9/hook"), "private_network_target", "/webhook/url"},
		"a webhook named localhost":              {webhookAt("https://localhost:9/hook"), "private_network_target", "/webhook/url"},
		"a webhook on a private network":         {webhookAt("https://10.0.0.1/hook"), "private_network_target", "/webhook/url"},
		"a webhook on a link-local address":      {webhookAt("https://169.254.169.254/latest"), "private_network_target", "/webhook/url"},
		"clickhouse on the metadata address":     {clickHouseAt("http://169.254.169.254:8123"), "link_local_target", "/clickhouse/url"},
		"clickhouse on an IPv6 link-local":       {clickHouseAt("http://[fe80::1]:8123"), "link_local_target", "/clickhouse/url"},
		"clickhouse on a mapped link-local":      {clickHouseAt("http://[::ffff:169.254.169.254]:8123"), "link_local_target", "/clickhouse/url"},
		"object storage on the metadata address": {objectStorageAt("http://169.254.169.254"), "link_local_target", "/object_storage/endpoint"},
	} {
		res := do(admin.CreateSinkWithResponse(t.Context(), p.Key, tc.body))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, tc.code)
		if got := problemViolations(pr); got[tc.pointer] != tc.code {
			t.Errorf("%s: violations %v, want %s at %s", name, got, tc.code, tc.pointer)
		}
	}

	// ClickHouse and object storage sit in the cluster's own network.
	for name, body := range map[string]management.SinkCreate{
		"clickhouse on loopback":              clickHouseAt("http://127.0.0.1:8123"),
		"clickhouse named localhost":          clickHouseAt("http://localhost:8123"),
		"clickhouse on a private network":     clickHouseAt("http://10.0.0.5:8123"),
		"object storage on IPv6 loopback":     objectStorageAt("http://[::1]:9000"),
		"object storage on a private network": objectStorageAt("http://192.168.1.10:9000"),
		"object storage on AWS (no endpoint)": objectStorageSink(),
	} {
		if res := do(admin.CreateSinkWithResponse(t.Context(), p.Key, body)); res.JSON201 == nil {
			t.Errorf("%s = %d: %s, want it accepted", name, res.StatusCode(), res.Body)
		}
	}

	// A refused edit changes nothing.
	created := sinkOf(createSink(t, admin, p.Key, webhookSink()))
	bad := "https://10.0.0.1/hook"
	res := updateSink(t, admin, p.Key, created, management.SinkPatch{Webhook: &management.SinkWebhookInput{Url: &bad}})
	assertProblem(t, "an edit to a private target", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "private_network_target")
	linkLocal := "http://169.254.169.254:8123"
	ch := sinkOf(createSink(t, admin, p.Key, clickHouseSink()))
	res = updateSink(t, admin, p.Key, ch, management.SinkPatch{Clickhouse: &management.SinkClickHouseInput{
		Url: &linkLocal, Database: ptr("analytics"), Username: ptr("writer"),
	}})
	assertProblem(t, "an edit to a link-local target", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "link_local_target")
}

func TestTheOperatorsPolicyLiftsWhatItNamesForWebhookSinksAndNeverTheLinkLocalBan(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithWebhookTargets(governance.TargetPolicy{AllowHTTP: true, AllowPrivateNetworks: true}))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	body := webhookSink()
	body.Webhook.Url = ptr("http://127.0.0.1:9/hook")
	if created := createSink(t, admin, p.Key, body); created.Webhook.Url != "http://127.0.0.1:9/hook" {
		t.Errorf("url %q", created.Webhook.Url)
	}
	ch := clickHouseSink()
	ch.Clickhouse.Url = ptr("http://169.254.169.254:8123")
	res := do(admin.CreateSinkWithResponse(t.Context(), p.Key, ch))
	assertProblem(t, "link-local with private networks allowed", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "link_local_target")
}

func TestOnlyAWebhookSinksSecretIsRotatedAndTheOldOneSignsBesideTheNewForTheOverlap(t *testing.T) {
	clock := testkit.Clock()
	now := clock.Now()
	srv := testkit.StartServer(t, testkit.WithClock(clock))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := createSink(t, admin, p.Key, webhookSink())
	first := *created.Secret

	res := do(admin.RotateSinkSecretWithResponse(t.Context(), p.Key, created.Id))
	if res.JSON200 == nil || res.JSON200.Secret == nil || *res.JSON200.Secret == first || res.JSON200.Version != 2 ||
		res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("rotate = %d: %s", res.StatusCode(), res.Body)
	}
	second := *res.JSON200.Secret
	testkit.RecordSecret(second)
	if raw, err := base64.RawURLEncoding.DecodeString(second); err != nil || len(raw) != 32 {
		t.Errorf("secret %q: want base64url of 32 random bytes", second)
	}

	stored := storedSink(t, p, sinkOf(created))
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	previous, err := keyring.Open(secret.PurposeSinkWebhookSecret, stored.PreviousSealedSecret)
	if openedSecret(t, stored) != second || err != nil || string(previous) != first || stored.PreviousSecretExpiresAt == nil ||
		!stored.PreviousSecretExpiresAt.After(now) {
		t.Errorf("after a rotation the sink signs with %q beside %q until %v", openedSecret(t, stored), previous, stored.PreviousSecretExpiresAt)
	}

	// Only two secrets sign: the one an earlier rotation left is dropped.
	again := do(admin.RotateSinkSecretWithResponse(t.Context(), p.Key, created.Id))
	if again.JSON200 == nil || again.JSON200.Secret == nil {
		t.Fatalf("a second rotation = %d: %s", again.StatusCode(), again.Body)
	}
	testkit.RecordSecret(*again.JSON200.Secret)
	stored = storedSink(t, p, sinkOf(created))
	if previous, err = keyring.Open(secret.PurposeSinkWebhookSecret, stored.PreviousSealedSecret); err != nil || string(previous) != second {
		t.Errorf("after a second rotation the previous secret opens as %q, %v, want the first rotation's", previous, err)
	}

	// Neither answer nor audit entry shows a secret again.
	if got := do(admin.GetSinkWithResponse(t.Context(), p.Key, created.Id)); strings.Contains(string(got.Body), second) {
		t.Errorf("a read shows the secret: %s", got.Body)
	}
	entries := auditOf(t, srv, "sink.secret_rotated", created.Id.String())
	if len(entries) != 2 {
		t.Fatalf("%d sink.secret_rotated entries, want 2", len(entries))
	}
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		for _, s := range []string{first, second, *again.JSON200.Secret} {
			if strings.Contains(string(raw), s) {
				t.Errorf("a rotation entry holds a secret: %s", raw)
			}
		}
	}

	for name, body := range map[string]management.SinkCreate{"clickhouse": clickHouseSink(), "object storage": objectStorageSink()} {
		other := createSink(t, admin, p.Key, body)
		refused := do(admin.RotateSinkSecretWithResponse(t.Context(), p.Key, other.Id))
		assertProblem(t, name, refused.StatusCode(), refused.Body, http.StatusUnprocessableEntity, "no_secret_to_rotate")
	}
	gone := do(admin.RotateSinkSecretWithResponse(t.Context(), p.Key, [16]byte{1}))
	assertProblem(t, "a sink that does not exist", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
}

func TestOnlyAMaintainerReadsAndManagesSinks(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := createSink(t, admin, p.Key, webhookSink())

	for _, role := range []access.RoleName{access.RoleViewer, access.RoleDeveloper} {
		c := testkit.Client(t, srv, testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(p), role))
		for name, res := range map[string]interface {
			StatusCode() int
		}{
			"list": do(c.ListSinksWithResponse(t.Context(), p.Key)),
			"get":  do(c.GetSinkWithResponse(t.Context(), p.Key, created.Id)),
		} {
			if res.StatusCode() != http.StatusForbidden {
				t.Errorf("a %s %s of sinks = %d, want 403", role, name, res.StatusCode())
			}
		}
	}
	maintainer := testkit.Client(t, srv, testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(p), access.RoleMaintainer))
	if res := do(maintainer.ListSinksWithResponse(t.Context(), p.Key)); res.JSON200 == nil || len(res.JSON200.Items) != 1 {
		t.Errorf("a maintainer lists %d: %s", res.StatusCode(), res.Body)
	}
}

func TestDeletingAProjectDeletesItsSinks(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createSink(t, admin, p.Key, webhookSink())
	createSink(t, admin, p.Key, clickHouseSink())
	owner, _ := grantedSession(t, srv, p, access.RoleOwner)

	if res := deleteProject(t, owner.ClientWithResponses, p.Key, p.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE project = %d: %s", res.StatusCode(), res.Body)
	}
	left, err := pgsinks.NewStore(testkit.Pool(t)).Sinks(t.Context(), projectIDOf(p))
	if err != nil || len(left) != 0 {
		t.Errorf("the deleted project still has sinks: %v, %v", left, err)
	}
}
