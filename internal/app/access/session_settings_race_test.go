package access_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"sync"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// holdsIdentity tells that every member holds an identity at the enabled
// provider with the slug, as disabling local login asks of whoever does it.
type holdsIdentity string

func (h holdsIdentity) MemberIdentities(context.Context, access.MemberID) ([]access.IdentityMethod, error) {
	return []access.IdentityMethod{{ID: "identity", Provider: string(h), ProviderEnabled: true}}, nil
}

// vouchingProvider tells that every provider is enabled and vouches for
// every domain.
type vouchingProvider struct{ domain string }

func (v vouchingProvider) EnforcementProvider(context.Context, string) (appaccess.EnforcementProvider, bool, error) {
	return appaccess.EnforcementProvider{Enabled: true, Domains: []string{v.domain}}, true, nil
}

type noCache struct{}

func (noCache) Forget() {}

// staleSettings reads the settings as a replica that has not yet heard of a
// change does: the ones in force when it was made.
type staleSettings struct{ s access.AuthSettings }

func (s staleSettings) AuthSettings(context.Context) (access.AuthSettings, error) { return s.s, nil }

// updateAuthSettings stores change applied to the settings in force, as an
// instance admin does, and restores the settings when the test ends.
func updateAuthSettings(t *testing.T, change func(*access.AuthSettings), domain string) error {
	t.Helper()
	pool := testkit.Pool(t)
	store := pgaccess.NewStore(pool)
	before, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.SaveAuthSettings(context.WithoutCancel(t.Context()), before); err != nil {
			t.Error(err)
		}
	})
	admin := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
	c := clock.System{}
	h := appaccess.NewUpdateAuthSettings(appaccess.UpdateAuthSettingsDeps{
		Tx: postgres.NewTransactor(pool), Store: store, Cache: noCache{}, Providers: vouchingProvider{domain: domain},
		Identities: holdsIdentity("corp"), Audit: testkit.Record(pool), Clock: c, IDs: id.NewSource(c),
	})
	next := before
	change(&next)
	m := access.Member{ID: admin.ID, Email: admin.Email, Status: access.MemberActive, InstanceAdmin: true}
	_, err = h.Handle(t.Context(), appaccess.UpdateAuthSettingsCommand{
		Settings: next, Caller: access.Caller{Method: access.MethodSession, Member: &m},
		Request: governance.Request{Actor: appaccess.ActorOf(access.Caller{Method: access.MethodSession, Member: &m})},
	})
	return err
}

// aProvider creates an auth provider vouching for domain, which a domain can
// be enforced to, and returns its slug.
func aProvider(t *testing.T, domain string) string {
	t.Helper()
	slug := "idp-" + strings.ToLower(rand.Text())[:8]
	in, err := identity.CheckProviderInput(identity.ProviderInput{
		Slug: slug, Kind: identity.KindOIDC, DisplayName: slug, Enabled: true, Domains: []string{domain},
		OIDC: identity.OIDCInput{Issuer: "https://idp.invalid", ClientID: "flagnetic", ClientSecret: secret.New("unused")},
	})
	if err != nil {
		t.Fatal(err)
	}
	pid := id.New[identity.Provider](id.NewSource(clock.System{}))
	if err = pgidentity.NewStore(testkit.Pool(t)).CreateProvider(t.Context(), identity.New(pid, in, "sealed", time.Now())); err != nil {
		t.Fatal(err)
	}
	return slug
}

func liveSessionsOf(t *testing.T, m access.Member) []access.Session {
	t.Helper()
	store := pgaccess.NewStore(testkit.Pool(t))
	settings, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := store.MemberSessions(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return access.LiveSessions(sessions, m, time.Now(), settings.SessionTimeouts())
}

func TestASignInHeldWhileLocalLoginIsSwitchedOffOpensNoSession(t *testing.T) {
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	member := access.Member{ID: m.ID, Email: m.Email, Status: access.MemberActive}
	v := &heldVerify{Hasher: password.NewHasher(password.DefaultParams), verified: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := newSignIn(t, v).Handle(t.Context(), appaccess.SignInCommand{Email: m.Email, Password: secret.New(m.Password), ClientIP: anAddress()})
		result <- err
	}()
	<-v.verified
	if err := updateAuthSettings(t, func(s *access.AuthSettings) { s.LocalLoginEnabled = false }, "corp.example"); err != nil {
		t.Fatal(err)
	}
	close(v.release)
	if err := <-result; codeOf(err) != "local_login_disabled" {
		t.Errorf("the sign-in held while local login was switched off: %v, want local_login_disabled", err)
	}
	if live := liveSessionsOf(t, member); len(live) != 0 {
		t.Errorf("%d live sessions of the member, want none", len(live))
	}
}

// TestASingleSignOnSessionOpenedWhileItsDomainIsEnforcedToAnotherProviderIsEnded opens the session
// inside a unit of work that stays open while the settings change starts,
// so the change waits on it and ends the session once it commits: with the
// settings read from a cache that has not heard of the change.
func TestASingleSignOnSessionOpenedWhileItsDomainIsEnforcedToAnotherProviderIsEnded(t *testing.T) {
	pool := testkit.Pool(t)
	const domain = "enforced-sso.example"
	other := aProvider(t, domain)
	m := testkit.NewMember(t, pool, testkit.WithEmail("ada@"+domain))
	member := access.Member{ID: m.ID, Email: m.Email, Status: access.MemberActive}
	store := pgaccess.NewStore(pool)
	before, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.Clock()
	sso := appaccess.NewSSO(store, staleSettings{before}, testkit.Record(pool), c, id.NewSource(c), rand.Reader, bytes.Repeat([]byte{1}, 32))

	var (
		wg       sync.WaitGroup
		enforced error
	)
	err = postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
		_, openErr := sso.OpenSession(ctx, appaccess.SSOSignIn{Member: member, Method: access.SignInOIDC, Provider: "a-provider", ClientIP: anAddress()})
		if openErr != nil {
			return openErr
		}
		wg.Go(func() {
			enforced = updateAuthSettings(t, func(s *access.AuthSettings) {
				s.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: domain, Provider: other}}
			}, domain)
		})
		// The change waits for this unit of work, which holds the settings
		// shared, however long it takes to commit.
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	wg.Wait()
	if enforced != nil {
		t.Fatalf("enforcing the domain: %v", enforced)
	}
	if err != nil {
		t.Fatalf("the sign-in, opened before the change began: %v", err)
	}
	if live := liveSessionsOf(t, member); len(live) != 0 {
		t.Errorf("%d live sessions through provider a once the domain is enforced to b, want none", len(live))
	}
}

func TestASingleSignOnSignInStartedUnderStaleSettingsIsRefusedOnceTheDomainIsEnforcedToAnotherProvider(t *testing.T) {
	pool := testkit.Pool(t)
	const domain = "enforced-late.example"
	other := aProvider(t, domain)
	m := testkit.NewMember(t, pool, testkit.WithEmail("bob@"+domain))
	member := access.Member{ID: m.ID, Email: m.Email, Status: access.MemberActive}
	store := pgaccess.NewStore(pool)
	before, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.Clock()
	sso := appaccess.NewSSO(store, staleSettings{before}, testkit.Record(pool), c, id.NewSource(c), rand.Reader, bytes.Repeat([]byte{1}, 32))
	if err = updateAuthSettings(t, func(s *access.AuthSettings) {
		s.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: domain, Provider: other}}
	}, domain); err != nil {
		t.Fatal(err)
	}
	err = postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
		_, openErr := sso.OpenSession(ctx, appaccess.SSOSignIn{Member: member, Method: access.SignInOIDC, Provider: "a-provider", ClientIP: anAddress()})
		return openErr
	})
	if codeOf(err) != "sso_required" {
		t.Errorf("the sign-in through a once the domain is enforced to b: %v, want sso_required", err)
	}
	if live := liveSessionsOf(t, member); len(live) != 0 {
		t.Errorf("%d live sessions, want none", len(live))
	}
}

// closeLocalLogin stores the settings in force with local login switched off,
// as a change that commits after a handler read them does, and returns what
// restores them; the test restores them too when it ends.
func closeLocalLogin(t *testing.T) (open func()) {
	t.Helper()
	store := pgaccess.NewStore(testkit.Pool(t))
	before, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	open = func() {
		if err := store.SaveAuthSettings(context.WithoutCancel(t.Context()), before); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(open)
	closed := before
	closed.LocalLoginEnabled = false
	if err = store.SaveAuthSettings(t.Context(), closed); err != nil {
		t.Fatal(err)
	}
	return open
}

func TestAPasswordResetCompletedUnderStaleSettingsIsRefusedOnceLocalLoginIsClosedAndStaysOpen(t *testing.T) {
	pool := testkit.Pool(t)
	c := clock.System{}
	tx, store, audit, ids := postgres.NewTransactor(pool), pgaccess.NewStore(pool), testkit.Record(pool), id.NewSource(c)
	m := testkit.NewMember(t, pool)
	member := access.Member{ID: m.ID, Email: m.Email, Status: access.MemberActive}
	admin := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
	caller := access.Caller{Method: access.MethodSession, Member: &access.Member{ID: admin.ID, Email: admin.Email, Status: access.MemberActive, InstanceAdmin: true}}
	issued, err := appaccess.NewIssuePasswordReset(tx, store, audit, c, ids, rand.Reader).Handle(t.Context(), appaccess.IssuePasswordResetCommand{
		Caller: caller, Member: m.ID, Request: governance.Request{Actor: appaccess.ActorOf(caller)},
	})
	if err != nil {
		t.Fatal(err)
	}
	testkit.RecordSecret(issued.Secret.Reveal())
	before, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	complete := appaccess.NewCompletePasswordReset(appaccess.CompletePasswordResetDeps{
		Tx: tx, Store: store, Settings: staleSettings{before}, Hasher: password.NewHasher(cheap), Audit: audit,
		Clock: c, IDs: ids, Random: rand.Reader, Key: bytes.Repeat([]byte{1}, 32),
	})
	cmd := appaccess.CompletePasswordResetCommand{Secret: issued.Secret, Password: secret.New("harbour-lantern-quiet-4410"), ClientIP: anAddress()}
	open := closeLocalLogin(t)

	if _, err = complete.Handle(t.Context(), cmd); codeOf(err) != "local_login_disabled" {
		t.Fatalf("completing the reset once local login is closed: %v, want local_login_disabled", err)
	}
	if live := liveSessionsOf(t, member); len(live) != 0 {
		t.Errorf("%d live sessions of the member, want none", len(live))
	}
	open()
	if _, err = complete.Handle(t.Context(), cmd); err != nil {
		t.Errorf("completing the reset once local login is open again: %v, want it still open", err)
	}
}

func TestAnInvitationAcceptedUnderStaleSettingsIsRefusedOnceLocalLoginIsClosedAndStaysOpen(t *testing.T) {
	pool := testkit.Pool(t)
	c := clock.System{}
	tx, store, audit, ids := postgres.NewTransactor(pool), pgaccess.NewStore(pool), testkit.Record(pool), id.NewSource(c)
	invited, err := appaccess.NewInviteMember(tx, store, audit, c, ids, rand.Reader, nil).Handle(t.Context(), appaccess.InviteMemberCommand{
		Email: strings.ToLower(rand.Text()) + "@example.com", DisplayName: "Invited", Request: governance.Request{Actor: cli},
	})
	if err != nil {
		t.Fatal(err)
	}
	testkit.RecordSecret(invited.Invitation.Secret.Reveal())
	before, err := store.AuthSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	accept := appaccess.NewAcceptInvitation(appaccess.AcceptInvitationDeps{
		Tx: tx, Store: store, Settings: staleSettings{before}, Hasher: password.NewHasher(cheap), Audit: audit,
		Clock: c, IDs: ids, Random: rand.Reader, Key: bytes.Repeat([]byte{1}, 32),
	})
	cmd := appaccess.AcceptInvitationCommand{Secret: invited.Invitation.Secret, Password: secret.New("harbour-lantern-quiet-4410"), ClientIP: anAddress()}
	open := closeLocalLogin(t)

	if _, err = accept.Handle(t.Context(), cmd); codeOf(err) != "local_login_disabled" {
		t.Fatalf("accepting the invitation once local login is closed: %v, want local_login_disabled", err)
	}
	m, _, err := store.LockMember(t.Context(), invited.Member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != access.MemberInvited {
		t.Errorf("the member is %s once the acceptance was refused, want invited", m.Status)
	}
	open()
	if _, err = accept.Handle(t.Context(), cmd); err != nil {
		t.Errorf("accepting the invitation once local login is open again: %v, want it still open", err)
	}
}
