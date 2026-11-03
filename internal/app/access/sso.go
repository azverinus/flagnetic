package access

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// SSOStore is what single sign-on needs of the stored members and sessions.
type SSOStore interface {
	// MemberByEmail returns the member whose email is email whatever its
	// case, with their password hash ("" for none); ErrNotFound when none.
	MemberByEmail(ctx context.Context, email string) (access.Member, string, error)
	// MemberByID returns the member; ErrNotFound when none has the identifier.
	MemberByID(ctx context.Context, member access.MemberID) (access.Member, error)
	CreateMember(ctx context.Context, m access.Member, passwordHash string) error
	RecordSignIn(ctx context.Context, member access.MemberID, at time.Time) error
	knownSourceRecorder
	CreateSession(ctx context.Context, s access.Session) error
}

// SSO is what the identity context asks of the access context to sign a
// person in through an auth provider: the members it knows, a member
// provisioned on first sign-in, and the session that opens. Each method acts
// in the unit of work its context carries, so the identity context makes all
// of a sign-in's changes in one transaction.
type SSO struct {
	store    SSOStore
	settings AuthSettingsReader
	audit    AuditLog
	clock    clock.Clock
	ids      *id.Source
	random   io.Reader
	key      []byte
}

// NewSSO returns the service, whose session cookies are drawn from random and
// signed with key.
func NewSSO(store SSOStore, settings AuthSettingsReader, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader, key []byte) *SSO {
	return &SSO{store: store, settings: settings, audit: audit, clock: c, ids: ids, random: random, key: key}
}

// MemberByEmail returns the member holding email, whatever its case; false
// when none does.
func (s *SSO) MemberByEmail(ctx context.Context, email string) (access.Member, bool, error) {
	m, _, err := s.store.MemberByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, false, nil
	}
	return m, err == nil, err
}

// MemberByID returns the member; false when none has the identifier.
func (s *SSO) MemberByID(ctx context.Context, member access.MemberID) (access.Member, bool, error) {
	m, err := s.store.MemberByID(ctx, member)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, false, nil
	}
	return m, err == nil, err
}

// ProvisionMember creates the active member a first sign-in through provider
// asks for, without a password, and records member.provisioned with the new
// member as its actor: they provisioned themself by signing in.
func (s *SSO) ProvisionMember(ctx context.Context, email, displayName, provider, requestID string) (access.Member, error) {
	now := s.clock.Now()
	m, err := access.NewProvisionedMember(id.New[access.Member](s.ids), email, displayName, now)
	if err != nil {
		return access.Member{}, err
	}
	if err = s.store.CreateMember(ctx, m, ""); err != nil {
		return access.Member{}, err
	}
	err = s.record(ctx, now, governance.Request{Actor: memberActor(m), ID: requestID}, governance.Record{
		Action: "member.provisioned",
		Entity: memberEntity(m),
		After:  map[string]any{"email": m.Email, "display_name": m.DisplayName, "method": string(access.SignInOIDC), "provider": provider},
	})
	return m, err
}

// SSOSignIn is one single sign-on opening a session.
type SSOSignIn struct {
	Member access.Member
	// Provider is the slug of the auth provider the member signed in through.
	Provider string
	// ClientIP and UserAgent describe the device signing in.
	ClientIP  netip.Addr
	UserAgent string
	RequestID string
}

// OpenSession opens the session of a member who proved who they are to an
// auth provider, records their sign-in and the source it came from, and
// returns the value of its cookie, which is handed to the browser and never
// stored. It records auth.signed_in with the method and the provider.
func (s *SSO) OpenSession(ctx context.Context, in SSOSignIn) (secret.String, error) {
	settings, err := s.settings.AuthSettings(ctx)
	if err != nil {
		return secret.String{}, err
	}
	now := s.clock.Now()
	session, cookie := access.NewSession(id.New[access.Session](s.ids), access.NewSessionInput{
		Member: in.Member.ID, Method: access.SignInOIDC, Provider: in.Provider, ClientIP: in.ClientIP, UserAgent: in.UserAgent,
		Timeouts: settings.SessionTimeouts(), Now: now,
	}, s.random, s.key)
	if err = s.store.RecordSignIn(ctx, session.Member, now); err != nil {
		return secret.String{}, err
	}
	if err = recordKnownSource(ctx, s.store, session.Member, session.ClientIP, now); err != nil {
		return secret.String{}, err
	}
	if err = s.store.CreateSession(ctx, session); err != nil {
		return secret.String{}, err
	}
	return cookie, s.record(ctx, now, governance.Request{Actor: memberActor(in.Member), ID: in.RequestID}, ssoRecord(
		"auth.signed_in", in.Member, in.Provider, nil))
}

// RecordFailedSignIn records auth.sign_in_failed for a member known to the
// instance who could not sign in through provider, with reason.
func (s *SSO) RecordFailedSignIn(ctx context.Context, member access.Member, provider, reason, requestID string) error {
	return s.record(ctx, s.clock.Now(), governance.Request{Actor: memberActor(member), ID: requestID},
		ssoRecord("auth.sign_in_failed", member, provider, map[string]any{"reason": reason}))
}

func (s *SSO) record(ctx context.Context, now time.Time, req governance.Request, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](s.ids), now, req, r)
	if err != nil {
		return err
	}
	return s.audit.Record(ctx, entry)
}

// ssoRecord is the audit record of action, a sign-in or a failed one, of m
// through provider.
func ssoRecord(action string, m access.Member, provider string, more map[string]any) governance.Record {
	r := authRecord(action, m, more)
	after := r.After.(map[string]any)
	after["method"], after["provider"] = access.SignInOIDC, provider
	return r
}
