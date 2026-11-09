package access_test

import (
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestTheAuthSettingsCacheTrustsItsCopyOnlyWhileListening(t *testing.T) {
	pool := testkit.FreshPool(t)
	store, cache := pgaccess.NewStore(pool), pgaccess.NewAuthSettingsCache(pool)
	save := func(minLength int) {
		t.Helper()
		s := access.DefaultAuthSettings
		s.PasswordMinLength = minLength
		if err := store.SaveAuthSettings(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	read := func(step string, want int) {
		t.Helper()
		got, err := cache.AuthSettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got.PasswordMinLength != want {
			t.Errorf("%s: minimum length = %d, want %d", step, got.PasswordMinLength, want)
		}
	}

	read("fresh", 12)
	save(16)
	read("before listening, a change unannounced", 16)

	cache.Listening(true)
	read("listening", 16)
	save(18)
	read("listening, before the notification arrives", 16)
	cache.Received("")
	read("after the notification", 18)

	save(20)
	cache.Listening(false)
	read("once the listener is down", 20)
	save(21)
	read("while the listener is down, a change unannounced", 21)

	cache.Listening(true)
	read("listening again", 21)
	save(22)
	cache.Forget()
	read("after this replica's own change", 22)
}

func TestClearingEverySignInFailureLiftsEachLockoutAndThrottledAddress(t *testing.T) {
	pool := testkit.FreshPool(t)
	store := pgaccess.NewStore(pool)
	at := testkit.Start
	reserve := func(email, ip string, when time.Time) access.Reservation {
		t.Helper()
		r, err := store.ReserveAttempt(t.Context(), access.NewAttempt(email, netip.MustParseAddr(ip), when))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	member := testkit.NewMember(t, pool)
	home, _ := access.KnownSourceOf(netip.MustParseAddr("198.51.100.7"))
	if err := store.RecordKnownSource(t.Context(), member.ID, home, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The member's email is locked twice, from anywhere and from their known
	// source; another email's lockout ended a day ago; and one address made
	// more failures than its window allows.
	for range access.LockoutThreshold {
		reserve(member.Email, "192.0.2.1", at)
		reserve(member.Email, "198.51.100.7", at)
		reserve("ended@example.com", "192.0.2.2", at.Add(-24*time.Hour))
	}
	for i := range access.SourceFailureLimit {
		reserve(fmt.Sprintf("guess-%d@example.com", i), "203.0.113.9", at)
	}
	if err := reserve("one-more@example.com", "203.0.113.9", at).Refusal(); err == nil {
		t.Fatal("the address past its limit is not throttled")
	}

	cleared, err := store.ClearAllSignInFailures(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Errorf("lockouts cleared = %d, want 1: the member's email once, the lockout over not at all", cleared)
	}
	for what, r := range map[string]access.Reservation{
		"the member's email":                     reserve(member.Email, "192.0.2.1", at),
		"the member's email from a known source": reserve(member.Email, "198.51.100.7", at),
		"the throttled address":                  reserve("after@example.com", "203.0.113.9", at),
	} {
		if err := r.Refusal(); err != nil || r.Failures > 1 || r.SourceFailures != 1 {
			t.Errorf("%s after the clearing: refusal %v, failures %d, address failures %d; want a fresh count",
				what, err, r.Failures, r.SourceFailures)
		}
	}
}
