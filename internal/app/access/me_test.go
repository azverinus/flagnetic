package access_test

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func newChangeMyPassword(t *testing.T, hasher appaccess.PasswordVerifier) *appaccess.ChangeMyPassword {
	t.Helper()
	pool := testkit.Pool(t)
	c := clock.System{}
	return appaccess.NewChangeMyPassword(appaccess.ChangeMyPasswordDeps{
		Tx: postgres.NewTransactor(pool), Store: pgaccess.NewStore(pool), Settings: pgaccess.NewStore(pool), Hasher: hasher,
		Audit: testkit.Record(pool), Clock: c, IDs: id.NewSource(c),
	})
}

// holdAChangeOfPassword signs m in, starts a change of m's password whose
// current-password verification is held, runs interleave while it is held,
// and returns the change's outcome.
func holdAChangeOfPassword(t *testing.T, m testkit.Member, interleave func(session access.Session)) error {
	t.Helper()
	signedIn, err := newSignIn(t, password.NewHasher(password.DefaultParams)).Handle(t.Context(),
		appaccess.SignInCommand{Email: m.Email, Password: secret.New(m.Password), ClientIP: anAddress()})
	if err != nil {
		t.Fatal(err)
	}
	v := &heldVerify{Hasher: password.NewHasher(password.DefaultParams), verified: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- newChangeMyPassword(t, v).Handle(t.Context(), appaccess.ChangeMyPasswordCommand{
			Caller: signedIn.Caller, CurrentPassword: secret.New(m.Password), NewPassword: secret.New("attacker-chosen-pass-9"), ClientIP: anAddress(),
			Request: governance.Request{Actor: appaccess.ActorOf(signedIn.Caller)},
		})
	}()
	<-v.verified
	interleave(*signedIn.Caller.Session)
	close(v.release)
	return <-result
}

func TestAPasswordChangeHeldWhileAResetCompletesDoesNotOverwriteTheReset(t *testing.T) {
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	const reset = "harbor-maple-signal-4410"
	testkit.RecordSecret(reset)
	testkit.RecordSecret("attacker-chosen-pass-9")
	err := holdAChangeOfPassword(t, m, func(access.Session) { completeAPasswordReset(t, m, reset) })
	if codeOf(err) != "invalid_credentials" {
		t.Errorf("the change held while a reset completed: %v, want invalid_credentials", err)
	}
	h := newSignIn(t, password.NewHasher(password.DefaultParams))
	if _, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New(reset), ClientIP: anAddress()}); err != nil {
		t.Errorf("the password the reset set: %v, want signed in", err)
	}
}

// completeAPasswordReset issues a password reset of m, as an instance admin
// does, and completes it with newPassword.
func completeAPasswordReset(t *testing.T, m testkit.Member, newPassword string) {
	t.Helper()
	pool := testkit.Pool(t)
	c := clock.System{}
	tx, store, audit, ids := postgres.NewTransactor(pool), pgaccess.NewStore(pool), testkit.Record(pool), id.NewSource(c)
	admin := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
	caller := access.Caller{Method: access.MethodSession, Member: &access.Member{ID: admin.ID, Email: admin.Email, Status: access.MemberActive, InstanceAdmin: true}}
	issued, err := appaccess.NewIssuePasswordReset(tx, store, audit, c, ids, rand.Reader).Handle(t.Context(), appaccess.IssuePasswordResetCommand{
		Caller: caller, Member: m.ID, Request: governance.Request{Actor: appaccess.ActorOf(caller)},
	})
	if err != nil {
		t.Fatal(err)
	}
	testkit.RecordSecret(issued.Secret.Reveal())
	if _, err = appaccess.NewCompletePasswordReset(appaccess.CompletePasswordResetDeps{
		Tx: tx, Store: store, Settings: store, Hasher: password.NewHasher(cheap), Audit: audit,
		Clock: c, IDs: ids, Random: rand.Reader, Key: bytes.Repeat([]byte{1}, 32),
	}).Handle(t.Context(), appaccess.CompletePasswordResetCommand{Secret: issued.Secret, Password: secret.New(newPassword), ClientIP: anAddress()}); err != nil {
		t.Fatal(err)
	}
}

func TestAPasswordChangeHeldWhileItsMemberIsDeactivatedIsRefused(t *testing.T) {
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	testkit.RecordSecret("attacker-chosen-pass-9")
	err := holdAChangeOfPassword(t, m, func(access.Session) {
		c := clock.System{}
		if _, err := appaccess.NewDeactivateMember(postgres.NewTransactor(pool), pgaccess.NewStore(pool), testkit.Record(pool), c, id.NewSource(c)).
			Handle(t.Context(), m.ID, governance.Request{Actor: cli}); err != nil {
			t.Fatal(err)
		}
	})
	if codeOf(err) != "invalid_credentials" {
		t.Errorf("the change held while its member was deactivated: %v, want invalid_credentials", err)
	}
}

func TestAPasswordChangeHeldWhileItsSessionIsRevokedIsRefused(t *testing.T) {
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	testkit.RecordSecret("attacker-chosen-pass-9")
	err := holdAChangeOfPassword(t, m, func(s access.Session) {
		if _, err := pgaccess.NewStore(pool).RevokeSessions(t.Context(), []access.SessionID{s.ID}, time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if codeOf(err) != access.ErrSessionExpired.Code {
		t.Errorf("the change held while its session was revoked: %v, want %s", err, access.ErrSessionExpired.Code)
	}
	h := newSignIn(t, password.NewHasher(password.DefaultParams))
	if _, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New(m.Password), ClientIP: anAddress()}); err != nil {
		t.Errorf("the old password after the refused change: %v, want signed in", err)
	}
}
