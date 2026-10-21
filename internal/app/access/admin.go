package access

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// MemberStore creates members.
type MemberStore interface {
	CreateMember(ctx context.Context, m access.Member, passwordHash string) error
}

// CreateInstanceAdmin adds an instance admin with a password from the
// command line, for a second admin or one who lost their way in.
type CreateInstanceAdmin struct {
	tx     Transactor
	store  MemberStore
	hasher PasswordHasher
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
}

// NewCreateInstanceAdmin returns the handler. random is the source
// generated passwords are drawn from.
func NewCreateInstanceAdmin(tx Transactor, store MemberStore, hasher PasswordHasher, audit AuditLog, c clock.Clock,
	ids *id.Source, random io.Reader,
) *CreateInstanceAdmin {
	return &CreateInstanceAdmin{tx: tx, store: store, hasher: hasher, audit: audit, clock: c, ids: ids, random: random}
}

// CreateInstanceAdminCommand names the instance admin to create.
type CreateInstanceAdminCommand struct {
	Email string
	// DisplayName is "" to name the member after their email's local part.
	DisplayName string
	// Password is checked against the password policy, or zero to have one
	// generated.
	Password secret.String
	Request  governance.Request
}

// CreateInstanceAdminResult is the instance admin created and the password
// generated for a command that gave none, shown once.
type CreateInstanceAdminResult struct {
	Member            access.Member
	GeneratedPassword secret.String
}

// Handle creates the active instance admin. A password the command gives is
// refused weak_password before it is hashed; an email the instance holds
// is refused already_exists.
func (h *CreateInstanceAdmin) Handle(ctx context.Context, cmd CreateInstanceAdminCommand) (CreateInstanceAdminResult, error) {
	now := h.clock.Now()
	member, err := access.NewInstanceAdmin(id.New[access.Member](h.ids), cmd.Email, cmd.DisplayName, now)
	if err != nil {
		return CreateInstanceAdminResult{}, err
	}
	password, res, err := h.passwordOf(cmd.Password, member)
	if err != nil {
		return CreateInstanceAdminResult{}, err
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, memberCreated(member))
	if err != nil {
		return CreateInstanceAdminResult{}, err
	}
	hash, err := h.hasher.Hash(ctx, password)
	if err != nil {
		return CreateInstanceAdminResult{}, err
	}
	if err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.CreateMember(ctx, member, hash); err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	}); err != nil {
		return CreateInstanceAdminResult{}, err
	}
	res.Member = member
	return res, nil
}

func (h *CreateInstanceAdmin) passwordOf(given secret.String, m access.Member) (secret.String, CreateInstanceAdminResult, error) {
	if given.IsZero() {
		p := access.GeneratePassword(m.Email, h.random)
		return p, CreateInstanceAdminResult{GeneratedPassword: p}, nil
	}
	if err := access.CheckPassword(given.Reveal(), m.Email, access.MinPasswordLength); err != nil {
		return secret.String{}, CreateInstanceAdminResult{}, err
	}
	return given, CreateInstanceAdminResult{}, nil
}

// PromoteStore finds a member to promote and makes them an instance admin.
type PromoteStore interface {
	// LockMemberByEmail returns the member whose email is email whatever
	// its case, locked until the unit of work ends; ErrNotFound when none.
	LockMemberByEmail(ctx context.Context, email string) (access.Member, error)
	SetInstanceAdmin(ctx context.Context, member access.MemberID, instanceAdmin bool, at time.Time) error
}

// ErrNoMemberWithEmail refuses a command naming an email no member has.
var ErrNoMemberWithEmail = apperrors.NotFound("not_found", "no member has this email")

// PromoteMember makes a member an instance admin from the command line.
type PromoteMember struct {
	tx    Transactor
	store PromoteStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewPromoteMember returns the handler.
func NewPromoteMember(tx Transactor, store PromoteStore, audit AuditLog, c clock.Clock, ids *id.Source) *PromoteMember {
	return &PromoteMember{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle makes the member with email an instance admin and reports whether
// they were not one already; promoting an instance admin changes and
// records nothing.
func (h *PromoteMember) Handle(ctx context.Context, email string, req governance.Request) (bool, error) {
	promoted := false
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		m, err := h.store.LockMemberByEmail(ctx, email)
		if errors.Is(err, ErrNotFound) {
			return ErrNoMemberWithEmail
		}
		if err != nil || m.InstanceAdmin {
			return err
		}
		now := h.clock.Now()
		entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, req, governance.Record{
			Action: "member.promoted",
			Entity: memberEntity(m),
			Before: map[string]any{"instance_admin": false},
			After:  map[string]any{"instance_admin": true},
		})
		if err != nil {
			return err
		}
		if err := h.store.SetInstanceAdmin(ctx, m.ID, true, now); err != nil {
			return err
		}
		promoted = true
		return h.audit.Record(ctx, entry)
	})
	return promoted, err
}
