package sinks_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

func TestAnAnswerIsClassifiedByItsStatusAndTheKindsOwnCode(t *testing.T) {
	type want struct {
		failed bool
		f      sinks.Failure
	}
	for _, tc := range []struct {
		name     string
		classify func() (sinks.Failure, bool)
		want     want
	}{
		{"webhook 204", func() (sinks.Failure, bool) { return sinks.WebhookFailure(204) }, want{}},
		{"webhook 301", func() (sinks.Failure, bool) { return sinks.WebhookFailure(301) }, want{true, sinks.Failure{Class: sinks.ClassRedirect, Status: 301}}},
		{"webhook 400", func() (sinks.Failure, bool) { return sinks.WebhookFailure(400) }, want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 400}}},
		{"webhook 404", func() (sinks.Failure, bool) { return sinks.WebhookFailure(404) }, want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 404}}},
		{"webhook 408", func() (sinks.Failure, bool) { return sinks.WebhookFailure(408) }, want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 408}}},
		{"webhook 429", func() (sinks.Failure, bool) { return sinks.WebhookFailure(429) }, want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 429}}},
		{"webhook 500", func() (sinks.Failure, bool) { return sinks.WebhookFailure(500) }, want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 500}}},
		{"clickhouse 200", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(200, "") }, want{}},
		{"clickhouse 500 with a schema exception", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(500, "16") },
			want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 500, Code: "16 NO_SUCH_COLUMN_IN_TABLE"}}},
		{"clickhouse 500 with a transient exception", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(500, "252") },
			want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 500, Code: "252 TOO_MANY_PARTS"}}},
		{"clickhouse 500 with an unknown exception", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(500, " 9999 ") },
			want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 500, Code: "9999"}}},
		{"clickhouse 500 with a header that is no number", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(500, "secret=hunter2") },
			want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 500}}},
		{"clickhouse 516 authentication", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(403, "516") },
			want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 403, Code: "516 AUTHENTICATION_FAILED"}}},
		{"clickhouse 4xx without a code", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(404, "") },
			want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 404}}},
		{"clickhouse 429", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(429, "") }, want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 429}}},
		{"clickhouse 503", func() (sinks.Failure, bool) { return sinks.ClickHouseFailure(503, "") }, want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 503}}},
		{"s3 200", func() (sinks.Failure, bool) { return sinks.S3Failure(200, "") }, want{}},
		{"s3 404 NoSuchBucket", func() (sinks.Failure, bool) { return sinks.S3Failure(404, "NoSuchBucket") },
			want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 404, Code: "NoSuchBucket"}}},
		{"s3 503 SlowDown", func() (sinks.Failure, bool) { return sinks.S3Failure(503, "SlowDown") },
			want{true, sinks.Failure{Class: sinks.ClassStatus, Status: 503, Code: "SlowDown"}}},
		{"s3 403 with prose for a code", func() (sinks.Failure, bool) { return sinks.S3Failure(403, "the admin password is hunter2") },
			want{true, sinks.Failure{Class: sinks.ClassRejected, Status: 403}}},
		{"s3 307", func() (sinks.Failure, bool) { return sinks.S3Failure(307, "TemporaryRedirect") },
			want{true, sinks.Failure{Class: sinks.ClassRedirect, Status: 307, Code: "TemporaryRedirect"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, failed := tc.classify()
			if failed != tc.want.failed || got != tc.want.f {
				t.Errorf("= %+v, %t; want %+v, %t", got, failed, tc.want.f, tc.want.failed)
			}
		})
	}
}

func TestOnlyACamelCaseWordIsKeptAsAnS3Code(t *testing.T) {
	for in, want := range map[string]string{
		"NoSuchBucket": "NoSuchBucket", "  AccessDenied\n": "AccessDenied", "XAmzContentSHA256Mismatch": "XAmzContentSHA256Mismatch",
		"": "", "two words": "", "Bad<Code>": "", "9Lives": "", "snake_case": "",
		"AVeryLongWordThatNoServiceEverWroteAsACodeButATargetMayWriteToCarrySecrets": "",
	} {
		if got := sinks.SafeS3Code(in); got != want {
			t.Errorf("SafeS3Code(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAFailureIsWrittenForLogsAndTheAPI(t *testing.T) {
	f := sinks.Failure{Class: sinks.ClassRejected, Status: 500, Code: "16 NO_SUCH_COLUMN_IN_TABLE"}
	if got, want := f.Error(), "rejected 500 16 NO_SUCH_COLUMN_IN_TABLE"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	r := f.Report()
	if r.Class != sinks.ClassRejected || r.Status == nil || *r.Status != 500 || r.Code == nil || *r.Code != "16 NO_SUCH_COLUMN_IN_TABLE" {
		t.Errorf("Report() = %+v", r)
	}
	bare := sinks.Failure{Class: sinks.ClassDNS}
	if got := bare.Error(); got != "dns" {
		t.Errorf("Error() = %q, want dns", got)
	}
	if r := bare.Report(); r.Status != nil || r.Code != nil {
		t.Errorf("a failure with no answer reports status and code: %+v", r)
	}
}

func TestEveryFailureClassIsListedOnce(t *testing.T) {
	seen := map[sinks.FailureClass]bool{}
	for _, c := range sinks.FailureClasses {
		seen[c] = true
	}
	if len(seen) != 8 || len(sinks.FailureClasses) != 8 {
		t.Errorf("failure classes = %v", sinks.FailureClasses)
	}
}

func TestEachKindIsGivenItsOwnCallTimeout(t *testing.T) {
	for k, want := range map[sinks.Kind]time.Duration{
		sinks.KindWebhook: 10 * time.Second, sinks.KindClickHouse: time.Minute, sinks.KindObjectStorage: 2 * time.Minute,
	} {
		if got := k.CallTimeout(); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func TestTheAddressesACallMayNotBeMadeToDependOnTheKind(t *testing.T) {
	strict, lifted := governance.TargetPolicy{}, governance.TargetPolicy{AllowPrivateNetworks: true}
	for _, tc := range []struct {
		addr string
		// refused for a webhook sink under the strict policy; for a webhook
		// sink under the lifted one; for the two other kinds.
		webhook, webhookLifted, other bool
	}{
		{"93.184.216.34", false, false, false},
		{"2606:2800:220:1::1", false, false, false},
		{"127.0.0.1", true, false, false},
		{"::1", true, false, false},
		{"10.0.0.5", true, false, false},
		{"fd00::1", true, false, false},
		{"::ffff:192.168.0.1", true, false, false},
		{"169.254.169.254", true, false, true},
		{"::ffff:169.254.169.254", true, false, true},
		{"fe80::1", true, false, true},
		{"100.64.0.1", true, false, false},
		{"198.18.0.1", true, false, false},
		{"64:ff9b::a00:1", true, false, false},
		{"64:ff9b::a9fe:a9fe", true, false, true},
		{"2002:a9fe:a9fe::1", true, false, true},
		{"fd00:ec2::254", true, false, true},
		{"64:ff9b::5db8:d822", false, false, false},
		{"0.0.0.0", true, true, true},
		{"::", true, true, true},
		{"224.0.0.1", true, true, true},
		{"239.1.1.1", true, true, true},
		{"ff02::1", true, true, true},
	} {
		a := netip.MustParseAddr(tc.addr)
		if got := sinks.KindWebhook.AddressRefused(a, strict); got != tc.webhook {
			t.Errorf("webhook %s: refused = %t, want %t", tc.addr, got, tc.webhook)
		}
		if got := sinks.KindWebhook.AddressRefused(a, lifted); got != tc.webhookLifted {
			t.Errorf("webhook %s, private networks allowed: refused = %t, want %t", tc.addr, got, tc.webhookLifted)
		}
		for _, k := range []sinks.Kind{sinks.KindClickHouse, sinks.KindObjectStorage} {
			for _, p := range []governance.TargetPolicy{strict, lifted} {
				if got := k.AddressRefused(a, p); got != tc.other {
					t.Errorf("%s %s with %+v: refused = %t, want %t", k, tc.addr, p, got, tc.other)
				}
			}
		}
	}
	for _, k := range sinks.Kinds {
		if !k.AddressRefused(netip.Addr{}, lifted) {
			t.Errorf("%s: the zero address is not refused", k)
		}
	}
}
