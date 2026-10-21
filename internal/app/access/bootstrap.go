// Package access holds the use cases of the access context: members, the
// instance admin, API tokens and the authentication of their callers.
package access

import (
	"context"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// PasswordHasher turns a password into the string stored for it.
type PasswordHasher interface {
	Hash(ctx context.Context, password secret.String) (string, error)
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// InstanceAdmins tells whether the instance has an instance admin.
type InstanceAdmins interface {
	// AnyInstanceAdmin reports whether an instance admin exists, whatever
	// their status.
	AnyInstanceAdmin(ctx context.Context) (bool, error)
}

// BootstrapStore is what bootstrapping needs of the stored members and
// tokens.
type BootstrapStore interface {
	InstanceAdmins
	// LockBootstrap serializes bootstraps until the unit of work ends, so two
	// running at once cannot both find no instance admin.
	LockBootstrap(ctx context.Context) error
	CreateMember(ctx context.Context, m access.Member, passwordHash string) error
	CreateToken(ctx context.Context, t access.APIToken) error
}

// ErrInstanceAdminExists refuses a bootstrap once the instance has an
// instance admin: from then on, they add the others.
var ErrInstanceAdminExists = apperrors.Conflict("instance_admin_exists", "an instance admin already exists")

// bootstrapTokenName names the token bootstrap creates.
const bootstrapTokenName = "bootstrap"

// Bootstrap creates the first instance admin of a fresh instance, with a
// password and a personal access token, so a platform engineer gets in
// without the console.
type Bootstrap struct {
	tx     Transactor
	store  BootstrapStore
	hasher PasswordHasher
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
}

// NewBootstrap returns the handler. random is the source token values and
// generated passwords are drawn from.
func NewBootstrap(tx Transactor, store BootstrapStore, hasher PasswordHasher, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader) *Bootstrap {
	return &Bootstrap{tx: tx, store: store, hasher: hasher, audit: audit, clock: c, ids: ids, random: random}
}

// BootstrapCommand names the first instance admin.
type BootstrapCommand struct {
	Email string
	// DisplayName is "" to name the member after their email's local part.
	DisplayName string
	// Password is the instance admin's password, or zero to have one
	// generated: unattended provisioning need not invent one.
	Password secret.String
	// Request names who bootstraps, for the audit entries.
	Request governance.Request
}

// BootstrapResult is the instance admin created and their token's value,
// which is shown once and never stored.
type BootstrapResult struct {
	Member access.Member
	Token  secret.String
	// GeneratedPassword is the password generated for a command that gave
	// none, shown once; zero when the command gave one.
	GeneratedPassword secret.String
}

// Handle creates the instance admin and their token, carrying every scope
// and no expiry: whoever runs bootstrap holds the database's credentials
// already, so the token gives them nothing they lack.
func (h *Bootstrap) Handle(ctx context.Context, cmd BootstrapCommand) (BootstrapResult, error) {
	now := h.clock.Now()
	member, err := access.NewInstanceAdmin(id.New[access.Member](h.ids), cmd.Email, cmd.DisplayName, now)
	if err != nil {
		return BootstrapResult{}, err
	}
	token, value, err := access.NewPersonalToken(id.New[access.APIToken](h.ids), access.NewPersonalTokenInput{
		Member: member.ID, Name: bootstrapTokenName, Scopes: access.AllScopes(), Now: now,
	}, h.random)
	if err != nil {
		return BootstrapResult{}, err
	}
	memberEntry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, memberCreated(member))
	if err != nil {
		return BootstrapResult{}, err
	}
	tokenEntry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, tokenCreated(token, member))
	if err != nil {
		return BootstrapResult{}, err
	}
	res := BootstrapResult{Member: member, Token: value.Value}
	password := cmd.Password
	if password.IsZero() {
		password = access.GeneratePassword(h.random)
		res.GeneratedPassword = password
	}

	admin := newAdmin{member: member, password: password, token: token, memberEntry: memberEntry, tokenEntry: tokenEntry}
	if err := h.tx.Within(ctx, func(ctx context.Context) error { return h.create(ctx, admin) }); err != nil {
		return BootstrapResult{}, err
	}
	return res, nil
}

// newAdmin is what a bootstrap stores, prepared before its unit of work.
type newAdmin struct {
	member                  access.Member
	password                secret.String
	token                   access.APIToken
	memberEntry, tokenEntry governance.Entry
}

// create stores the instance admin and their token unless the instance has
// an instance admin already, in the unit of work ctx carries.
func (h *Bootstrap) create(ctx context.Context, a newAdmin) error {
	if err := h.store.LockBootstrap(ctx); err != nil {
		return err
	}
	exists, err := h.store.AnyInstanceAdmin(ctx)
	if err != nil {
		return err
	}
	if exists {
		return ErrInstanceAdminExists
	}
	// Hashed only once the instance is known to need its admin, so a
	// bootstrap that is refused spends no hashing on a password.
	hash, err := h.hasher.Hash(ctx, a.password)
	if err != nil {
		return err
	}
	if err := h.store.CreateMember(ctx, a.member, hash); err != nil {
		return err
	}
	if err := h.audit.Record(ctx, a.memberEntry); err != nil {
		return err
	}
	if err := h.store.CreateToken(ctx, a.token); err != nil {
		return err
	}
	return h.audit.Record(ctx, a.tokenEntry)
}

// InstanceAdminExists answers whether the instance has an instance admin, so
// provisioning that bootstraps on every deploy learns there is nothing to do
// before it prepares anything.
type InstanceAdminExists struct {
	store InstanceAdmins
}

// NewInstanceAdminExists returns the handler.
func NewInstanceAdminExists(store InstanceAdmins) *InstanceAdminExists {
	return &InstanceAdminExists{store: store}
}

// Handle reports whether an instance admin exists.
func (h *InstanceAdminExists) Handle(ctx context.Context) (bool, error) {
	return h.store.AnyInstanceAdmin(ctx)
}

func memberCreated(m access.Member) governance.Record {
	return governance.Record{
		Action: "member.created",
		Entity: governance.Entity{Kind: "member", ID: m.ID.String(), Key: m.Email},
		After: map[string]any{
			"email": m.Email, "display_name": m.DisplayName, "status": m.Status, "instance_admin": m.InstanceAdmin,
		},
	}
}

// tokenCreated records a token by its hint, never its value.
func tokenCreated(t access.APIToken, holder access.Member) governance.Record {
	var expiresAt *string
	if t.ExpiresAt != nil {
		s := t.ExpiresAt.UTC().Format(time.RFC3339)
		expiresAt = &s
	}
	return governance.Record{
		Action: "api_token.created",
		Entity: governance.Entity{Kind: "api_token", ID: t.ID.String(), Key: t.Name},
		After: map[string]any{
			"kind": t.Kind, "name": t.Name, "hint": t.Hint, "member": holder.ID.String(),
			"scopes": t.Scopes, "expires_at": expiresAt,
		},
	}
}
