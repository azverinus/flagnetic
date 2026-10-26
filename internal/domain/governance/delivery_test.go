package governance_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	someDelivery = id.MustParse[governance.Delivery]("0190d2a0-0000-7000-8000-0000000000f1")
	someEvent    = id.MustParse[governance.Event]("0190d2a0-0000-7000-8000-0000000000f2")
)

// random returns a source whose next draw is u, a fraction in [0, 1).
func random(u float64) *bytes.Reader {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(u*(1<<53))<<11)
	return bytes.NewReader(b)
}

func code(c int) *int { return &c }

// The vectors are what a receiver written in any language computes: the
// lower-case hex of HMAC-SHA256 over "{timestamp}.{body}" keyed by the
// secret's own bytes, computed here with openssl.
func TestASignatureIsHMACSHA256OverTheTimestampAndTheBody(t *testing.T) {
	body := []byte(`{"id":"e1","type":"ping"}`)
	timestamp := governance.SignatureTimestamp(occurred)
	if timestamp != "1772445600" {
		t.Fatalf("timestamp %q, want Unix seconds", timestamp)
	}
	const current = "v1=edb66c1a890bec591f53133094ae1734a1763c9557a09b03f0fc127a91d7f904"
	const previous = "v1=e58165691859e124f02c18640e8734c67c4790d2def703a168dc928513994fdb"
	if got := governance.Sign(timestamp, body, "a-receivers-secret"); got != current {
		t.Errorf("Sign = %q, want %q", got, current)
	}
	// During a rotation's overlap both secrets sign, the current one first.
	if got := governance.Sign(timestamp, body, "a-receivers-secret", "the-previous-secret"); got != current+","+previous {
		t.Errorf("Sign under two secrets = %q", got)
	}
}

func TestEveryCallCarriesTheEventTypeDeliveryAttemptTimestampAndSignature(t *testing.T) {
	body := []byte(`{"id":"e1"}`)
	got := governance.DeliveryHeaders(governance.Delivery{ID: someDelivery, Event: someEvent, Type: "flag.kill_switch", Body: body},
		3, occurred, []string{"a-receivers-secret"})
	want := map[string]string{
		"Content-Type":           "application/json",
		"X-Flagnetic-Event":      someEvent.String(),
		"X-Flagnetic-Event-Type": "flag.kill_switch",
		"X-Flagnetic-Delivery":   someDelivery.String(),
		"X-Flagnetic-Attempt":    "3",
		"X-Flagnetic-Timestamp":  "1772445600",
		"X-Flagnetic-Signature":  governance.Sign("1772445600", body, "a-receivers-secret"),
	}
	if len(got) != len(want) {
		t.Errorf("headers %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestNoAnswerTimeoutsThrottlingAndServerErrorsAreRetried(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attempt   governance.DeliveryAttempt
		succeeded bool
		retryable bool
	}{
		{"no answer", governance.DeliveryAttempt{Error: new("connection refused")}, false, true},
		{"200", governance.DeliveryAttempt{StatusCode: code(200)}, true, false},
		{"204", governance.DeliveryAttempt{StatusCode: code(204)}, true, false},
		{"302, a redirect not followed", governance.DeliveryAttempt{StatusCode: code(302)}, false, false},
		{"400", governance.DeliveryAttempt{StatusCode: code(400)}, false, false},
		{"404", governance.DeliveryAttempt{StatusCode: code(404)}, false, false},
		{"408", governance.DeliveryAttempt{StatusCode: code(408)}, false, true},
		{"429", governance.DeliveryAttempt{StatusCode: code(429)}, false, true},
		{"500", governance.DeliveryAttempt{StatusCode: code(500)}, false, true},
		{"503", governance.DeliveryAttempt{StatusCode: code(503)}, false, true},
		{"599", governance.DeliveryAttempt{StatusCode: code(599)}, false, true},
	} {
		if got := tc.attempt.Succeeded(); got != tc.succeeded {
			t.Errorf("%s: succeeded %v", tc.name, got)
		}
		if got := tc.attempt.Retryable(); got != tc.retryable {
			t.Errorf("%s: retryable %v", tc.name, got)
		}
	}
}

func TestTheBackoffRisesFromTenSecondsToTwelveHoursWithinTwentyPercent(t *testing.T) {
	bases := []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour,
		6 * time.Hour, 12 * time.Hour}
	for i, base := range bases {
		n := i + 1
		for _, tc := range []struct {
			u    float64
			want time.Duration
		}{
			{0, base * 8 / 10},
			{0.5, base},
			{math.Nextafter(1, 0), base * 12 / 10},
		} {
			got, err := governance.RetryDelay(n, random(tc.u))
			if err != nil {
				t.Fatal(err)
			}
			if d := got - tc.want; d < -time.Millisecond || d > time.Millisecond {
				t.Errorf("after attempt %d with draw %v: %v, want %v", n, tc.u, got, tc.want)
			}
		}
	}
	if _, err := governance.RetryDelay(1, iotest.ErrReader(bytes.ErrTooLarge)); err == nil {
		t.Error("a random source that fails gave a delay")
	}
}

func TestAnAttemptSettlesTheDeliveryOrSchedulesTheNext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		attempt governance.DeliveryAttempt
		want    governance.DeliveryStatus
		next    time.Duration
	}{
		{"accepted", governance.DeliveryAttempt{N: 1, At: occurred, StatusCode: code(200)}, governance.DeliverySucceeded, 0},
		{"refused", governance.DeliveryAttempt{N: 1, At: occurred, StatusCode: code(410)}, governance.DeliveryFailed, 0},
		{"a first 500", governance.DeliveryAttempt{N: 1, At: occurred, StatusCode: code(500)}, governance.DeliveryPending,
			10 * time.Second},
		{"a seventh timeout", governance.DeliveryAttempt{N: 7, At: occurred, Error: new("timeout")},
			governance.DeliveryPending, 12 * time.Hour},
		{"the eighth 503", governance.DeliveryAttempt{N: 8, At: occurred, StatusCode: code(503)},
			governance.DeliveryExhausted, 0},
		{"a refusal on the last attempt", governance.DeliveryAttempt{N: 8, At: occurred, StatusCode: code(400)},
			governance.DeliveryFailed, 0},
	} {
		status, next, err := governance.SettleAttempt(tc.attempt, random(0.5))
		if err != nil {
			t.Fatal(err)
		}
		if status != tc.want {
			t.Errorf("%s: status %s, want %s", tc.name, status, tc.want)
		}
		switch {
		case tc.next == 0 && next != nil:
			t.Errorf("%s: a settled delivery is due again at %v", tc.name, next)
		case tc.next != 0 && (next == nil || !next.Equal(occurred.Add(tc.next))):
			t.Errorf("%s: next attempt %v, want %v", tc.name, next, occurred.Add(tc.next))
		}
	}
}

func TestAResponseSnippetIsTheFirstKibibyteAsValidText(t *testing.T) {
	if got := governance.ResponseSnippet(nil); got != nil {
		t.Errorf("an empty body is snippet %q, want none", *got)
	}
	if got := governance.ResponseSnippet([]byte(`{"ok":true}`)); got == nil || *got != `{"ok":true}` {
		t.Errorf("snippet %v", got)
	}
	long := governance.ResponseSnippet([]byte(strings.Repeat("é", 1000)))
	if long == nil || len(*long) > governance.ResponseSnippetLimit || !utf8.ValidString(*long) {
		t.Errorf("a long body is snippet of %d bytes", len(*long))
	}
	if got := governance.ResponseSnippet([]byte{0xff, 'o', 'k'}); got == nil || !utf8.ValidString(*got) {
		t.Errorf("a binary body is snippet %v", got)
	}
}

// A redelivery is the same event, byte for byte, under a new delivery with
// no attempts, so a receiver deduplicating on the event id recognises it.
func TestARedeliveryIsTheSameEventUnderANewDelivery(t *testing.T) {
	sent := governance.Delivery{
		ID: someDelivery, Webhook: id.MustParse[governance.Webhook]("0190d2a0-0000-7000-8000-0000000000f3"),
		Event: someEvent, Type: "flag.created", Body: []byte(`{"id":"e1"}`), Status: governance.DeliveryFailed,
		Attempts: []governance.DeliveryAttempt{{N: 1, At: occurred, StatusCode: code(400)}}, CreatedAt: occurred,
	}
	again := id.MustParse[governance.Delivery]("0190d2a0-0000-7000-8000-0000000000f4")
	at := occurred.Add(time.Hour)
	got := sent.Redeliver(again, at)
	if got.ID != again || got.Webhook != sent.Webhook || got.Event != sent.Event || got.Type != sent.Type ||
		!bytes.Equal(got.Body, sent.Body) {
		t.Errorf("redelivery %+v of %+v", got, sent)
	}
	if got.Status != governance.DeliveryPending || len(got.Attempts) != 0 || got.NextAttemptAt == nil ||
		!got.NextAttemptAt.Equal(at) || !got.CreatedAt.Equal(at) {
		t.Errorf("redelivery %+v, want pending, due now, with no attempts", got)
	}
}

func TestAPingNamesItsProjectAndCallerAndNothingElse(t *testing.T) {
	actor := governance.Actor{Kind: governance.ActorMember, ID: "0190d2a0-0000-7000-8000-0000000000d1", Name: "Ada"}
	e := governance.PingEvent(someEvent, someProject, "shop", actor, occurred)
	got := e.Envelope()
	if got["type"] != governance.EventPing || got["id"] != someEvent.String() {
		t.Errorf("envelope %v", got)
	}
	if p, _ := got["project"].(map[string]any); p["key"] != "shop" {
		t.Errorf("project %v", got["project"])
	}
	if a, _ := got["actor"].(map[string]any); a["name"] != "Ada" {
		t.Errorf("actor %v", got["actor"])
	}
	for _, k := range []string{"environment", "entity", "audit_entry_id"} {
		if v, present := got[k]; !present || v != nil {
			t.Errorf("%s = %v, want null", k, v)
		}
	}
	if d, ok := got["data"].(map[string]any); !ok || len(d) != 0 {
		t.Errorf("data %v, want an empty object", got["data"])
	}
}
