package identity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// FailureCode is why a sign-in did not end with a session, as the sign-in
// page is told.
type FailureCode string

// The codes the sign-in page is redirected with.
const (
	// CodeSSOFailed: the sign-in could not be verified (protocol, signature,
	// state, an attempt gone) or the instance could not carry it out.
	CodeSSOFailed FailureCode = "sso_failed"
	// CodeIDPError: the identity provider refused the sign-in itself.
	CodeIDPError FailureCode = "idp_error"
	// CodeMemberNotFound: no member matches the person.
	CodeMemberNotFound FailureCode = "member_not_found"
	// CodeMemberDeactivated: the member was deactivated.
	CodeMemberDeactivated FailureCode = "member_deactivated"
	// CodeAttemptMismatch: the browser completing the sign-in is not the one
	// that started it.
	CodeAttemptMismatch FailureCode = "attempt_mismatch"
	// CodeIdentityLinked: linking an identity found its subject linked to
	// another member.
	CodeIdentityLinked FailureCode = "identity_linked"
	// CodeSSORequired: the person's email domain is enforced to another
	// provider, which the failure names.
	CodeSSORequired FailureCode = "sso_required"
)

// SignInFailure ends a sign-in with a code for the sign-in page.
type SignInFailure struct {
	Code  FailureCode
	Cause error
	// Kind is the protocol of the provider the sign-in was through, empty
	// when it failed before the provider was known.
	Kind identity.Kind
	// Intent is what the attempt was for, empty when it failed before the
	// attempt was known. The browser of a link lands on the page the member
	// started it from; one that signs in lands on the sign-in page.
	Intent identity.Intent
	// Provider is the slug of the provider to sign in through instead, for
	// CodeSSORequired.
	Provider string
}

func (f *SignInFailure) Error() string {
	if f.Cause == nil {
		return string(f.Code)
	}
	return string(f.Code) + ": " + f.Cause.Error()
}

func (f *SignInFailure) Unwrap() error { return f.Cause }

func fail(code FailureCode, cause error) error { return &SignInFailure{Code: code, Cause: cause} }

// requireSSO refuses a sign-in or link of an email whose domain is enforced
// to another provider, naming that one.
func requireSSO(provider string) error {
	return &SignInFailure{Code: CodeSSORequired, Provider: provider,
		Cause: errors.New("the email's domain is enforced to the auth provider " + provider)}
}

// refuseEnforced returns requireSSO when email's domain is enforced to a
// provider other than p; nil otherwise.
func refuseEnforced(ctx context.Context, members Members, p identity.Provider, email string) error {
	enforced, err := members.EnforcedProvider(ctx, email)
	if err != nil || enforced == "" || enforced == p.Slug {
		return err
	}
	return requireSSO(enforced)
}

// inKind names kind as the protocol of the sign-in err ends, if err is a
// failure that does not say yet.
func inKind(kind identity.Kind, err error) error {
	var f *SignInFailure
	if errors.As(err, &f) && f.Kind == "" {
		f.Kind = kind
	}
	return err
}

// inIntent names the intent of the attempt as that of the sign-in err ends, if
// err is a failure that does not say yet.
func inIntent(intent identity.Intent, err error) error {
	var f *SignInFailure
	if errors.As(err, &f) && f.Intent == "" {
		f.Intent = intent
	}
	return err
}

// AttemptStore keeps sign-in attempts.
type AttemptStore interface {
	// CreateAttempt stores a, and deletes the attempts that expired before
	// now, so abandoned ones do not pile up.
	CreateAttempt(ctx context.Context, a identity.Attempt, now time.Time) error
	// TakeCallback marks the live attempt with the state's hash as answered
	// at now and returns it, in one step; ErrNotFound when there is none, it
	// was answered already or it expired.
	TakeCallback(ctx context.Context, state identity.SecretHash, now time.Time) (identity.Attempt, error)
	// StoreClaims stores the verified claims on the attempt and the hash of
	// the token its browser completes with.
	StoreClaims(ctx context.Context, attempt identity.AttemptID, completion identity.SecretHash, c identity.VerifiedClaims) error
	DeleteAttempt(ctx context.Context, attempt identity.AttemptID) error
	// TakeCompletion deletes the attempt completing with the token's hash and
	// returns it, claims included; ErrNotFound when there is none.
	TakeCompletion(ctx context.Context, completion identity.SecretHash) (identity.Attempt, error)
}

// LinkStore keeps identities.
type LinkStore interface {
	// IdentityBySubject returns the identity linking subject at provider,
	// locked until the unit of work ends; ErrNotFound when there is none.
	IdentityBySubject(ctx context.Context, provider identity.ProviderID, subject string) (identity.Identity, error)
	// CreateIdentity stores i; ErrIdentityLinked when its subject is linked at
	// its provider already.
	CreateIdentity(ctx context.Context, i identity.Identity) error
	// RecordIdentitySignIn stores that the identity signed in at at.
	RecordIdentitySignIn(ctx context.Context, i identity.ID, at time.Time) error
	// MemberIdentities returns the identities member holds, oldest first, each
	// with the provider it is at.
	MemberIdentities(ctx context.Context, member identity.MemberID) ([]LinkedIdentity, error)
	// DeleteIdentity deletes the identity.
	DeleteIdentity(ctx context.Context, i identity.ID) error
}

// MemberRecord is a member as the identity context sees them.
type MemberRecord struct {
	identity.KnownMember
	Email       string
	DisplayName string
	// Provider is the slug of the auth provider whose SCIM token provisioned
	// the member, "" for one it did not.
	Provider string
}

// Members is what the identity context asks of the access context, acting in
// the unit of work the context it is given carries.
type Members interface {
	// MemberByEmail returns the member holding email whatever its case; false
	// when none does.
	MemberByEmail(ctx context.Context, email string) (MemberRecord, bool, error)
	// MemberByID returns the member; false when none has the identifier.
	MemberByID(ctx context.Context, member identity.MemberID) (MemberRecord, bool, error)
	// MemberByExternalID returns the member the provider with this slug
	// provisioned with externalID as their external ID; false when none.
	MemberByExternalID(ctx context.Context, provider, externalID string) (MemberRecord, bool, error)
	// ProvisionMember creates an active member without a password and records
	// that provider provisioned them.
	ProvisionMember(ctx context.Context, email, displayName string, kind identity.Kind, provider, requestID string) (MemberRecord, error)
	// OpenSession opens a session of member signed in through provider and
	// returns the value of its cookie.
	OpenSession(ctx context.Context, s SessionRequest) (secret.String, error)
	// RecordFailedSignIn records that member could not sign in through
	// provider, and why.
	RecordFailedSignIn(ctx context.Context, member MemberRecord, kind identity.Kind, provider, reason, requestID string) error
	// LockMember returns the member, locked until the unit of work ends, so
	// the changes to their ways in are made one at a time; false when none has
	// the identifier.
	LockMember(ctx context.Context, member identity.MemberID) (MemberRecord, bool, error)
	// PasswordSetAt returns when the member set the password they hold, nil
	// when they hold none.
	PasswordSetAt(ctx context.Context, member identity.MemberID) (*time.Time, error)
	// EnforcedProvider returns the slug of the auth provider email's domain
	// is enforced to; "" when it is not.
	EnforcedProvider(ctx context.Context, email string) (string, error)
	// EnsureWayIn refuses, with the conflict last_auth_method, unlinking the
	// identity without ("" for none) when no other usable way in would remain
	// to member, whom the caller holds locked.
	EnsureWayIn(ctx context.Context, member MemberRecord, without identity.ID) error
}

// SessionResolver tells who a session cookie signs in.
type SessionResolver interface {
	// SessionOf returns the member and the session the cookie signs in; false
	// when it signs nobody in.
	SessionOf(ctx context.Context, cookie string) (identity.MemberID, identity.SessionID, bool, error)
}

// LinkedIdentity is an identity with the provider it is at.
type LinkedIdentity struct {
	identity.Identity
	ProviderSlug string
	ProviderName string
	Kind         identity.Kind
	// ProviderEnabled is whether the provider is enabled: an identity at a
	// disabled one signs nobody in.
	ProviderEnabled bool
}

// ErrIdentityLinked refuses linking a subject that is linked to a member at
// its provider already.
var ErrIdentityLinked = errors.New("identity: the subject is already linked")

// SessionRequest asks for the session of a member who signed in.
type SessionRequest struct {
	Member MemberRecord
	// Kind is the protocol of the provider, Provider its slug.
	Kind      identity.Kind
	Provider  string
	ClientIP  netip.Addr
	UserAgent string
	RequestID string
}

// OIDCConfig is what the instance presents to an OIDC provider as a client.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret secret.String
	RedirectURL  string
	Scopes       []string
	Claims       identity.Claims
}

// OIDCClient speaks the OpenID Connect authorization code flow with PKCE.
type OIDCClient interface {
	// AuthorizationURL returns where a browser is sent to authenticate, for
	// an attempt with this state, nonce and code verifier.
	AuthorizationURL(ctx context.Context, cfg OIDCConfig, state, nonce, codeVerifier string) (string, error)
	// Exchange trades code for tokens and verifies the ID token: signature,
	// issuer, audience, expiry and nonce. It reads the claims cfg names,
	// asking the provider's UserInfo for the email when the token holds none.
	Exchange(ctx context.Context, cfg OIDCConfig, code, codeVerifier, nonce string) (identity.VerifiedClaims, error)
}

// Opener opens a value sealed under the instance's secret key.
type Opener interface {
	Open(purpose secret.Purpose, sealed string) ([]byte, error)
}

// SignInDeps are what the sign-in use cases run on.
type SignInDeps struct {
	Tx         Transactor
	Providers  ProviderStore
	Attempts   AttemptStore
	Identities LinkStore
	Members    Members
	// Sessions tells who the session a link completes in belongs to.
	Sessions SessionResolver
	// Mappings and Access give a member what the provider's group mappings
	// map the groups they claim to.
	Mappings MappingStore
	Access   ProviderAccess
	Logger   *slog.Logger
	OIDC     OIDCClient
	SAML     SAMLClient
	Replays  ReplayStore
	Opener   Opener
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	Random   io.Reader
	// BaseURL is the instance's public origin, nil when not configured.
	BaseURL *url.URL
}

func (d SignInDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

// enabledProvider returns the enabled provider with slug and one of kinds,
// every kind when none is named; a disabled one, or one of another kind, is
// not found, so its endpoints answer as unknown ones do.
func (d SignInDeps) enabledProvider(ctx context.Context, slug string, kinds ...identity.Kind) (identity.Provider, error) {
	p, err := providerOf(ctx, d.Providers, slug)
	if err != nil {
		return p, err
	}
	if !p.Enabled || (len(kinds) > 0 && !slices.Contains(kinds, p.Kind)) {
		return p, errProviderNotFound
	}
	return p, nil
}

func (d SignInDeps) oidcConfig(p identity.Provider) (OIDCConfig, error) {
	if d.BaseURL == nil {
		return OIDCConfig{}, errors.New("the instance has no public URL")
	}
	plain, err := d.Opener.Open(secret.PurposeOIDCClientSecret, p.OIDC.SealedClientSecret)
	if err != nil {
		return OIDCConfig{}, fmt.Errorf("opening the client secret of %s: %w", p.Slug, err)
	}
	return OIDCConfig{
		Issuer: p.OIDC.Issuer, ClientID: p.OIDC.ClientID, ClientSecret: secret.New(string(plain)),
		RedirectURL: identity.RedirectURI(d.BaseURL, p.Slug), Scopes: p.OIDC.Scopes, Claims: p.OIDC.Claims,
	}, nil
}

// StartSignIn begins a sign-in through an auth provider.
type StartSignIn struct{ d SignInDeps }

// NewStartSignIn returns the handler.
func NewStartSignIn(d SignInDeps) *StartSignIn { return &StartSignIn{d: d} }

// StartCommand asks to sign in through the provider Slug and return to
// ReturnTo afterwards.
type StartCommand struct {
	Slug     string
	ReturnTo string
}

// Started is where the browser goes, and the cookie that binds the attempt to
// it.
type Started struct {
	RedirectURL string
	// Cookie is the value of the cookie to set; the attempt keeps only its
	// hash.
	Cookie secret.String
	// Lifetime is how long the cookie lives.
	Lifetime time.Duration
}

// Handle creates an attempt bound to a new cookie and answers the provider's
// authorization address. A disabled or unknown provider is not found; one the
// instance cannot reach is SignInFailure sso_failed.
func (h *StartSignIn) Handle(ctx context.Context, cmd StartCommand) (Started, error) {
	return h.start(ctx, cmd.Slug, cmd.ReturnTo, nil)
}

// linkBinding is the member and session a link attempt is bound to.
type linkBinding struct {
	member  identity.MemberID
	session identity.SessionID
}

// start creates the attempt of a sign-in, or of a link when link is not nil,
// and answers where the browser goes.
func (h *StartSignIn) start(ctx context.Context, slug, returnTo string, link *linkBinding) (started Started, err error) {
	p, err := h.d.enabledProvider(ctx, slug)
	if err != nil {
		return Started{}, err
	}
	defer func() { err = inKind(p.Kind, err) }()
	if p.Kind == identity.KindSAML {
		return h.startSAML(ctx, p, returnTo, link)
	}
	cfg, err := h.d.oidcConfig(p)
	if err != nil {
		return Started{}, fail(CodeSSOFailed, err)
	}
	now := h.d.now()
	secrets := identity.NewSecrets(h.d.Random)
	attemptID := id.New[identity.Attempt](h.d.IDs)
	attempt := identity.NewLoginAttempt(attemptID, p.ID, returnTo, secrets, now)
	if link != nil {
		attempt = identity.NewLinkAttempt(attemptID, p.ID, link.member, link.session, returnTo, "", secrets, now)
	}
	redirect, err := h.d.OIDC.AuthorizationURL(ctx, cfg, secrets.State, attempt.Nonce, attempt.CodeVerifier)
	if err != nil {
		return Started{}, fail(CodeSSOFailed, err)
	}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error { return h.d.Attempts.CreateAttempt(ctx, attempt, now) })
	if err != nil {
		return Started{}, err
	}
	return Started{RedirectURL: redirect, Cookie: secret.New(secrets.Cookie), Lifetime: identity.AttemptLifetime}, nil
}

// CompleteCallback takes the answer of an identity provider.
type CompleteCallback struct{ d SignInDeps }

// NewCompleteCallback returns the handler.
func NewCompleteCallback(d SignInDeps) *CompleteCallback { return &CompleteCallback{d: d} }

// CallbackCommand is what the identity provider's redirect to the callback
// carried.
type CallbackCommand struct {
	Slug  string
	State string
	Code  string
	// IDPError is the provider's error parameter, when it refused the sign-in.
	IDPError string
}

// Handle verifies the provider's answer and stores the person's claims on
// the attempt, changing nothing about any member, and returns the token the
// browser completes the sign-in with. The attempt is answered once: a state
// seen again, unknown or expired is SignInFailure sso_failed, and an answer
// that does not verify ends the attempt.
func (h *CompleteCallback) Handle(ctx context.Context, cmd CallbackCommand) (token string, err error) {
	p, err := h.d.enabledProvider(ctx, cmd.Slug, identity.KindOIDC)
	if err != nil {
		return "", err
	}
	defer func() { err = inKind(p.Kind, err) }()
	attempt, err := h.d.Attempts.TakeCallback(ctx, identity.HashSecret(cmd.State), h.d.now())
	if errors.Is(err, ErrNotFound) {
		return "", fail(CodeSSOFailed, errors.New("the state names no live attempt"))
	}
	if err != nil {
		return "", err
	}
	if attempt.Provider != p.ID {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, errors.New("the state belongs to another provider")))
	}
	if cmd.IDPError != "" {
		return "", h.end(ctx, attempt, fail(CodeIDPError, errors.New("the identity provider answered "+clip(cmd.IDPError, 100))))
	}
	cfg, err := h.d.oidcConfig(p)
	if err != nil {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, err))
	}
	if cmd.Code == "" {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, errors.New("the callback carries no code")))
	}
	claims, err := h.d.OIDC.Exchange(ctx, cfg, cmd.Code, attempt.CodeVerifier, attempt.Nonce)
	if err != nil {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, err))
	}
	token = identity.NewToken(h.d.Random)
	if err = h.d.Attempts.StoreClaims(ctx, attempt.ID, identity.HashSecret(token), claims); err != nil {
		return "", err
	}
	return token, nil
}

// end deletes an attempt that cannot go on, and returns why.
func (h *CompleteCallback) end(ctx context.Context, a identity.Attempt, why error) error {
	return errors.Join(inIntent(a.Intent, why), h.d.Attempts.DeleteAttempt(context.WithoutCancel(ctx), a.ID))
}

// CompleteSignIn completes a sign-in in the browser that started it.
type CompleteSignIn struct{ d SignInDeps }

// NewCompleteSignIn returns the handler.
func NewCompleteSignIn(d SignInDeps) *CompleteSignIn { return &CompleteSignIn{d: d} }

// CompleteCommand completes the sign-in the token names, presenting the
// browser's attempt cookie.
type CompleteCommand struct {
	Token string
	// Cookie is the value of the attempt cookie the browser sent, "" for none.
	Cookie string
	// SessionCookie is the value of the session cookie the browser sent, ""
	// for none: a link completes only in the session that started it.
	SessionCookie string
	ClientIP      netip.Addr
	UserAgent     string
	RequestID     string
}

// SignedIn is the session a sign-in opened and where its browser goes next.
type SignedIn struct {
	// Cookie is the value of the session cookie.
	Cookie   secret.String
	ReturnTo string
	// Kind is the protocol of the provider the member signed in through.
	Kind identity.Kind
	// Linked is true when the attempt linked an identity to the member signed
	// in already, who needs no session: Cookie is then empty.
	Linked bool
}

// unresolved refuses a person no rule lets in; completion turns it into a
// failure code once its unit of work has rolled back.
type unresolved struct {
	err    error
	member MemberRecord
	// known is whether the person is that member: linked to them, or holding their
	// email where the provider vouches for it. A member merely named by an
	// email the provider does not vouch for is not known, so nobody can fill a
	// member's audit trail with refusals by claiming their address.
	known bool
}

// reasonNotLinked is why an active member could not sign in: the subject is
// not linked to them and the provider does not link by email.
const reasonNotLinked = "not_linked"

func (u *unresolved) Error() string { return u.err.Error() }
func (u *unresolved) Unwrap() error { return u.err }

// Handle deletes the attempt, so it completes once, and checks the
// browser's cookie against the one the attempt was bound to, in constant
// time: another browser changes nothing. Then, in one transaction, it
// resolves the verified person to a member, provisions or links as the
// provider allows, and opens the session. A person who matches no member who
// may sign in is SignInFailure member_not_found or member_deactivated; one
// whose email's domain is enforced to another provider is sso_required,
// naming it.
func (h *CompleteSignIn) Handle(ctx context.Context, cmd CompleteCommand) (signed SignedIn, err error) {
	now := h.d.now()
	attempt, err := h.d.Attempts.TakeCompletion(ctx, identity.HashSecret(cmd.Token))
	if errors.Is(err, ErrNotFound) {
		return SignedIn{}, fail(CodeSSOFailed, errors.New("the completion names no attempt"))
	}
	if err != nil {
		return SignedIn{}, err
	}
	defer func() { err = inIntent(attempt.Intent, err) }()
	if !attempt.Live(now) || attempt.Claims == nil {
		return SignedIn{}, fail(CodeSSOFailed, errors.New("the attempt expired"))
	}
	if cmd.Cookie == "" || !identity.HashSecret(cmd.Cookie).Equal(attempt.CookieHash) {
		return SignedIn{}, fail(CodeAttemptMismatch, errors.New("the browser did not start the attempt"))
	}
	p, err := h.d.Providers.ProviderByID(ctx, attempt.Provider)
	if errors.Is(err, ErrNotFound) || (err == nil && !p.Enabled) {
		return SignedIn{}, fail(CodeSSOFailed, errors.New("the provider is gone or disabled"))
	}
	if err != nil {
		return SignedIn{}, err
	}
	defer func() { err = inKind(p.Kind, err) }()
	if attempt.Intent == identity.IntentLink {
		return h.link(ctx, p, attempt, cmd)
	}
	out := SignedIn{Kind: p.Kind}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		out.Cookie, txErr = h.signIn(ctx, p, *attempt.Claims, cmd)
		return txErr
	})
	var (
		refused *unresolved
		failure *SignInFailure
	)
	switch {
	case errors.As(err, &refused):
		return SignedIn{}, h.refuse(ctx, p, refused, cmd.RequestID)
	case errors.As(err, &failure):
		return SignedIn{}, err
	case err != nil:
		return SignedIn{}, fail(CodeSSOFailed, err)
	}
	out.ReturnTo = attempt.ReturnTo
	return out, nil
}

// refuse returns the failure for a person no rule lets in, recording the
// failed sign-in of a member the instance knows them as. The reason is the
// member's status when it is what refuses them, as a password sign-in
// records it, and not_linked for an active member.
func (h *CompleteSignIn) refuse(ctx context.Context, p identity.Provider, u *unresolved, requestID string) error {
	refusal := fail(CodeMemberNotFound, u.err)
	if errors.Is(u.err, identity.ErrMemberDeactivated) {
		refusal = fail(CodeMemberDeactivated, u.err)
	}
	if !u.known {
		return refusal
	}
	reason := string(u.member.Status)
	if u.member.Status == identity.MemberActive {
		reason = reasonNotLinked
	}
	if err := h.d.Tx.Within(context.WithoutCancel(ctx), func(ctx context.Context) error {
		return h.d.Members.RecordFailedSignIn(ctx, u.member, p.Kind, p.Slug, reason, requestID)
	}); err != nil {
		return errors.Join(refusal, err)
	}
	return refusal
}

// signIn resolves the person claims describe, and makes every change the
// sign-in asks for, in the unit of work ctx carries.
func (h *CompleteSignIn) signIn(ctx context.Context, p identity.Provider, claims identity.VerifiedClaims, cmd CompleteCommand) (secret.String, error) {
	in := identity.ResolveInput{Provider: p, Claims: claims}
	var (
		linked identity.Identity
		member MemberRecord
	)
	if claims.HasGroups {
		// Replacing the mappings holds the provider until it has run them for
		// every claim stored, so a sign-in waits for it and then applies the
		// new ones: otherwise it could store its claim just after the
		// replacement looked for claims, and apply the mappings that are gone.
		// The provider is held before anything is written: linking an
		// identity or storing the claim refers to the provider's row, and two
		// sign-ins that each referred to it first would then wait on each
		// other to hold it.
		if _, err := h.d.Providers.LockProvider(ctx, p.Slug); err != nil {
			return secret.String{}, err
		}
	}
	found, err := h.d.Identities.IdentityBySubject(ctx, p.ID, claims.Subject)
	hasIdentity := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return secret.String{}, err
	}
	if hasIdentity {
		linked = found
		m, ok, memberErr := h.d.Members.MemberByID(ctx, linked.Member)
		if memberErr != nil {
			return secret.String{}, memberErr
		}
		if ok {
			member, in.Linked = m, &m.KnownMember
		}
	}
	if in.Linked == nil && claims.Subject != "" {
		m, ok, memberErr := h.d.Members.MemberByExternalID(ctx, p.Slug, claims.Subject)
		if memberErr != nil {
			return secret.String{}, memberErr
		}
		if ok {
			member, in.Provisioned = m, &m.KnownMember
		}
	}
	if in.Linked == nil && in.Provisioned == nil && claims.Email != "" {
		m, ok, memberErr := h.d.Members.MemberByEmail(ctx, claims.Email)
		if memberErr != nil {
			return secret.String{}, memberErr
		}
		if ok {
			member, in.ByEmail = m, &m.KnownMember
		}
	}
	// An email of an enforced domain signs in through its provider only:
	// the email of the member the person resolves to, or the one they claim
	// when they resolve to none.
	email := claims.Email
	if in.Linked != nil || in.Provisioned != nil || in.ByEmail != nil {
		email = member.Email
	}
	if err = refuseEnforced(ctx, h.d.Members, p, email); err != nil {
		return secret.String{}, err
	}
	res, err := identity.Resolve(in)
	if err != nil {
		known := in.Linked != nil || in.Provisioned != nil || (in.ByEmail != nil && p.Vouches(claims))
		return secret.String{}, &unresolved{err: err, member: member, known: known}
	}
	now := h.d.now()
	switch res.Action {
	case identity.Provision:
		if member, err = h.d.Members.ProvisionMember(ctx, claims.Email, claims.MemberName(), p.Kind, p.Slug, cmd.RequestID); err != nil {
			return secret.String{}, err
		}
		fallthrough
	case identity.LinkByEmail, identity.LinkProvisioned:
		linked = identity.Identity{
			ID: id.New[identity.Identity](h.d.IDs), Provider: p.ID, Subject: claims.Subject, Member: member.ID, LinkedAt: now,
		}
		if err = h.d.Identities.CreateIdentity(ctx, linked); err != nil {
			return secret.String{}, err
		}
		if err = h.recordLinked(ctx, now, p, linked, member, cmd.RequestID); err != nil {
			return secret.String{}, err
		}
	}
	if err = h.d.Identities.RecordIdentitySignIn(ctx, linked.ID, now); err != nil {
		return secret.String{}, err
	}
	if err = h.applyGroups(ctx, now, p, member, claims, cmd.RequestID); err != nil {
		return secret.String{}, err
	}
	return h.d.Members.OpenSession(ctx, SessionRequest{
		Member: member, Kind: p.Kind, Provider: p.Slug, ClientIP: cmd.ClientIP, UserAgent: cmd.UserAgent, RequestID: cmd.RequestID,
	})
}

// applyGroups stores the groups the person claimed and gives member what the
// provider's mappings map them and the member's SCIM groups at the provider
// to, replacing what the provider granted before. A claim that is absent or not a list of strings changes nothing: a
// misconfigured identity provider cannot strip everyone of their access.
func (h *CompleteSignIn) applyGroups(ctx context.Context, now time.Time, p identity.Provider, member MemberRecord,
	claims identity.VerifiedClaims, requestID string,
) error {
	mappings, err := h.d.Mappings.GroupMappings(ctx, p.ID)
	if err != nil {
		return err
	}
	if !claims.HasGroups {
		if len(mappings) > 0 {
			h.d.Logger.WarnContext(ctx, "the groups claim is missing or is not a list of strings, so the member keeps the access the provider granted",
				"provider", p.Slug, "member", member.ID.String())
		}
		return nil
	}
	if err = h.d.Mappings.StoreGroupClaim(ctx, p.ID, member.ID, claims.Groups, now); err != nil {
		return err
	}
	if len(mappings) == 0 {
		// Nothing is mapped, so nothing was granted: editing the mappings
		// runs them for everyone who claimed groups.
		return nil
	}
	// The member's SCIM groups at the provider count beside the claim, so a
	// sign-in keeps what a SCIM group grants and a SCIM change what a claim
	// grants.
	groups, err := h.d.Mappings.GroupsOf(ctx, p.ID, member.ID)
	if err != nil {
		return err
	}
	deps := GroupMappingDeps{Access: h.d.Access, Logger: h.d.Logger}
	_, err = deps.replaceAccess(ctx, p, member.ID, mappings, groups, governance.Actor{}, requestID)
	return err
}

// recordLinked records identity.linked: the member, as the person who signed
// in, linked the subject.
func (h *CompleteSignIn) recordLinked(ctx context.Context, now time.Time, p identity.Provider, i identity.Identity, m MemberRecord, requestID string) error {
	req := governance.Request{
		Actor: governance.Actor{Kind: governance.ActorMember, ID: m.ID.String(), Name: m.DisplayName, MemberID: m.ID.String()},
		ID:    requestID,
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, req, governance.Record{
		Action: "identity.linked",
		Entity: governance.Entity{Kind: "identity", ID: i.ID.String(), Key: p.Slug},
		After: map[string]any{
			"provider": p.Slug, "subject": i.Subject, "member_id": m.ID.String(), "member_email": m.Email,
		},
	})
	if err != nil {
		return err
	}
	return h.d.Audit.Record(ctx, entry)
}
