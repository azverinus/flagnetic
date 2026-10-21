package access

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ProjectTokenStore keeps projects' tokens.
type ProjectTokenStore interface {
	// ProjectTokens returns every project token of project, revoked and
	// expired ones included, the newest first.
	ProjectTokens(ctx context.Context, project workspace.ProjectID) ([]access.APIToken, error)
	CreateToken(ctx context.Context, t access.APIToken) error
	// RevokeProjectToken revokes the project token of project with the
	// identifier at at and returns it as revoked; ErrNotFound unless the
	// project holds it unrevoked.
	RevokeProjectToken(ctx context.Context, project workspace.ProjectID, token access.APITokenID, at time.Time) (access.APIToken, error)
}

// ListProjectTokens answers the tokens of a project.
type ListProjectTokens struct{ store ProjectTokenStore }

// NewListProjectTokens returns the query.
func NewListProjectTokens(store ProjectTokenStore) *ListProjectTokens {
	return &ListProjectTokens{store: store}
}

// Handle returns every token of project, the newest first.
func (h *ListProjectTokens) Handle(ctx context.Context, project workspace.ProjectID) ([]access.APIToken, error) {
	return h.store.ProjectTokens(ctx, project)
}

// CreateProjectTokenCommand asks for a token of Project holding Role.
type CreateProjectTokenCommand struct {
	Caller    access.Caller
	Project   ProjectRef
	Name      string
	Role      access.RoleName
	Scopes    []access.Scope
	ExpiresAt *time.Time
	Request   governance.Request
}

// CreateProjectToken creates a token belonging to a project, for a pipeline.
type CreateProjectToken struct {
	tx       Transactor
	store    ProjectTokenStore
	settings AuthSettingsReader
	audit    AuditLog
	clock    clock.Clock
	ids      *id.Source
	random   io.Reader
}

// NewCreateProjectToken returns the handler; random draws token values.
func NewCreateProjectToken(tx Transactor, store ProjectTokenStore, settings AuthSettingsReader, audit AuditLog, c clock.Clock,
	ids *id.Source, random io.Reader,
) *CreateProjectToken {
	return &CreateProjectToken{tx: tx, store: store, settings: settings, audit: audit, clock: c, ids: ids, random: random}
}

// Handle creates the token and records api_token.created in its project,
// with its hint. Only a session creates one, so no token can mint another;
// a session holds every scope a project token may carry, which never
// includes instance:admin, so only its role is capped, by what the caller
// may hand out in the project; its expiry is within what the auth settings
// allow. It acts for no member and survives its creator.
func (h *CreateProjectToken) Handle(ctx context.Context, cmd CreateProjectTokenCommand) (CreatedToken, error) {
	if cmd.Caller.Session == nil || cmd.Caller.Member == nil {
		return CreatedToken{}, access.ErrSessionRequired
	}
	now := h.clock.Now()
	token, value, err := access.NewProjectToken(id.New[access.APIToken](h.ids), access.NewProjectTokenInput{
		Project: cmd.Project.ID, Role: cmd.Role, Name: cmd.Name, Scopes: cmd.Scopes, ExpiresAt: cmd.ExpiresAt,
		CreatedBy: cmd.Caller.Member.ID, Now: now,
	}, h.random)
	if err != nil {
		return CreatedToken{}, err
	}
	role, _ := access.RoleNamed(token.Role)
	if err = access.CheckGrantorCap(cmd.Project.Held, role); err != nil {
		return CreatedToken{}, err
	}
	settings, err := h.settings.AuthSettings(ctx)
	if err != nil {
		return CreatedToken{}, err
	}
	if err = access.CheckTokenExpiry(token.ExpiresAt, now, settings); err != nil {
		return CreatedToken{}, err
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, governance.Record{
		Project: cmd.Project.ID, Action: "api_token.created", Entity: tokenEntity(token), After: projectTokenDetail(token),
	})
	if err != nil {
		return CreatedToken{}, err
	}
	if err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.CreateToken(ctx, token); err != nil {
			return err
		}
		return h.audit.Record(ctx, entry)
	}); err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{Token: token, Value: value.Value}, nil
}

// RevokeProjectTokenCommand names the token of Project to revoke.
type RevokeProjectTokenCommand struct {
	Project ProjectRef
	Token   access.APITokenID
	Request governance.Request
}

// RevokeProjectToken revokes a project's token.
type RevokeProjectToken struct {
	tx    Transactor
	store ProjectTokenStore
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewRevokeProjectToken returns the handler.
func NewRevokeProjectToken(tx Transactor, store ProjectTokenStore, audit AuditLog, c clock.Clock, ids *id.Source,
) *RevokeProjectToken {
	return &RevokeProjectToken{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// Handle revokes the token and records api_token.revoked in its project; a
// token the project does not hold unrevoked is ErrTokenNotFound.
func (h *RevokeProjectToken) Handle(ctx context.Context, cmd RevokeProjectTokenCommand) error {
	return h.tx.Within(ctx, func(ctx context.Context) error {
		now := h.clock.Now()
		token, err := h.store.RevokeProjectToken(ctx, cmd.Project.ID, cmd.Token, now)
		if errors.Is(err, ErrNotFound) {
			return ErrTokenNotFound
		}
		if err != nil {
			return err
		}
		return recordAll(ctx, h.audit, h.ids, now, cmd.Request, tokenRevoked(cmd.Project.ID, token, projectTokenDetail(token)))
	})
}

// projectTokenDetail is what the audit log keeps of a project token: its
// hint, never its value.
func projectTokenDetail(t access.APIToken) map[string]any {
	return map[string]any{
		"kind": t.Kind, "name": t.Name, "hint": t.Hint, "role": t.Role, "scopes": t.Scopes, "expires_at": expiryOf(t),
		"created_by": t.CreatedBy.String(),
	}
}
