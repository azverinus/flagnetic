package management_test

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// wrongPassword is a password no member of these tests has.
const wrongPassword = "not-the-password-9"

// fromAddress returns a browser of srv whose requests a trusted proxy
// forwards for addr.
func fromAddress(t *testing.T, srv *testkit.Server, addr string) *testkit.Browser {
	t.Helper()
	b := testkit.NewBrowser(t, srv.URL)
	b.Header.Set("X-Forwarded-For", addr)
	return b
}

// someAddress returns an IPv4 address in the documentation ranges no other
// test of the run shares, so the failures counted against it are this
// test's alone.
func someAddress() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<16))
	return fmt.Sprintf("198.51.%d.%d", n.Int64()>>8, n.Int64()&0xff)
}

// signInAs attempts a sign-in and returns its status, its problem code and
// its retry_after.
func signInAs(t *testing.T, b *testkit.Browser, email, password string) (status int, code string, retryAfter int) {
	t.Helper()
	res, err := b.SignInWithResponse(t.Context(), management.SignInRequest{Email: email, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	var p *management.Problem
	switch {
	case res.ApplicationproblemJSON401 != nil:
		p = res.ApplicationproblemJSON401
	case res.ApplicationproblemJSON429 != nil:
		p = res.ApplicationproblemJSON429
		if p.RetryAfter != nil {
			retryAfter = *p.RetryAfter
		}
		if header := res.HTTPResponse.Header.Get("Retry-After"); header != fmt.Sprint(retryAfter) {
			t.Errorf("Retry-After = %q, want %d as in the body", header, retryAfter)
		}
	case res.StatusCode() != http.StatusOK:
		t.Fatalf("sign-in = %d %s, an answer the contract does not declare", res.StatusCode(), res.Body)
	}
	if p != nil {
		code = p.Code
	}
	return res.StatusCode(), code, retryAfter
}

func TestFiveWrongPasswordsLockTheEmailWithAGrowingWindow(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c), trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t))
	b := fromAddress(t, srv, someAddress())
	for i := range 5 {
		if status, code, _ := signInAs(t, b, m.Email, wrongPassword); status != 401 || code != "invalid_credentials" {
			t.Fatalf("failure %d: %d %s, want 401 invalid_credentials", i+1, status, code)
		}
	}
	if status, code, wait := signInAs(t, b, m.Email, wrongPassword); status != 429 || code != "locked" || wait != 60 {
		t.Fatalf("sixth attempt: %d %s retry after %d, want 429 locked for 60 seconds", status, code, wait)
	}
	if status, code, _ := signInAs(t, b, m.Email, m.Password); status != 429 || code != "locked" {
		t.Errorf("the right password while locked: %d %s, want locked", status, code)
	}
	if n := len(auditOf(t, srv, "auth.locked_out", m.ID.String())); n != 1 {
		t.Errorf("%d locked_out entries, want 1", n)
	}

	c.Advance(61 * time.Second)
	if status, _, _ := signInAs(t, b, m.Email, wrongPassword); status != 401 {
		t.Fatalf("the first failure after the lock: %d, want 401", status)
	}
	if status, code, wait := signInAs(t, b, m.Email, wrongPassword); status != 429 || code != "locked" || wait != 120 {
		t.Fatalf("after the sixth consecutive failure: %d %s retry after %d, want locked for 120 seconds", status, code, wait)
	}
	if n := len(auditOf(t, srv, "auth.locked_out", m.ID.String())); n != 2 {
		t.Errorf("%d locked_out entries, want 2", n)
	}

	c.Advance(121 * time.Second)
	b.SignIn(t, m.Email, m.Password)
	for range 2 {
		if status, _, _ := signInAs(t, b, m.Email, wrongPassword); status != 401 {
			t.Errorf("a failure after signing in: %d, want 401: success clears the count", status)
		}
	}
}

func TestAnUnknownEmailLocksIdenticallyToAKnownOne(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(testkit.Clock()), trustLoopback())
	b := fromAddress(t, srv, someAddress())
	email := "nobody-" + strings.ToLower(rand.Text()) + "@example.com"
	for i := range 5 {
		if status, code, _ := signInAs(t, b, email, wrongPassword); status != 401 || code != "invalid_credentials" {
			t.Fatalf("failure %d: %d %s, want 401 invalid_credentials", i+1, status, code)
		}
	}
	if status, code, wait := signInAs(t, b, strings.ToUpper(email), wrongPassword); status != 429 || code != "locked" || wait != 60 {
		t.Errorf("sixth attempt: %d %s retry after %d, want 429 locked for 60 seconds", status, code, wait)
	}
	if n := len(auditOfQ(t, srv, email)); n != 0 {
		t.Errorf("%d audit entries name the unknown email, want none", n)
	}
}

func TestUnknownEmailAndWrongPasswordTakeComparableTimeUnderLockout(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	b := fromAddress(t, srv, someAddress())
	const samples = 12
	var known, unknown []time.Duration
	for range samples {
		// A fresh email each time, so neither lockout nor throttle answers
		// first.
		m := testkit.NewMember(t, testkit.Pool(t))
		for _, email := range []string{m.Email, "nobody-" + m.Email} {
			start := time.Now()
			if status, _, _ := signInAs(t, b, email, wrongPassword); status != 401 {
				t.Fatalf("status = %d, want 401", status)
			}
			if email == m.Email {
				known = append(known, time.Since(start))
			} else {
				unknown = append(unknown, time.Since(start))
			}
		}
	}
	a, u := median(known), median(unknown)
	if ratio := float64(max(a, u)) / float64(min(a, u)); ratio > 3 {
		t.Errorf("median wrong password %v, unknown email %v: ratio %.1f, want at most 3", a, u, ratio)
	}
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}

func TestAHundredFailuresFromOneAddressAreRateLimitedWhileAnotherPasses(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(testkit.Clock()), trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t))
	// An IPv6 subscriber holds a whole /64: failures from any of its
	// addresses count together.
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<16))
	prefix := fmt.Sprintf("2001:db8:%x:1::", n.Int64())
	var wg sync.WaitGroup
	attempts := make(chan int)
	for range 8 {
		wg.Go(func() {
			for i := range attempts {
				b := fromAddress(t, srv, fmt.Sprintf("%s%x", prefix, i+1))
				if status, code, _ := signInAs(t, b, fmt.Sprintf("nobody-%d-%s", i, m.Email), wrongPassword); status != 401 {
					t.Errorf("failure %d: %d %s, want 401", i+1, status, code)
				}
			}
		})
	}
	for i := range 100 {
		attempts <- i
	}
	close(attempts)
	wg.Wait()

	status, code, wait := signInAs(t, fromAddress(t, srv, prefix+"ffff"), m.Email, m.Password)
	if status != 429 || code != "rate_limited" || wait != 900 {
		t.Errorf("the 101st attempt from the /64: %d %s retry after %d, want 429 rate_limited for 900 seconds", status, code, wait)
	}
	if status, _, _ := signInAs(t, fromAddress(t, srv, someAddress()), m.Email, m.Password); status != 200 {
		t.Errorf("the right password from another address: %d, want 200", status)
	}
}

func TestALockoutFromOneReplicaIsHonouredByAnother(t *testing.T) {
	a := testkit.StartServer(t, trustLoopback())
	b := testkit.StartServer(t, trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t))
	home := someAddress()
	fromAddress(t, a, home).SignIn(t, m.Email, m.Password)
	addr := someAddress()
	for range 6 {
		signInAs(t, fromAddress(t, a, addr), m.Email, wrongPassword)
	}
	if status, code, _ := signInAs(t, fromAddress(t, b, someAddress()), m.Email, m.Password); status != 429 || code != "locked" {
		t.Errorf("the right password on the other replica: %d %s, want 429 locked", status, code)
	}
	if status, code, _ := signInAs(t, fromAddress(t, b, home), m.Email, m.Password); status != 200 {
		t.Errorf("the right password on the other replica from where the member signed in on the first: %d %s, want 200", status, code)
	}
}

// someSubscriber returns an IPv6 /64 no other test of the run shares, to
// write its addresses after.
func someSubscriber() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<32))
	return fmt.Sprintf("2001:db8:%x:%x::", n.Int64()>>16, n.Int64()&0xffff)
}

// lockEmail locks email with wrong passwords from an address of its own.
func lockEmail(t *testing.T, srv *testkit.Server, email string) {
	t.Helper()
	b := fromAddress(t, srv, someAddress())
	for range 5 {
		signInAs(t, b, email, wrongPassword)
	}
	if status, code, _ := signInAs(t, b, email, wrongPassword); status != 429 || code != "locked" {
		t.Fatalf("after five wrong passwords: %d %s, want 429 locked", status, code)
	}
}

func TestALockedEmailLetsItsMemberInFromASourceTheySignedInFrom(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c), trustLoopback())
	m, other := testkit.NewMember(t, testkit.Pool(t)), testkit.NewMember(t, testkit.Pool(t))
	home := someSubscriber()
	fromAddress(t, srv, home+"1").SignIn(t, m.Email, m.Password)
	c.Advance(29 * 24 * time.Hour)
	lockEmail(t, srv, m.Email)
	lockEmail(t, srv, other.Email)

	// Any address of the /64 the member signed in from is that source.
	if status, code, _ := signInAs(t, fromAddress(t, srv, home+"2"), m.Email, m.Password); status != 200 {
		t.Errorf("the right password from a source the member signed in from: %d %s, want 200 despite the lock", status, code)
	}
	if status, code, _ := signInAs(t, fromAddress(t, srv, someAddress()), m.Email, m.Password); status != 429 || code != "locked" {
		t.Errorf("the right password from a source the member never signed in from: %d %s, want 429 locked", status, code)
	}
	// The source is known to that member alone.
	if status, code, _ := signInAs(t, fromAddress(t, srv, home+"3"), other.Email, other.Password); status != 429 || code != "locked" {
		t.Errorf("another member's email from the source: %d %s, want 429 locked", status, code)
	}
}

func TestAKnownSourceIsLockedByItsOwnFailuresAndLeavesTheEmailsAlone(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c), trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t))
	home := fromAddress(t, srv, someAddress())
	home.SignIn(t, m.Email, m.Password)
	for i := range 5 {
		if status, code, _ := signInAs(t, home, m.Email, wrongPassword); status != 401 || code != "invalid_credentials" {
			t.Fatalf("failure %d from the known source: %d %s, want 401 invalid_credentials", i+1, status, code)
		}
	}
	if status, code, wait := signInAs(t, home, m.Email, m.Password); status != 429 || code != "locked" || wait != 60 {
		t.Fatalf("the right password after five failures from the known source: %d %s retry after %d, want 429 locked for 60 seconds",
			status, code, wait)
	}
	if n := len(auditOf(t, srv, "auth.locked_out", m.ID.String())); n != 1 {
		t.Errorf("%d locked_out entries, want 1", n)
	}

	// The email counted none of them: elsewhere, it locks after five
	// failures of its own.
	away := fromAddress(t, srv, someAddress())
	for i := range 5 {
		if status, code, _ := signInAs(t, away, m.Email, wrongPassword); status != 401 {
			t.Fatalf("failure %d elsewhere: %d %s, want 401: the known source's failures are its own", i+1, status, code)
		}
	}
	if status, code, wait := signInAs(t, away, m.Email, m.Password); status != 429 || code != "locked" || wait != 60 {
		t.Errorf("elsewhere after five failures: %d %s retry after %d, want 429 locked for 60 seconds", status, code, wait)
	}

	c.Advance(61 * time.Second)
	if status, _, _ := signInAs(t, home, m.Email, wrongPassword); status != 401 {
		t.Fatalf("the known source's first failure after its lock: %d, want 401", status)
	}
	if status, code, wait := signInAs(t, home, m.Email, wrongPassword); status != 429 || code != "locked" || wait != 120 {
		t.Errorf("the known source after its sixth consecutive failure: %d %s retry after %d, want locked for 120 seconds", status, code, wait)
	}
}

func TestASourceIsKnownForThirtyDaysAfterTheLastSignInFromIt(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c), trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t))
	home := someAddress()
	fromAddress(t, srv, home).SignIn(t, m.Email, m.Password)
	c.Advance(30 * 24 * time.Hour)
	lockEmail(t, srv, m.Email)
	if status, code, _ := signInAs(t, fromAddress(t, srv, home), m.Email, m.Password); status != 429 || code != "locked" {
		t.Errorf("the right password thirty days after the last sign-in from the source: %d %s, want 429 locked", status, code)
	}
}

func TestAcceptingAnInvitationMakesItsSourceKnown(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(testkit.Clock()), trustLoopback())
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Ada Lovelace")
	home := someAddress()
	if res := accept(t, fromAddress(t, srv, home), mi.Invitation.Url, newPassword); res.JSON200 == nil {
		t.Fatalf("accept = %d: %s", res.StatusCode(), res.Body)
	}
	lockEmail(t, srv, mi.Member.Email)
	if status, code, _ := signInAs(t, fromAddress(t, srv, home), mi.Member.Email, newPassword); status != 200 {
		t.Errorf("the right password from where the invitation was accepted: %d %s, want 200 despite the lock", status, code)
	}
}

func TestADeactivatedMemberCannotSignIn(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(testkit.Clock()), trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t), testkit.WithStatus(access.MemberDeactivated))
	b := fromAddress(t, srv, someAddress())
	res, err := b.SignInWithResponse(t.Context(), management.SignInRequest{Email: m.Email, Password: m.Password})
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON401; p == nil || p.Code != "invalid_credentials" || len(res.HTTPResponse.Cookies()) != 0 {
		t.Fatalf("the right password of a deactivated member: %d %s, want 401 invalid_credentials and no cookie", res.StatusCode(), res.Body)
	}
	failed := auditOf(t, srv, "auth.sign_in_failed", m.ID.String())
	if len(failed) != 1 || failed[0].After.(map[string]any)["reason"] != "deactivated" {
		t.Errorf("sign_in_failed entries = %+v, want one with the reason deactivated", failed)
	}
	if n := len(auditOf(t, srv, "auth.signed_in", m.ID.String())); n != 0 {
		t.Errorf("%d signed_in entries, want none", n)
	}
	// It counts as a failure, so the right password never unlocks more
	// guesses than a wrong one.
	for range 4 {
		signInAs(t, b, m.Email, m.Password)
	}
	if status, code, _ := signInAs(t, b, m.Email, m.Password); status != 429 || code != "locked" {
		t.Errorf("after five right passwords of a deactivated member: %d %s, want locked", status, code)
	}
}

func TestAnInvitedMemberCannotSignInAndIsAuditedAsInvited(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithClock(testkit.Clock()), trustLoopback())
	m := testkit.NewMember(t, testkit.Pool(t), testkit.WithStatus(access.MemberInvited))
	if status, code, _ := signInAs(t, fromAddress(t, srv, someAddress()), m.Email, m.Password); status != 401 || code != "invalid_credentials" {
		t.Fatalf("an invited member signing in: %d %s, want 401 invalid_credentials", status, code)
	}
	failed := auditOf(t, srv, "auth.sign_in_failed", m.ID.String())
	if len(failed) != 1 || failed[0].After.(map[string]any)["reason"] != "invited" {
		t.Errorf("sign_in_failed entries = %+v, want one with the reason invited: they hold no password yet", failed)
	}
}

func TestAuthBodiesOver8KiBAreRefusedBeforeAnything(t *testing.T) {
	srv := testkit.StartServer(t)
	body := `{"email":"a@example.com","password":"` + strings.Repeat("x", 8<<10) + `"}`
	res, err := testkit.NewBrowser(t, srv.URL).SignInWithBodyWithResponse(t.Context(), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON413; p == nil || p.Code != "request_too_large" {
		t.Errorf("a 8 KiB password: %d %s, want 413 request_too_large", res.StatusCode(), res.Body)
	}
}
