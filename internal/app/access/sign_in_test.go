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

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
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
	pool := testkit.Pool(t)
	c := clock.System{}
	return appaccess.NewSignIn(postgres.NewTransactor(pool), pgaccess.NewStore(pool), hasher, pggovernance.NewAuditLog(pool),
		c, id.NewSource(c), rand.Reader, bytes.Repeat([]byte{1}, 32))
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
	v := &countingVerifier{Hasher: password.NewBoundedHasher(password.DefaultParams, 4, password.SlotWait)}
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
	hasher := password.NewBoundedHasher(password.DefaultParams, 2, wait)
	h := newSignIn(t, hasher)
	m := testkit.NewMember(t, testkit.Pool(t))
	from := anAddress()
	attempt := func(email, pw string, ip netip.Addr) (string, time.Duration) {
		start := time.Now()
		_, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: email, Password: secret.New(pw), ClientIP: ip})
		return codeOf(err), time.Since(start)
	}

	var releases []func()
	for range 2 {
		release, err := hasher.Occupy(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
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

	for _, release := range releases {
		release()
	}
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
