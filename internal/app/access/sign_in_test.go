package access_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// countingVerifier counts the passwords it verifies.
type countingVerifier struct {
	*password.Hasher
	verified atomic.Int32
}

func (v *countingVerifier) Verify(ctx context.Context, p secret.String, encoded string) (bool, bool, error) {
	v.verified.Add(1)
	return v.Hasher.Verify(ctx, p, encoded)
}

func newSignIn(t *testing.T, hasher appaccess.PasswordVerifier) *appaccess.SignIn {
	t.Helper()
	return newSignInAt(t, clock.System{}, hasher)
}

// newSignInAt returns the handler reading the time from c.
func newSignInAt(t *testing.T, c clock.Clock, hasher appaccess.PasswordVerifier) *appaccess.SignIn {
	t.Helper()
	pool := testkit.Pool(t)
	return appaccess.NewSignIn(appaccess.SignInDeps{
		Tx: postgres.NewTransactor(pool), Store: pgaccess.NewStore(pool), Settings: pgaccess.NewStore(pool), Hasher: hasher, Audit: testkit.Record(pool),
		Clock: c, IDs: id.NewSource(c), Random: rand.Reader, Key: bytes.Repeat([]byte{1}, 32),
	})
}

// anAddress returns an address no other test of the run signs in from.
func anAddress() netip.Addr {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[0], b[1] = 0x20, 0x01
	return netip.AddrFrom16(b)
}

func codeOf(err error) string {
	if e, ok := apperrors.From(err); ok {
		return e.Code
	}
	return ""
}

func TestParallelWrongSignInsLockAfterAboutOneVerificationPastTheThreshold(t *testing.T) {
	v := &countingVerifier{Hasher: password.NewBoundedHasher(password.DefaultParams, semaphore.NewWeighted(4), password.SlotWait)}
	h := newSignIn(t, v)
	m := testkit.NewMember(t, testkit.Pool(t))
	const attempts = 24
	codes := make([]string, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			_, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New("not-the-password-9"), ClientIP: anAddress()})
			codes[i] = codeOf(err)
		})
	}
	wg.Wait()
	locked := 0
	for _, c := range codes {
		if c == "locked" {
			locked++
		}
	}
	if n := v.verified.Load(); n > access.LockoutThreshold+1 || locked < attempts-access.LockoutThreshold-1 {
		t.Errorf("%d verifications and %d attempts locked of %d, want at most %d verified: each attempt reserves its failure first",
			n, locked, attempts, access.LockoutThreshold+1)
	}
}

func TestEverySlotHeldAnswersBusyAndRefusedAttemptsTakeNoSlot(t *testing.T) {
	const wait = 200 * time.Millisecond
	slots := semaphore.NewWeighted(2)
	hasher := password.NewBoundedHasher(password.DefaultParams, slots, wait)
	h := newSignIn(t, hasher)
	m := testkit.NewMember(t, testkit.Pool(t))
	from := anAddress()
	attempt := func(email, pw string, ip netip.Addr) (string, time.Duration) {
		start := time.Now()
		_, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: email, Password: secret.New(pw), ClientIP: ip})
		return codeOf(err), time.Since(start)
	}

	// The test holds every slot, as hashes running for other sign-ins would.
	if err := slots.Acquire(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if code, took := attempt(m.Email, "not-the-password-9", from); code != "busy" || took < wait {
			t.Fatalf("with every slot held: %s after %v, want busy after the wait", code, took)
		}
	}
	if code, took := attempt(m.Email, strings.Repeat("x", 129), from); code != "invalid_credentials" || took >= wait {
		t.Errorf("an oversized password with every slot held: %s after %v, want invalid_credentials at once", code, took)
	}
	locked := "nobody-" + m.Email
	for range 5 {
		attempt(locked, strings.Repeat("x", 129), from)
	}
	if code, took := attempt(locked, "not-the-password-9", from); code != "locked" || took >= wait {
		t.Errorf("a locked email with every slot held: %s after %v, want locked at once", code, took)
	}
	throttled := anAddress()
	for i := range access.SourceFailureLimit {
		attempt("spray-"+string(rune('a'+i%26))+m.Email, strings.Repeat("x", 129), throttled)
	}
	if code, took := attempt(m.Email, m.Password, throttled); code != "rate_limited" || took >= wait {
		t.Errorf("a throttled source with every slot held: %s after %v, want rate_limited at once", code, took)
	}

	slots.Release(2)
	// The busy attempts were no guesses: they counted nothing.
	if code, _ := attempt(m.Email, "not-the-password-9", from); code != "invalid_credentials" {
		t.Errorf("a wrong password once slots are free: %s, want invalid_credentials, not locked by the busy attempts", code)
	}
	if code, _ := attempt(m.Email, m.Password, from); code != "" {
		t.Errorf("the right password once slots are free: %s, want signed in", code)
	}
}

// busyRehash verifies passwords as its hasher does but finds every stored
// hash outdated, and finds no slot to rehash one.
type busyRehash struct{ *password.Hasher }

func (b busyRehash) Verify(ctx context.Context, p secret.String, encoded string) (bool, bool, error) {
	ok, _, err := b.Hasher.Verify(ctx, p, encoded)
	return ok, true, err
}

func (busyRehash) Hash(context.Context, secret.String) (string, error) { return "", password.ErrBusy }

func TestARightPasswordNeverCountsAsAFailure(t *testing.T) {
	m := testkit.NewMember(t, testkit.Pool(t))
	from := anAddress()
	busy := newSignIn(t, busyRehash{password.NewHasher(password.DefaultParams)})
	for i := range access.LockoutThreshold + 1 {
		_, err := busy.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New(m.Password), ClientIP: from})
		if codeOf(err) != "busy" {
			t.Fatalf("attempt %d with the right password and no slot to rehash: %v, want busy", i+1, err)
		}
	}
	h := newSignIn(t, password.NewHasher(password.DefaultParams))
	if _, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New(m.Password), ClientIP: from}); err != nil {
		t.Errorf("the right password once a rehash can run: %v, want signed in: the right password counted no failure", err)
	}
}

// busyVerify finds no slot to verify any password.
type busyVerify struct{ *password.Hasher }

func (busyVerify) Verify(context.Context, secret.String, string) (bool, bool, error) {
	return false, false, password.ErrBusy
}

func TestABusyAttemptLiftsTheLockItStarted(t *testing.T) {
	c := testkit.Clock()
	m := testkit.NewMember(t, testkit.Pool(t))
	from := anAddress()
	attempt := func(h *appaccess.SignIn) string {
		_, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New("not-the-password-9"), ClientIP: from})
		return codeOf(err)
	}
	free, busy := newSignInAt(t, c, password.NewHasher(password.DefaultParams)), newSignInAt(t, c, busyVerify{})
	for range access.LockoutThreshold {
		attempt(free)
	}
	c.Advance(access.LockoutWindow(access.LockoutThreshold) + time.Second)
	if code := attempt(busy); code != "busy" {
		t.Fatalf("an attempt with no slot after the lock ended: %s, want busy", code)
	}
	if code := attempt(free); code != "invalid_credentials" {
		t.Errorf("a wrong password after the busy attempt: %s, want invalid_credentials: the busy attempt guessed nothing and locked nothing", code)
	}
}

func TestARefusedAttemptCostsItsSourceNothing(t *testing.T) {
	h := newSignIn(t, password.NewHasher(password.DefaultParams))
	m := testkit.NewMember(t, testkit.Pool(t))
	from := anAddress()
	attempt := func(email string) string {
		_, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: email, Password: secret.New("not-the-password-9"), ClientIP: from})
		return codeOf(err)
	}
	for range access.LockoutThreshold {
		attempt(m.Email)
	}
	for i := range access.SourceFailureLimit {
		if code := attempt(m.Email); code != "locked" {
			t.Fatalf("attempt %d on the locked email: %s, want locked", i+1, code)
		}
	}
	if code := attempt("nobody-" + m.Email); code != "invalid_credentials" {
		t.Errorf("a wrong password on another email after %d locked refusals: %s, want invalid_credentials: a refusal checked no password and is no failure of its source",
			access.SourceFailureLimit, code)
	}
}
