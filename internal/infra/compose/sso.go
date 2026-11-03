package compose

import (
	"context"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ssoMembers lets the identity context reach the members of the access
// context: the two share no types, so this is where one's are turned into
// the other's.
type ssoMembers struct{ sso *appaccess.SSO }

var _ appidentity.Members = ssoMembers{}

func recordOf(m access.Member) appidentity.MemberRecord {
	return appidentity.MemberRecord{
		KnownMember: identity.KnownMember{ID: id.FromBytes[identity.Member](m.ID.Bytes()), Status: identity.MemberStatus(m.Status)},
		Email:       m.Email, DisplayName: m.DisplayName,
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

func (a ssoMembers) ProvisionMember(ctx context.Context, email, displayName, provider, requestID string) (appidentity.MemberRecord, error) {
	m, err := a.sso.ProvisionMember(ctx, email, displayName, provider, requestID)
	return recordOf(m), err
}

func (a ssoMembers) OpenSession(ctx context.Context, s appidentity.SessionRequest) (secret.String, error) {
	return a.sso.OpenSession(ctx, appaccess.SSOSignIn{
		Member: memberOf(s.Member), Provider: s.Provider, ClientIP: s.ClientIP, UserAgent: s.UserAgent, RequestID: s.RequestID,
	})
}

func (a ssoMembers) RecordFailedSignIn(ctx context.Context, member appidentity.MemberRecord, provider, reason, requestID string) error {
	return a.sso.RecordFailedSignIn(ctx, memberOf(member), provider, reason, requestID)
}
