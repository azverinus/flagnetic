package management_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// retentionServer starts a server on an instance of its own, so the purge it
// is ticked to run meets no other test's rows, timed by c, behind a proxy
// on the loopback that names each request's address.
func retentionServer(t *testing.T, c clock.Clock) (*testkit.Server, *pgxpool.Pool) {
	t.Helper()
	pool := testkit.FreshPool(t)
	return testkit.StartServer(t, testkit.WithPool(pool), testkit.WithClock(c), trustLoopback()), pool
}

// purge runs one round of the auth retention worker of srv.
func purge(t *testing.T, srv *testkit.Server) {
	t.Helper()
	if err := srv.Tick(t, worker.AuthRetentionName); err != nil {
		t.Fatal(err)
	}
}

// purged returns how many rows of kind the auth retention worker of srv has
// deleted.
func purged(t *testing.T, srv *testkit.Server, kind string) int {
	t.Helper()
	return int(testkit.Metric(t, srv.Scrape(t), `flagnetic_auth_purged_rows_total{kind="`+kind+`"}`))
}

// stored reports whether the session b's cookie refers to is still stored,
// ended or not, as the session store finds it.
func stored(t *testing.T, pool *pgxpool.Pool, b *testkit.Browser) bool {
	t.Helper()
	key := secret.NewKeyring(secret.New(testkit.SecretKey)).Key(secret.UseSessionCookie)
	hash, ok := access.ParseSessionCookie(b.Cookie(management.SessionCookie), key)
	if !ok {
		t.Fatal("the browser holds no session cookie the server issued")
	}
	_, _, err := pgaccess.NewStore(pool).SessionByHash(t.Context(), hash)
	if errors.Is(err, appaccess.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

// holdRetentionLock holds the purge lock in a transaction of its own, as a
// replica purging does, until the returned function is called.
func holdRetentionLock(t *testing.T, pool *pgxpool.Pool) (release func()) {
	t.Helper()
	held, done := make(chan error), make(chan struct{})
	go func() {
		err := postgres.NewTransactor(pool).Within(context.Background(), func(ctx context.Context) error {
			ok, err := pgaccess.NewStore(pool).TryLockAuthRetention(ctx)
			if err == nil && !ok {
				err = errors.New("the purge lock is held already")
			}
			held <- err
			<-done
			return nil
		})
		if err != nil {
			t.Error(err)
		}
		close(held)
	}()
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	return func() {
		close(done)
		<-held
	}
}

func TestTheRetentionWorkerPurgesEndedSessionsAndLeavesLiveOnesAlone(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	m := testkit.NewMember(t, pool)
	live, idle, revoked := signedIn(t, srv, m), signedIn(t, srv, m), signedIn(t, srv, m)
	// The browser drops the cookie sign-out clears; the test keeps it to look
	// the session up.
	revokedCookie := testkit.NewBrowser(t, srv.URL)
	revokedCookie.SetCookie(management.SessionCookie, revoked.Cookie(management.SessionCookie))
	if res, err := revoked.SignOutWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("sign-out = %v, %v", res.StatusCode(), err)
	}

	purge(t, srv)
	if stored(t, pool, revokedCookie) || !stored(t, pool, idle) || !stored(t, pool, live) {
		t.Error("first purge: want the revoked session gone at once and the others kept")
	}
	for range 25 {
		c.Advance(time.Hour)
		wantSignedIn(t, live, "the session in use")
	}
	purge(t, srv)
	if stored(t, pool, idle) || !stored(t, pool, live) {
		t.Error("purge a day later: want the idle session gone and the one in use kept")
	}
	if sessions := mySessions(t, live); len(sessions) != 1 || !sessions[0].Current {
		t.Errorf("sessions = %+v, want the one in use", sessions)
	}
	if got := purged(t, srv, "sessions"); got != 2 {
		t.Errorf("purged sessions = %d, want 2", got)
	}

	other := testkit.StartServer(t, testkit.WithPool(pool), testkit.WithClock(c))
	purge(t, other)
	purge(t, srv)
	if !stored(t, pool, live) || purged(t, other, "sessions") != 0 {
		t.Error("purges with nothing ended removed something")
	}
}

func TestARetentionRunLeavesTheWorkToAReplicaAlreadyPurging(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	m := testkit.NewMember(t, pool)
	idle := signedIn(t, srv, m)
	if status, _, _ := signInAs(t, fromAddress(t, srv, someAddress()), m.Email, wrongPassword); status != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", status)
	}
	c.Advance(25 * time.Hour)

	release := holdRetentionLock(t, pool)
	purge(t, srv)
	if !stored(t, pool, idle) {
		t.Error("the ended session was purged while another replica held the lock")
	}
	for _, kind := range []string{"sessions", "email_failures", "source_failures"} {
		if got := purged(t, srv, kind); got != 0 {
			t.Errorf("purged %s = %d while another replica held the lock, want 0", kind, got)
		}
	}

	release()
	purge(t, srv)
	if stored(t, pool, idle) {
		t.Error("the ended session survived the purge after the lock was released")
	}
	// The sign-in's source keeps its row too, its failure taken back, until
	// its window is over.
	for kind, want := range map[string]int{"sessions": 1, "email_failures": 1, "source_failures": 2} {
		if got := purged(t, srv, kind); got != want {
			t.Errorf("purged %s = %d after the lock was released, want %d", kind, got, want)
		}
	}
}

func TestTheRetentionWorkerPurgesOnlyLockoutsAndSourceFailuresPastTheirWindow(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	const (
		staleFrom  = "203.0.113.1"
		recentFrom = "203.0.113.2"
		lockedFrom = "203.0.113.3"
	)
	fail := func(email, from string, times int) {
		t.Helper()
		b := fromAddress(t, srv, from)
		for range times {
			if status, _, _ := signInAs(t, b, email, wrongPassword); status != http.StatusUnauthorized {
				t.Fatalf("failure of %s = %d, want 401", email, status)
			}
		}
	}
	stale, recent := testkit.NewMember(t, pool), testkit.NewMember(t, pool)
	ranOut, locked := testkit.NewMember(t, pool), testkit.NewMember(t, pool)
	fail(stale.Email, staleFrom, 3)
	c.Advance(23 * time.Hour)
	fail(recent.Email, recentFrom, 2)
	fail(ranOut.Email, recentFrom, 5) // locked for a minute, a lock long lifted when the purge runs
	c.Set(testkit.Start.Add(24*time.Hour + time.Minute))
	fail(locked.Email, lockedFrom, 5)

	purge(t, srv)
	if got := purged(t, srv, "email_failures"); got != 1 {
		t.Errorf("purged email failures = %d, want 1: only the email whose last failure is a day old", got)
	}
	if got := purged(t, srv, "source_failures"); got != 2 {
		t.Errorf("purged source failures = %d, want 2: the sources whose windows are over", got)
	}

	b := fromAddress(t, srv, "203.0.113.9")
	if status, code, _ := signInAs(t, b, locked.Email, locked.Password); status != http.StatusTooManyRequests || code != "locked" {
		t.Errorf("the locked email with its password = %d %s, want 429 locked: a lock in force is kept", status, code)
	}
	fail(recent.Email, "203.0.113.9", 3)
	if status, code, _ := signInAs(t, b, recent.Email, wrongPassword); status != http.StatusTooManyRequests || code != "locked" {
		t.Errorf("the recent email after three more failures = %d %s, want 429 locked: its earlier failures still count", status, code)
	}
	signInAs(t, b, ranOut.Email, wrongPassword)
	if status, code, retry := signInAs(t, b, ranOut.Email, wrongPassword); status != http.StatusTooManyRequests || code != "locked" || retry != 120 {
		t.Errorf("the email whose lock ran out = %d %s retry %d, want 429 locked for twice its window", status, code, retry)
	}
	fail(stale.Email, "203.0.113.9", 4)
	if status, code, _ := signInAs(t, b, stale.Email, wrongPassword); status != http.StatusUnauthorized {
		t.Errorf("the stale email after four more failures = %d %s, want 401: its count started again", status, code)
	}
}

func TestTheRetentionWorkerForgetsAKnownSourceThirtyDaysAfterTheLastSignInFromIt(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	m := testkit.NewMember(t, pool)
	home := fromAddress(t, srv, "203.0.113.20")
	home.SignIn(t, m.Email, m.Password)
	if status, _, _ := signInAs(t, home, m.Email, wrongPassword); status != http.StatusUnauthorized {
		t.Fatalf("a wrong password from the known source = %d, want 401", status)
	}

	c.Advance(30*24*time.Hour - time.Second)
	purge(t, srv)
	if got := purged(t, srv, "known_source_failures"); got != 1 {
		t.Errorf("purged known source failures = %d, want 1: its last failure is long past a day old", got)
	}
	if got := purged(t, srv, "known_sources"); got != 0 {
		t.Errorf("purged known sources = %d a second before thirty days, want 0", got)
	}
	c.Advance(time.Second)
	purge(t, srv)
	if got := purged(t, srv, "known_sources"); got != 1 {
		t.Errorf("purged known sources = %d thirty days after the sign-in, want 1", got)
	}
}

func TestTheAuthPurgeRunsInBatchesUntilNothingIsLeft(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	store := pgaccess.NewStore(pool)
	const sources = 2500
	for i := range sources {
		addr := netip.MustParseAddr(fmt.Sprintf("10.%d.%d.1", i/250, i%250))
		if _, err := store.ReserveAttempt(t.Context(), access.NewAttempt(fmt.Sprintf("batch-%d@example.com", i), addr, c.Now())); err != nil {
			t.Fatal(err)
		}
	}
	c.Advance(access.SourceWindow)

	purge(t, srv)
	if got := purged(t, srv, "source_failures"); got != sources {
		t.Errorf("purged source failures = %d, want all %d in one run", got, sources)
	}
	purge(t, srv)
	if got := purged(t, srv, "source_failures"); got != sources {
		t.Errorf("purged source failures after a second run = %d, want nothing more", got)
	}
}
