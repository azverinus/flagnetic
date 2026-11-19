package compose

import (
	"context"
	"errors"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// signInMethodOf is the method of a session opened through a provider of
// kind. Each kind is named here, so one renamed in the identity context
// stops this build's tests instead of writing a method the access context
// does not know.
func signInMethodOf(kind identity.Kind) access.SignInMethod {
	switch kind {
	case identity.KindOIDC:
		return access.SignInOIDC
	case identity.KindSAML:
		return access.SignInSAML
	}
	panic("compose: no sign-in method for the auth provider kind " + string(kind)) // kinds are checked when a provider is stored
}

// ssoMembers lets the identity context reach the members of the access
// context: the two share no types, so this is where one's are turned into
// the other's.
type ssoMembers struct {
	sso  *appaccess.SSO
	ways *appaccess.WaysIn
}

var _ appidentity.Members = ssoMembers{}

func recordOf(m access.Member) appidentity.MemberRecord {
	return appidentity.MemberRecord{
		KnownMember: identity.KnownMember{ID: id.FromBytes[identity.Member](m.ID.Bytes()), Status: identity.MemberStatus(m.Status)},
		Email:       m.Email, DisplayName: m.DisplayName, Provider: m.Provider,
	}
}

// memberOf is the access member a record stands for, as far as signing in
// and its audit entries read them.
func memberOf(r appidentity.MemberRecord) access.Member {
	return access.Member{
		ID: id.FromBytes[access.Member](r.ID.Bytes()), Email: r.Email, DisplayName: r.DisplayName, Status: access.MemberStatus(r.Status),
	}
}

func (a ssoMembers) MemberByEmail(ctx context.Context, email string) (appidentity.MemberRecord, bool, error) {
	m, ok, err := a.sso.MemberByEmail(ctx, email)
	return recordOf(m), ok, err
}

func (a ssoMembers) MemberByID(ctx context.Context, member identity.MemberID) (appidentity.MemberRecord, bool, error) {
	m, ok, err := a.sso.MemberByID(ctx, id.FromBytes[access.Member](member.Bytes()))
	return recordOf(m), ok, err
}

func (a ssoMembers) MemberByExternalID(ctx context.Context, provider, externalID string) (appidentity.MemberRecord, bool, error) {
	m, ok, err := a.sso.MemberByExternalID(ctx, provider, externalID)
	return recordOf(m), ok, err
}

func (a ssoMembers) ProvisionMember(ctx context.Context, email, displayName string, kind identity.Kind, provider, requestID string) (appidentity.MemberRecord, error) {
	m, err := a.sso.ProvisionMember(ctx, email, displayName, signInMethodOf(kind), provider, requestID)
	return recordOf(m), err
}

func (a ssoMembers) ActivateInvited(ctx context.Context, member identity.MemberID, kind identity.Kind, provider, requestID string) (appidentity.MemberRecord, error) {
	m, err := a.sso.ActivateInvited(ctx, appaccess.SSOActivation{
		Member: id.FromBytes[access.Member](member.Bytes()), Method: signInMethodOf(kind), Provider: provider, RequestID: requestID,
	})
	if errors.Is(err, appaccess.ErrNotInvited) {
		return appidentity.MemberRecord{}, identity.ErrMemberNotFound
	}
	return recordOf(m), err
}

func (a ssoMembers) HoldAuthSettings(ctx context.Context) error {
	return a.sso.HoldAuthSettings(ctx)
}

func (a ssoMembers) OpenSession(ctx context.Context, s appidentity.SessionRequest) (secret.String, error) {
	cookie, err := a.sso.OpenSession(ctx, appaccess.SSOSignIn{
		Member: memberOf(s.Member), Method: signInMethodOf(s.Kind), Provider: s.Provider, ClientIP: s.ClientIP, UserAgent: s.UserAgent, RequestID: s.RequestID,
	})
	// A domain enforced to another provider while the sign-in was under way
	// is refused as one enforced when it began.
	if e, ok := apperrors.From(err); ok && e.Code == "sso_required" {
		if provider, _ := e.Extensions["provider"].(string); provider != "" {
			return secret.String{}, appidentity.RequireSSO(provider)
		}
	}
	return cookie, err
}

func (a ssoMembers) LockInstanceAdmins(ctx context.Context) error {
	return a.sso.LockInstanceAdmins(ctx)
}

func (a ssoMembers) RevokeProviderSessions(ctx context.Context, member identity.MemberID, provider string) (int, error) {
	return a.sso.RevokeProviderSessions(ctx, id.FromBytes[access.Member](member.Bytes()), provider)
}

func (a ssoMembers) RecordFailedSignIn(ctx context.Context, member appidentity.MemberRecord, kind identity.Kind, provider, reason, requestID string) error {
	return a.sso.RecordFailedSignIn(ctx, memberOf(member), signInMethodOf(kind), provider, reason, requestID)
}

func (a ssoMembers) LockMember(ctx context.Context, member identity.MemberID) (appidentity.MemberRecord, bool, error) {
	m, err := a.ways.Lock(ctx, id.FromBytes[access.Member](member.Bytes()))
	if errors.Is(err, appaccess.ErrNotFound) {
		return appidentity.MemberRecord{}, false, nil
	}
	return recordOf(m), err == nil, err
}

func (a ssoMembers) PasswordSetAt(ctx context.Context, member identity.MemberID) (*time.Time, error) {
	return a.ways.PasswordSetAt(ctx, id.FromBytes[access.Member](member.Bytes()))
}

func (a ssoMembers) EnforcedProvider(ctx context.Context, email string) (string, error) {
	return a.sso.EnforcedProvider(ctx, email)
}

func (a ssoMembers) EnsureWayIn(ctx context.Context, member appidentity.MemberRecord, without identity.ID) error {
	return a.ways.EnsureWayIn(ctx, memberOf(member), false, without.String())
}

// identityDirectory lets the access context read the identities members
// hold, which the identity context keeps.
type identityDirectory struct{ store *pgidentity.Store }

var _ appaccess.IdentityDirectory = identityDirectory{}

func (d identityDirectory) MemberIdentities(ctx context.Context, member access.MemberID) ([]access.IdentityMethod, error) {
	held, err := d.store.MemberIdentities(ctx, id.FromBytes[identity.Member](member.Bytes()))
	if err != nil {
		return nil, err
	}
	out := make([]access.IdentityMethod, 0, len(held))
	for _, i := range held {
		out = append(out, access.IdentityMethod{ID: i.ID.String(), Provider: i.ProviderSlug, ProviderEnabled: i.ProviderEnabled})
	}
	return out, nil
}

// sessionResolver tells the identity context who a session cookie signs in.
type sessionResolver struct {
	authenticate *appaccess.AuthenticateSession
}

var _ appidentity.SessionResolver = sessionResolver{}

func (r sessionResolver) SessionOf(ctx context.Context, cookie string) (identity.MemberID, identity.SessionID, bool, error) {
	if cookie == "" {
		return identity.MemberID{}, identity.SessionID{}, false, nil
	}
	c, err := r.authenticate.Handle(ctx, cookie)
	if errors.Is(err, appaccess.ErrNoSession) || errors.Is(err, access.ErrSessionExpired) {
		return identity.MemberID{}, identity.SessionID{}, false, nil
	}
	if err != nil || c.Member == nil || c.Session == nil {
		return identity.MemberID{}, identity.SessionID{}, false, err
	}
	return id.FromBytes[identity.Member](c.Member.ID.Bytes()), id.FromBytes[identity.Session](c.Session.ID.Bytes()), true, nil
}

// enforcementProviders lets the access context check the auth providers the
// enforced domains name, which the identity context keeps.
type enforcementProviders struct{ store *pgidentity.Store }

var _ appaccess.EnforcementProviders = enforcementProviders{}

func (e enforcementProviders) EnforcementProvider(ctx context.Context, slug string) (appaccess.EnforcementProvider, bool, error) {
	p, err := e.store.Provider(ctx, slug)
	if errors.Is(err, appidentity.ErrNotFound) {
		return appaccess.EnforcementProvider{}, false, nil
	}
	if err != nil {
		return appaccess.EnforcementProvider{}, false, err
	}
	return appaccess.EnforcementProvider{Enabled: p.Enabled, Domains: p.Domains}, true, nil
}

// providerSettings lets the identity context read what the auth settings,
// which the access context keeps, ask of a provider.
type providerSettings struct{ store *pgaccess.Store }

var _ appidentity.ProviderSettings = providerSettings{}

func (p providerSettings) LockProviderSettings(ctx context.Context, slug string) (appidentity.SettingsOfProvider, error) {
	s, err := p.store.LockAuthSettings(ctx)
	if err != nil {
		return appidentity.SettingsOfProvider{}, err
	}
	return appidentity.SettingsOfProvider{LocalLoginEnabled: s.LocalLoginEnabled, EnforcedDomains: s.EnforcedDomainsOf(slug)}, nil
}

func (p providerSettings) RevokeProviderSessions(ctx context.Context, slug string, at time.Time) (int, error) {
	return p.store.RevokeAllProviderSessions(ctx, slug, at)
}
