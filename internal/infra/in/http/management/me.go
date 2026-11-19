package management

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// GetMe answers who the caller is: the member, the credential in use, the
// address the request came from and the instance's bound on a token's
// expiry. A project token acts for no member: it is answered with the
// project it belongs to and the role it holds.
func (a *api) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	c := callerOf(ctx)
	me, err := a.meOf(ctx, c)
	if err != nil {
		return nil, err
	}
	if t := c.Token; t != nil && t.Kind == access.TokenProject {
		projects, err := a.h.ListProjects.Handle(ctx, appworkspace.ListProjectsQuery{Only: []workspace.ProjectID{t.Project}})
		if err != nil {
			return nil, err
		}
		role := RoleName(t.Role)
		me.Credential.Role = &role
		if len(projects) == 1 {
			me.Credential.Project = &projects[0].Key
		}
	}
	return GetMe200JSONResponse(me), nil
}

// meOf describes c, calling from the request ctx belongs to. The token
// policy is read by every caller, not only by an instance admin, who alone
// reads the auth settings: whoever creates a token is told which expiries
// the instance accepts before choosing one.
func (a *api) meOf(ctx context.Context, c access.Caller) (Me, error) {
	settings, err := a.h.GetAuthSettings.Handle(ctx)
	if err != nil {
		return Me{}, err
	}
	return meWith(ctx, c, &settings), nil
}

// meAfterWrite is meOf for the answer to a request that has just committed
// (signing in, accepting an invitation, completing a password reset, editing
// one's own profile). The token policy is not what the write is for: were the
// settings not read (the pool is exhausted, the query timed out), the answer
// leaves the policy out and says so in the log, since failing here would
// answer 500 for a write that is done, with a secret consumed and a session
// open that no cookie was ever set for. Whoever creates a token reads GET /me
// first, which fails honestly.
func (a *api) meAfterWrite(ctx context.Context, c access.Caller) Me {
	settings, err := a.h.GetAuthSettings.Handle(ctx)
	if err != nil {
		a.edge.Logger().ErrorContext(ctx, "reading the auth settings for the answer to a committed write", "error", err.Error())
		return meWith(ctx, c, nil)
	}
	return meWith(ctx, c, &settings)
}

// meWith describes c, with the instance's token policy when settings are known.
func meWith(ctx context.Context, c access.Caller, settings *access.AuthSettings) Me {
	me := Me{
		ClientIp:   flaghttp.ClientOf(ctx).IP.String(),
		Credential: Credential{Method: CredentialMethod(c.Method)},
	}
	if settings != nil {
		// The instance always bounds a token's lifetime today; the field is
		// nullable so that an instance without a bound can say so.
		lifetime := int64(settings.TokenMaxLifetime / time.Second)
		me.MaxTokenLifetimeSeconds = &lifetime
		me.RequireTokenExpiry = settings.TokenExpiryRequired
	}
	if c.Token != nil {
		tokenID, name := openapi_types.UUID(c.Token.ID.Bytes()), c.Token.Name
		me.Credential.TokenId, me.Credential.TokenName = &tokenID, &name
	}
	if m := c.Member; m != nil {
		me.Member = &MeMember{
			Id: openapi_types.UUID(m.ID.Bytes()), Email: m.Email, DisplayName: m.DisplayName,
			Status: MemberStatus(m.Status), InstanceAdmin: m.InstanceAdmin,
		}
		if m.Locale != "" {
			locale := m.Locale
			me.Member.Locale = &locale
		}
	}
	return me
}

// UpdateMe renames the caller or changes their console language, and
// answers who they are as changed.
func (a *api) UpdateMe(ctx context.Context, req UpdateMeRequestObject) (UpdateMeResponseObject, error) {
	change := access.ProfileChange{DisplayName: req.Body.DisplayName}
	if req.Body.Locale.IsSpecified() {
		// null follows the browser, which the member records as no locale.
		locale, _ := req.Body.Locale.Get()
		change.Locale = &locale
	}
	c := callerOf(ctx)
	v, err := a.h.UpdateMe.Handle(ctx, appaccess.UpdateMeCommand{Caller: c, Change: change, Request: requestOf(ctx)})
	if err != nil {
		return nil, err
	}
	c.Member = &v.Member
	return UpdateMe200JSONResponse(a.meAfterWrite(ctx, c)), nil
}

// ChangeMyPassword sets the caller's new password once the current one is
// proved, signing their other devices out.
func (a *api) ChangeMyPassword(ctx context.Context, req ChangeMyPasswordRequestObject) (ChangeMyPasswordResponseObject, error) {
	err := a.h.ChangeMyPassword.Handle(ctx, appaccess.ChangeMyPasswordCommand{
		Caller:          callerOf(ctx),
		CurrentPassword: secret.New(req.Body.CurrentPassword),
		NewPassword:     secret.New(req.Body.NewPassword),
		ClientIP:        flaghttp.ClientOf(ctx).IP,
		Request:         requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return ChangeMyPassword204Response{}, nil
}
