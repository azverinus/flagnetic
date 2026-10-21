package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListMySessions answers the devices the caller is signed in on.
func (a *api) ListMySessions(ctx context.Context, _ ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	views, err := a.h.ListSessions.Handle(ctx, callerOf(ctx), access.MemberID{})
	if err != nil {
		return nil, err
	}
	return ListMySessions200JSONResponse(sessionList(views)), nil
}

// RevokeMySessions signs the caller out of every other device, or of this
// one too.
func (a *api) RevokeMySessions(ctx context.Context, req RevokeMySessionsRequestObject) (RevokeMySessionsResponseObject, error) {
	includeCurrent := req.Params.IncludeCurrent != nil && *req.Params.IncludeCurrent
	res, err := a.h.RevokeSessions.Handle(ctx, appaccess.RevokeSessionsCommand{
		Caller: callerOf(ctx), KeepCurrent: !includeCurrent, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RevokeMySessions200JSONResponse{
		Body:    SessionRevocation{Revoked: len(res.Revoked)},
		Headers: RevokeMySessions200ResponseHeaders{SetCookie: clearedIf(ctx, res.CurrentRevoked)},
	}, nil
}

// RevokeMySession signs the caller out of one device.
func (a *api) RevokeMySession(ctx context.Context, req RevokeMySessionRequestObject) (RevokeMySessionResponseObject, error) {
	session, err := id.Parse[access.Session](req.Session)
	if err != nil {
		// Not an identifier, so no session has it.
		return nil, appaccess.ErrSessionNotFound
	}
	res, err := a.h.RevokeSessions.Handle(ctx, appaccess.RevokeSessionsCommand{
		Caller: callerOf(ctx), Session: &session, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RevokeMySession204Response{Headers: RevokeMySession204ResponseHeaders{SetCookie: clearedIf(ctx, res.CurrentRevoked)}}, nil
}

// ListMemberSessions answers the devices a member is signed in on.
func (a *api) ListMemberSessions(ctx context.Context, req ListMemberSessionsRequestObject) (ListMemberSessionsResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	views, err := a.h.ListSessions.Handle(ctx, callerOf(ctx), member)
	if err != nil {
		return nil, err
	}
	return ListMemberSessions200JSONResponse(sessionList(views)), nil
}

// RevokeMemberSessions signs a member out of every device.
func (a *api) RevokeMemberSessions(ctx context.Context, req RevokeMemberSessionsRequestObject) (RevokeMemberSessionsResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	res, err := a.h.RevokeSessions.Handle(ctx, appaccess.RevokeSessionsCommand{
		Caller: callerOf(ctx), Member: member, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RevokeMemberSessions200JSONResponse{
		Body:    SessionRevocation{Revoked: len(res.Revoked)},
		Headers: RevokeMemberSessions200ResponseHeaders{SetCookie: clearedIf(ctx, res.CurrentRevoked)},
	}, nil
}

// memberIDOf reads a member's identifier from a path; one that is not an
// identifier names no member, and neither does the zero one, which the use
// cases read as the caller's own.
func memberIDOf(s string) (access.MemberID, error) {
	member, err := id.Parse[access.Member](s)
	if err != nil || member.IsZero() {
		return access.MemberID{}, appaccess.ErrMemberNotFound
	}
	return member, nil
}

// clearedIf returns the Set-Cookie clearing the session cookie when the
// session the request is signed in to has just ended, so the browser stops
// sending it; nil otherwise.
func clearedIf(ctx context.Context, currentEnded bool) *string {
	if !currentEnded {
		return nil
	}
	cleared := sessionCookie(ctx, "")
	return &cleared
}

func sessionList(views []appaccess.SessionView) SessionList {
	items := make([]Session, len(views))
	for i, v := range views {
		items[i] = Session{
			Id: openapi_types.UUID(v.ID.Bytes()), Method: SessionMethod(v.Method), UserAgent: v.UserAgent,
			CreatedAt: v.CreatedAt, LastSeenAt: v.LastSeenAt, ExpiresAt: v.End, Current: v.Current,
		}
		if v.ClientIP.IsValid() {
			ip := v.ClientIP.String()
			items[i].ClientIp = &ip
		}
	}
	return SessionList{Items: items}
}
