package sinks_test

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

var environments = []string{"production", "staging"}

func clickHouse() sinks.Draft {
	return sinks.Draft{
		Input: sinks.Input{
			Name: "Warehouse", Kind: sinks.KindClickHouse,
			Config: sinks.Config{ClickHouse: &sinks.ClickHouse{
				URL: "http://clickhouse.internal:8123", Database: "analytics", Username: "writer", CreateTables: true,
			}},
		},
		Secret: "ch-password",
	}
}

func objectStorage() sinks.Draft {
	return sinks.Draft{
		Input: sinks.Input{
			Name: "Lake", Kind: sinks.KindObjectStorage,
			Config: sinks.Config{ObjectStorage: &sinks.ObjectStorage{Bucket: "exports", AccessKeyID: "AKIAEXAMPLE"}},
		},
		Secret: "s3-secret",
	}
}

func webhook() sinks.Draft {
	return sinks.Draft{
		Input: sinks.Input{
			Name: "Ingest", Kind: sinks.KindWebhook,
			Config: sinks.Config{Webhook: &sinks.Webhook{URL: "https://receiver.example/sink"}},
		},
	}
}

// violations returns the pointer and code of each violation err names, or
// fails the test when err is not an invalid_sink refusal.
func violations(t *testing.T, err error) map[string]string {
	t.Helper()
	var e *apperrors.Error
	if !errors.As(err, &e) || e.Kind != apperrors.KindInvalid || e.Code != "invalid_sink" {
		t.Fatalf("error = %v, want an invalid_sink refusal", err)
	}
	out := map[string]string{}
	for _, v := range e.Violations {
		out[v.Pointer] = v.Code
	}
	return out
}

func TestACheckedSinkOfEachKindCarriesTheKindsDefaults(t *testing.T) {
	for name, tc := range map[string]struct {
		draft sinks.Draft
		batch int
		flush time.Duration
	}{
		"clickhouse":     {clickHouse(), 5_000, 10 * time.Second},
		"object storage": {objectStorage(), 500_000, 5 * time.Minute},
		"webhook":        {webhook(), 500, 5 * time.Second},
	} {
		got, err := sinks.Check(tc.draft, environments, governance.TargetPolicy{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.BatchSize != tc.batch || got.FlushInterval != tc.flush {
			t.Errorf("%s: batching = %d every %s, want %d every %s", name, got.BatchSize, got.FlushInterval, tc.batch, tc.flush)
		}
	}
}

func TestAClickHouseSinkTakesItsDefaultsAndRefusesWhatCannotWork(t *testing.T) {
	ok, err := sinks.Check(clickHouse(), environments, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ok.Config.ClickHouse.TablePrefix; got != "flagnetic" {
		t.Errorf("table prefix = %q, want the default flagnetic", got)
	}

	for name, tc := range map[string]struct {
		edit func(*sinks.Draft)
		want map[string]string
	}{
		"nothing": {func(d *sinks.Draft) { d.Config.ClickHouse = &sinks.ClickHouse{}; d.Secret = "" }, map[string]string{
			"/clickhouse/url": "required", "/clickhouse/database": "required", "/clickhouse/username": "required",
			"/clickhouse/password": "required",
		}},
		"a url that is not http": {func(d *sinks.Draft) { d.Config.ClickHouse.URL = "tcp://clickhouse:9000" },
			map[string]string{"/clickhouse/url": "invalid_url"}},
		"a password in the url": {func(d *sinks.Draft) { d.Config.ClickHouse.URL = "http://u:p@clickhouse:8123" },
			map[string]string{"/clickhouse/url": "invalid_url"}},
		"a table prefix that needs quoting": {func(d *sinks.Draft) { d.Config.ClickHouse.TablePrefix = "1-flags" },
			map[string]string{"/clickhouse/table_prefix": "invalid_table_prefix"}},
		"a setting delivery sets itself": {func(d *sinks.Draft) {
			d.Config.ClickHouse.Settings = map[string]string{"async_insert": "1", "max_threads": "2"}
		}, map[string]string{"/clickhouse/settings/async_insert": "reserved"}},
		"a masked password on a create": {func(d *sinks.Draft) { d.Secret = sinks.Masked },
			map[string]string{"/clickhouse/password": "required"}},
		"another kind's configuration": {func(d *sinks.Draft) { d.Config.Webhook = &sinks.Webhook{URL: "https://r.example"} },
			map[string]string{"/webhook": "not_allowed"}},
	} {
		d := clickHouse()
		tc.edit(&d)
		_, err := sinks.Check(d, environments, governance.TargetPolicy{})
		if got := violations(t, err); !sameViolations(got, tc.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.want)
		}
	}
}

func TestAnObjectStorageSinkTakesItsDefaultsAndRefusesWhatCannotWork(t *testing.T) {
	ok, err := sinks.Check(objectStorage(), environments, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if c := ok.Config.ObjectStorage; c.Format != "jsonl" || c.Compression != "gzip" {
		t.Errorf("format = %q, compression = %q, want jsonl and gzip", c.Format, c.Compression)
	}
	parquet := objectStorage()
	parquet.Config.ObjectStorage.Format = "parquet"
	ok, err = sinks.Check(parquet, environments, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ok.Config.ObjectStorage.Compression; got != "zstd" {
		t.Errorf("a parquet sink's compression = %q, want zstd", got)
	}

	for name, tc := range map[string]struct {
		edit func(*sinks.Draft)
		want map[string]string
	}{
		"nothing": {func(d *sinks.Draft) { d.Config.ObjectStorage = &sinks.ObjectStorage{}; d.Secret = "" }, map[string]string{
			"/object_storage/bucket": "required", "/object_storage/access_key_id": "required",
			"/object_storage/secret_access_key": "required",
		}},
		"an endpoint with a path": {func(d *sinks.Draft) { d.Config.ObjectStorage.Endpoint = "https://s3.example/bucket" },
			map[string]string{"/object_storage/endpoint": "invalid_endpoint"}},
		"an endpoint with credentials": {func(d *sinks.Draft) { d.Config.ObjectStorage.Endpoint = "https://k:s@s3.example" },
			map[string]string{"/object_storage/endpoint": "invalid_endpoint"}},
		"a format nobody reads": {func(d *sinks.Draft) { d.Config.ObjectStorage.Format = "xml" },
			map[string]string{"/object_storage/format": "invalid_format"}},
		"a compression nobody writes": {func(d *sinks.Draft) { d.Config.ObjectStorage.Compression = "bzip2" },
			map[string]string{"/object_storage/compression": "invalid_compression"}},
		"gzip with parquet": {func(d *sinks.Draft) {
			d.Config.ObjectStorage.Format, d.Config.ObjectStorage.Compression = "parquet", "gzip"
		}, map[string]string{"/object_storage/compression": "invalid_compression"}},
		"a bucket with a slash": {func(d *sinks.Draft) { d.Config.ObjectStorage.Bucket = "a/b" },
			map[string]string{"/object_storage/bucket": "invalid_bucket"}},
	} {
		d := objectStorage()
		tc.edit(&d)
		_, err := sinks.Check(d, environments, governance.TargetPolicy{})
		if got := violations(t, err); !sameViolations(got, tc.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.want)
		}
	}
}

func TestAnObjectStoragePrefixLosesItsOuterSlashes(t *testing.T) {
	d := objectStorage()
	d.Config.ObjectStorage.Prefix = "/flagnetic/prod/"
	got, err := sinks.Check(d, environments, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Config.ObjectStorage.Prefix; p != "flagnetic/prod" {
		t.Errorf("prefix = %q, want flagnetic/prod", p)
	}
}

func TestAWebhookSinkRefusesWhatCannotWork(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*sinks.Draft)
		want map[string]string
	}{
		"nothing": {func(d *sinks.Draft) { d.Config.Webhook = &sinks.Webhook{} },
			map[string]string{"/webhook/url": "required"}},
		"a url with credentials": {func(d *sinks.Draft) { d.Config.Webhook.URL = "https://u:p@r.example" },
			map[string]string{"/webhook/url": "invalid_url"}},
		"a signing header of ours": {func(d *sinks.Draft) {
			d.Config.Webhook.Headers = map[string]string{"x-flagnetic-signature": "v1=00", "Authorization": "Bearer t"}
		}, map[string]string{"/webhook/headers/x-flagnetic-signature": "reserved"}},
		"a header name that is not a token": {func(d *sinks.Draft) {
			d.Config.Webhook.Headers = map[string]string{"Bad Name": "v"}
		}, map[string]string{"/webhook/headers/Bad Name": "invalid_header"}},
		"a header value with a line break": {func(d *sinks.Draft) {
			d.Config.Webhook.Headers = map[string]string{"X-Token": "a\r\nX-Evil: 1"}
		}, map[string]string{"/webhook/headers/X-Token": "invalid_header"}},
		"a secret of the caller's": {func(d *sinks.Draft) { d.Secret = "mine" },
			map[string]string{"/webhook/secret": "not_allowed"}},
	} {
		d := webhook()
		tc.edit(&d)
		_, err := sinks.Check(d, environments, governance.TargetPolicy{})
		if got := violations(t, err); !sameViolations(got, tc.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.want)
		}
	}
}

func TestAWebhookSinkNeedsHTTPSUnlessTheOperatorAllowsPlainHTTP(t *testing.T) {
	d := webhook()
	d.Config.Webhook.URL = "http://receiver.example/sink"
	_, err := sinks.Check(d, environments, governance.TargetPolicy{})
	var e *apperrors.Error
	if !errors.As(err, &e) || e.Code != "insecure_url" {
		t.Fatalf("plain http = %v, want insecure_url", err)
	}
	if _, err = sinks.Check(d, environments, governance.TargetPolicy{AllowHTTP: true}); err != nil {
		t.Errorf("plain http the operator allows = %v", err)
	}
	// The other kinds take http or https alike.
	if _, err = sinks.Check(clickHouse(), environments, governance.TargetPolicy{}); err != nil {
		t.Errorf("a ClickHouse sink over http = %v", err)
	}
}

func TestEveryKindRefusesWhatIsWrongWithTheCommonFields(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*sinks.Draft)
		want map[string]string
	}{
		"a blank name":          {func(d *sinks.Draft) { d.Name = "  " }, map[string]string{"/name": "invalid_name"}},
		"a long name":           {func(d *sinks.Draft) { d.Name = strings.Repeat("é", 201) }, map[string]string{"/name": "invalid_name"}},
		"an event kind":         {func(d *sinks.Draft) { d.EventKinds = []sinks.EventKind{"click"} }, map[string]string{"/event_kinds/0": "invalid_event_kind"}},
		"no configuration":      {func(d *sinks.Draft) { d.Config = sinks.Config{} }, map[string]string{"/webhook": "required"}},
		"a kind we do not have": {func(d *sinks.Draft) { d.Kind = "kafka" }, map[string]string{"/kind": "invalid_kind"}},
		"a batch of none":       {func(d *sinks.Draft) { d.BatchSize = -1 }, map[string]string{"/batch_size": "out_of_range"}},
		"a webhook batch over its bound": {func(d *sinks.Draft) { d.BatchSize = 5_001 },
			map[string]string{"/batch_size": "out_of_range"}},
		"a flush of none": {func(d *sinks.Draft) { d.FlushInterval = -time.Second }, map[string]string{"/flush_interval": "out_of_range"}},
	} {
		d := webhook()
		tc.edit(&d)
		_, err := sinks.Check(d, environments, governance.TargetPolicy{})
		if got := violations(t, err); !sameViolations(got, tc.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, tc.want)
		}
	}
}

func TestObjectStorageBatchingIsBoundedToAMinuteAndAnHour(t *testing.T) {
	for flush, ok := range map[time.Duration]bool{
		30 * time.Second: false, time.Minute: true, time.Hour: true, time.Hour + time.Second: false,
	} {
		d := objectStorage()
		d.FlushInterval = flush
		_, err := sinks.Check(d, environments, governance.TargetPolicy{})
		if (err == nil) != ok {
			t.Errorf("flush interval %s: error = %v, accepted = %t, want %t", flush, err, err == nil, ok)
		}
	}
}

func TestEventKindsAndEnvironmentsLoseTheirRepeatsAndNoneMeansAll(t *testing.T) {
	d := webhook()
	d.EventKinds = []sinks.EventKind{sinks.EventAlias, sinks.EventAlias, sinks.EventExposure}
	d.Environments = []string{"staging", "staging"}
	got, err := sinks.Check(d, environments, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.EventKinds, []sinks.EventKind{sinks.EventAlias, sinks.EventExposure}) ||
		!slices.Equal(got.Environments, []string{"staging"}) {
		t.Errorf("event kinds %v, environments %v, want repeats dropped", got.EventKinds, got.Environments)
	}
	all, err := sinks.Check(webhook(), environments, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []sinks.EventKind{sinks.EventExposure, sinks.EventAlias, sinks.EventEvaluationSummary}; !slices.Equal(all.StreamKinds(), want) {
		t.Errorf("a sink naming no event kind streams %v, want all of %v", all.StreamKinds(), want)
	}
}

func TestAnUnknownEnvironmentIsRefusedUnlessTheSinkAlreadyHeldItAndTheEditLeavesItAlone(t *testing.T) {
	d := webhook()
	d.Environments = []string{"production", "nowhere"}
	_, err := sinks.Check(d, environments, governance.TargetPolicy{})
	var e *apperrors.Error
	if !errors.As(err, &e) || e.Code != "unknown_environment" || len(e.Violations) != 1 || e.Violations[0].Pointer != "/environments/1" {
		t.Fatalf("an unknown environment = %v, want unknown_environment at /environments/1", err)
	}
	if _, err = sinks.Check(d, append(slices.Clone(environments), "nowhere"), governance.TargetPolicy{}); err != nil {
		t.Errorf("an environment kept = %v", err)
	}
}

func TestAMaskedOrAbsentSecretKeepsTheStoredOneAndAnythingElseReplacesIt(t *testing.T) {
	for secret, keeps := range map[string]bool{"": true, sinks.Masked: true, "new-password": false} {
		if got := sinks.KeepsSecret(secret); got != keeps {
			t.Errorf("secret %q kept = %t, want %t", secret, got, keeps)
		}
	}
	// A draft that keeps the stored secret needs none of its own.
	d := clickHouse()
	d.Secret, d.KeepsSecret = "", true
	if _, err := sinks.Check(d, environments, governance.TargetPolicy{}); err != nil {
		t.Errorf("an edit keeping the stored password = %v", err)
	}
}

func TestEditingASinkReplacesAConfigurationWholeAndCannotChangeItsKind(t *testing.T) {
	in := sinks.Input{Name: "Warehouse", Kind: sinks.KindClickHouse, Config: clickHouse().Config, EventKinds: []sinks.EventKind{sinks.EventAlias}}
	other := clickHouse().Config
	other.ClickHouse.Database = "other"
	renamed := "Renamed"

	got, err := in.With(sinks.Change{Name: &renamed, Config: &other})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != renamed || got.Config.ClickHouse.Database != "other" || !slices.Equal(got.EventKinds, in.EventKinds) {
		t.Errorf("edited = %+v", got)
	}
	if in.Config.ClickHouse.Database != "analytics" {
		t.Error("the edit changed the sink it was applied to")
	}

	_, err = in.With(sinks.Change{Config: &sinks.Config{Webhook: &sinks.Webhook{URL: "https://r.example"}}})
	var e *apperrors.Error
	if !errors.As(err, &e) || e.Code != "kind_immutable" {
		t.Errorf("another kind's configuration = %v, want kind_immutable", err)
	}
	other = sinks.Config{ObjectStorage: &sinks.ObjectStorage{Bucket: "b"}}
	kind := sinks.KindObjectStorage
	if _, err = in.With(sinks.Change{Kind: &kind}); !errors.As(err, &e) || e.Code != "kind_immutable" {
		t.Errorf("naming another kind = %v, want kind_immutable", err)
	}
	same := sinks.KindClickHouse
	if _, err = in.With(sinks.Change{Kind: &same}); err != nil {
		t.Errorf("naming the sink's own kind = %v", err)
	}
}

func TestOnlyDisablingIsAnEditAndEnablingWaitsForItsOwnOperation(t *testing.T) {
	in := sinks.Input{Name: "Warehouse", Kind: sinks.KindWebhook, Config: webhook().Config}
	yes, no := true, false
	_, err := in.With(sinks.Change{Enabled: &yes})
	if got := violations(t, err); !sameViolations(got, map[string]string{"/enabled": "use_enable"}) {
		t.Errorf("enabling by an edit: violations = %v, want use_enable", got)
	}
	in.Enabled = true
	got, err := in.With(sinks.Change{Enabled: &no})
	if err != nil || got.Enabled {
		t.Errorf("disabling by an edit = %+v, %v", got, err)
	}
}

func TestTheSameSinkIsTheSameInputAnyMapOrder(t *testing.T) {
	a := clickHouse().Input
	a.Config.ClickHouse.Settings = map[string]string{"max_threads": "2", "max_memory_usage": "1"}
	b := clickHouse().Input
	b.Config.ClickHouse.Settings = map[string]string{"max_memory_usage": "1", "max_threads": "2"}
	if !a.Same(b) {
		t.Error("inputs differing in map order are not the same")
	}
	b.Config.ClickHouse.Settings["max_threads"] = "3"
	if a.Same(b) {
		t.Error("inputs differing in a setting are the same")
	}
}

func TestOnlyLinkLocalAddressesAreRefusedToTheKindsThatMayReachPrivateOnes(t *testing.T) {
	for addr, refused := range map[string]bool{
		"169.254.169.254": true, "fe80::1": true, "::ffff:169.254.169.254": true, "ff02::1": true,
		"127.0.0.1": false, "10.0.0.5": false, "::1": false, "93.184.216.34": false,
	} {
		if got := sinks.LinkLocal(netip.MustParseAddr(addr)); got != refused {
			t.Errorf("LinkLocal(%s) = %t, want %t", addr, got, refused)
		}
	}
	if err := sinks.CheckNotLinkLocal([]netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("169.254.0.1")}, "/clickhouse/url"); err == nil {
		t.Error("a name resolving to a link-local address was accepted")
	}
	if err := sinks.CheckNotLinkLocal(nil, "/clickhouse/url"); err != nil {
		t.Errorf("a name that does not resolve = %v", err)
	}
}

// sameViolations reports whether a and b hold the same pairs.
func sameViolations(a, b map[string]string) bool {
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
