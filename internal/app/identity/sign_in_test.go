package identity_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"testing"
	"time"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// outsideProvider is the identity provider the instance talks to, an outside
// party: it answers what a test scripts.
type outsideProvider struct {
	authErr, exchangeErr error
	claims               identity.VerifiedClaims
}

func (p *outsideProvider) AuthorizationURL(_ context.Context, cfg appidentity.OIDCConfig, state, _, _ string) (string, error) {
	if p.authErr != nil {
		return "", p.authErr
	}
	return cfg.Issuer + "/authorize?state=" + state, nil
}

func (p *outsideProvider) Exchange(context.Context, appidentity.OIDCConfig, string, string, string) (identity.VerifiedClaims, error) {
	return p.claims, p.exchangeErr
}

type signInSetup struct {
	deps     appidentity.SignInDeps
	provider identity.Provider
	outside  *outsideProvider
	clock    *clock.Fake
}

func newSignIn(t *testing.T, base *url.URL) signInSetup {
	t.Helper()
	pd := deps(t)
	p, err := appidentity.NewCreateProvider(pd).Handle(t.Context(), appidentity.CreateProviderCommand{Input: input(), Request: admin})
	if err != nil {
		t.Fatal(err)
	}
	pool := testkit.Pool(t)
	c := testkit.Clock()
	store := pgidentity.NewStore(pool)
	outside := &outsideProvider{claims: identity.VerifiedClaims{Subject: "s", Email: "ada@example.com", EmailVerified: true}}
	return signInSetup{
		deps: appidentity.SignInDeps{
			Tx: postgres.NewTransactor(pool), Providers: store, Attempts: store, Identities: store, OIDC: outside,
			Opener: secret.NewKeyring(secret.New(testkit.SecretKey)), Audit: testkit.Record(pool), Clock: c,
			IDs: id.NewSource(c), Random: rand.Reader, BaseURL: base,
		},
		provider: p, outside: outside, clock: c,
	}
}

var publicBase = &url.URL{Scheme: "https", Host: "flags.example.com"}

func failureOf(err error) appidentity.FailureCode {
	var f *appidentity.SignInFailure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func TestASignInFailureNamesItsCodeAndItsCause(t *testing.T) {
	cause := errors.New("the token expired")
	failure := &appidentity.SignInFailure{Code: appidentity.CodeSSOFailed, Cause: cause}
	if failure.Error() != "sso_failed: the token expired" || !errors.Is(failure, cause) {
		t.Errorf("failure = %q, unwraps to its cause: %v", failure, errors.Is(failure, cause))
	}
	if bare := (&appidentity.SignInFailure{Code: appidentity.CodeMemberNotFound}); bare.Error() != "member_not_found" {
		t.Errorf("a failure without a cause = %q", bare)
	}
}

func TestASignInCannotStartWhereTheInstanceHasNoPublicURLOrTheProviderCannotBeReached(t *testing.T) {
	cmd := func(s signInSetup) appidentity.StartCommand { return appidentity.StartCommand{Slug: s.provider.Slug} }

	noURL := newSignIn(t, nil)
	if _, err := appidentity.NewStartSignIn(noURL.deps).Handle(t.Context(), cmd(noURL)); failureOf(err) != appidentity.CodeSSOFailed {
		t.Errorf("starting without a public URL = %v, want sso_failed", err)
	}

	unreachable := newSignIn(t, publicBase)
	unreachable.outside.authErr = errors.New("connection refused")
	if _, err := appidentity.NewStartSignIn(unreachable.deps).Handle(t.Context(), cmd(unreachable)); failureOf(err) != appidentity.CodeSSOFailed {
		t.Errorf("starting at an unreachable provider = %v, want sso_failed", err)
	}
}

// started starts a sign-in and returns the state the provider is sent and the
// cookie the browser holds.
func started(t *testing.T, s signInSetup) (state, cookie string) {
	t.Helper()
	res, err := appidentity.NewStartSignIn(s.deps).Handle(t.Context(), appidentity.StartCommand{Slug: s.provider.Slug})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(res.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state"), res.Cookie.Reveal()
}

func TestACallbackThatCannotBeVerifiedEndsTheAttempt(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func(state string) appidentity.CallbackCommand
		fail func(*outsideProvider)
		want appidentity.FailureCode
	}{
		{"the provider's error", func(state string) appidentity.CallbackCommand {
			return appidentity.CallbackCommand{State: state, IDPError: "access_denied", Code: "c"}
		}, nil, appidentity.CodeIDPError},
		{"no code", func(state string) appidentity.CallbackCommand { return appidentity.CallbackCommand{State: state} }, nil, appidentity.CodeSSOFailed},
		{"a code the provider refuses", func(state string) appidentity.CallbackCommand {
			return appidentity.CallbackCommand{State: state, Code: "c"}
		}, func(p *outsideProvider) { p.exchangeErr = errors.New("invalid_grant") }, appidentity.CodeSSOFailed},
		{"an unknown state", func(string) appidentity.CallbackCommand {
			return appidentity.CallbackCommand{State: "unknown", Code: "c"}
		}, nil, appidentity.CodeSSOFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSignIn(t, publicBase)
			state, _ := started(t, s)
			if tc.fail != nil {
				tc.fail(s.outside)
			}
			cmd := tc.cmd(state)
			cmd.Slug = s.provider.Slug
			callback := appidentity.NewCompleteCallback(s.deps)

			if _, err := callback.Handle(t.Context(), cmd); failureOf(err) != tc.want {
				t.Fatalf("callback = %v, want %s", err, tc.want)
			}
			if tc.name == "an unknown state" {
				return
			}
			// The attempt is spent: the provider's real answer finds nothing.
			if _, err := callback.Handle(t.Context(), appidentity.CallbackCommand{Slug: cmd.Slug, State: state, Code: "c"}); failureOf(err) != appidentity.CodeSSOFailed {
				t.Errorf("the answer after a failed one = %v, want sso_failed", err)
			}
		})
	}
}

func TestAStateOfAnotherProvidersAttemptIsRefused(t *testing.T) {
	one, other := newSignIn(t, publicBase), newSignIn(t, publicBase)
	state, _ := started(t, one)

	_, err := appidentity.NewCompleteCallback(other.deps).Handle(t.Context(), appidentity.CallbackCommand{
		Slug: other.provider.Slug, State: state, Code: "c",
	})

	if failureOf(err) != appidentity.CodeSSOFailed {
		t.Errorf("a state of another provider = %v, want sso_failed", err)
	}
}

func TestACompletionNeedsALiveAttemptAndAnEnabledProvider(t *testing.T) {
	complete := func(s signInSetup, token, cookie string) error {
		_, err := appidentity.NewCompleteSignIn(s.deps).Handle(t.Context(), appidentity.CompleteCommand{Token: token, Cookie: cookie})
		return err
	}
	answered := func(s signInSetup) (token, cookie string) {
		state, cookie := started(t, s)
		token, err := appidentity.NewCompleteCallback(s.deps).Handle(t.Context(), appidentity.CallbackCommand{
			Slug: s.provider.Slug, State: state, Code: "c",
		})
		if err != nil {
			t.Fatal(err)
		}
		return token, cookie
	}

	expired := newSignIn(t, publicBase)
	token, cookie := answered(expired)
	expired.clock.Advance(identity.AttemptLifetime + time.Second)
	if err := complete(expired, token, cookie); failureOf(err) != appidentity.CodeSSOFailed {
		t.Errorf("completing an expired attempt = %v, want sso_failed", err)
	}

	disabled := newSignIn(t, publicBase)
	token, cookie = answered(disabled)
	pd := deps(t)
	if _, err := appidentity.NewDisableProvider(pd).Handle(t.Context(), appidentity.ProviderCommand{Slug: disabled.provider.Slug, Request: admin}); err != nil {
		t.Fatal(err)
	}
	if err := complete(disabled, token, cookie); failureOf(err) != appidentity.CodeSSOFailed {
		t.Errorf("completing at a disabled provider = %v, want sso_failed", err)
	}
}
