package compose

import (
	"context"
	"errors"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

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
	m, err := a.sso.ProvisionMember(ctx, email, displayName, access.SignInMethod(kind), provider, requestID)
	return recordOf(m), err
}

func (a ssoMembers) OpenSession(ctx context.Context, s appidentity.SessionRequest) (secret.String, error) {
	return a.sso.OpenSession(ctx, appaccess.SSOSignIn{
		Member: memberOf(s.Member), Method: access.SignInMethod(s.Kind), Provider: s.Provider, ClientIP: s.ClientIP, UserAgent: s.UserAgent, RequestID: s.RequestID,
	})
}

func (a ssoMembers) RecordFailedSignIn(ctx context.Context, member appidentity.MemberRecord, kind identity.Kind, provider, reason, requestID string) error {
	return a.sso.RecordFailedSignIn(ctx, memberOf(member), access.SignInMethod(kind), provider, reason, requestID)
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
		out = append(out, access.IdentityMethod{ID: i.ID.String(), ProviderEnabled: i.ProviderEnabled})
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
