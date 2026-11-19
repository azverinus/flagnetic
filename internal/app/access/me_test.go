package access_test

import (
	"context"
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
	err := holdAChangeOfPassword(t, m, func(access.Session) {
		hash, err := password.NewHasher(password.DefaultParams).Hash(t.Context(), secret.New(reset))
		if err != nil {
			t.Fatal(err)
		}
		store := pgaccess.NewStore(pool)
		if err := postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
			if err := store.SetPasswordHash(ctx, m.ID, hash, time.Now()); err != nil {
				return err
			}
			_, err := store.RevokeMemberSessions(ctx, m.ID, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})
	if codeOf(err) != "invalid_credentials" {
		t.Errorf("the change held while a reset completed: %v, want invalid_credentials", err)
	}
	h := newSignIn(t, password.NewHasher(password.DefaultParams))
	if _, err := h.Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New(reset), ClientIP: anAddress()}); err != nil {
		t.Errorf("the password the reset set: %v, want signed in", err)
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
