package management

import (
	"context"
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The bounds of an audit log page.
const (
	defaultAuditLimit = 50
	maxAuditLimit     = 200
)

// ListProjectAuditEntries answers a page of the project's audit log.
func (a *api) ListProjectAuditEntries(ctx context.Context, req ListProjectAuditEntriesRequestObject) (ListProjectAuditEntriesResponseObject, error) {
	list, err := a.auditPage(ctx, projectID(ctx), req.Params)
	if err != nil {
		return nil, err
	}
	return ListProjectAuditEntries200JSONResponse(list), nil
}

// ListAuditEntries answers a page of the instance's audit log, narrowed to
// one project when the request names one.
func (a *api) ListAuditEntries(ctx context.Context, req ListAuditEntriesRequestObject) (ListAuditEntriesResponseObject, error) {
	// The project is named by its identifier, which its entries keep after
	// the project is gone and which a later project reusing its key never
	// has.
	var project workspace.ProjectID
	if req.Params.Project != nil {
		project = id.FromBytes[workspace.Project](*req.Params.Project)
	}
	q := req.Params
	list, err := a.auditPage(ctx, project, ListProjectAuditEntriesParams{
		Actor: q.Actor, ActorKind: q.ActorKind, Action: q.Action, EntityKind: q.EntityKind, EntityId: q.EntityId,
		Environment: q.Environment, From: q.From, To: q.To, Q: q.Q, KillSwitch: q.KillSwitch, ChangeRequest: q.ChangeRequest, Cursor: q.Cursor,
		Limit: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	return ListAuditEntries200JSONResponse(list), nil
}

// GetProjectAuditEntry answers one entry of the project's audit log.
func (a *api) GetProjectAuditEntry(ctx context.Context, req GetProjectAuditEntryRequestObject) (GetProjectAuditEntryResponseObject, error) {
	entryID, err := id.Parse[governance.Entry](req.Entry)
	if err != nil {
		// Not an identifier, so no entry of the log has it.
		return nil, appgovernance.ErrEntryNotFound
	}
	e, err := a.h.GetAuditEntry.Handle(ctx, projectID(ctx), entryID)
	if err != nil {
		return nil, err
	}
	return GetProjectAuditEntry200JSONResponse(auditEntryOut(e)), nil
}

// auditPage answers the page of the log of project (zero for the whole
// instance) that q asks for.
func (a *api) auditPage(ctx context.Context, project workspace.ProjectID, q ListProjectAuditEntriesParams) (AuditEntryList, error) {
	f := appgovernance.AuditFilter{
		Project: project, Action: valueOf(q.Action), EntityKind: valueOf(q.EntityKind), EntityID: valueOf(q.EntityId), Q: valueOf(q.Q),
		KillSwitch: q.KillSwitch,
	}
	if q.Actor != nil {
		f.Actor = id.FromBytes[access.Member](*q.Actor)
	}
	if q.ActorKind != nil {
		f.ActorKind = governance.ActorKind(*q.ActorKind)
	}
	if q.Environment != nil {
		f.Environment = id.FromBytes[workspace.Environment](*q.Environment)
	}
	if q.ChangeRequest != nil {
		f.ChangeRequest = id.FromBytes[governance.ChangeRequest](*q.ChangeRequest)
	}
	if q.From != nil {
		f.From = *q.From
	}
	if q.To != nil {
		f.To = *q.To
	}
	page := appgovernance.AuditPage{Limit: defaultAuditLimit}
	if q.Limit != nil {
		if *q.Limit < 1 || *q.Limit > maxAuditLimit {
			return AuditEntryList{}, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxAuditLimit))
		}
		page.Limit = *q.Limit
	}
	if q.Cursor != nil {
		after, err := id.Parse[governance.Entry](*q.Cursor)
		if err != nil {
			return AuditEntryList{}, flaghttp.InvalidRequest("cursor is not a next_cursor this API returned")
		}
		page.After = after
	}
	entries, next, err := a.h.ListAuditEntries.Handle(ctx, f, page)
	if err != nil {
		return AuditEntryList{}, err
	}
	out := AuditEntryList{Items: each(entries, auditEntryOut)}
	if !next.IsZero() {
		cursor := next.String()
		out.NextCursor = &cursor
	}
	return out, nil
}

// actorOut returns a as the audit log names it.
func actorOut(a governance.Actor) Actor {
	out := Actor{Kind: ActorKind(a.Kind), Id: a.ID, Name: a.Name}
	if m, err := id.Parse[access.Member](a.MemberID); err == nil {
		member := openapi_types.UUID(m.Bytes())
		out.MemberId = &member
	}
	return out
}

func auditEntryOut(e governance.Entry) AuditEntry {
	out := AuditEntry{
		Id: openapi_types.UUID(e.ID.Bytes()), OccurredAt: e.OccurredAt.UTC(), Action: e.Action,
		Actor:  actorOut(e.Actor),
		Entity: AuditEntity{Kind: e.Entity.Kind, Id: e.Entity.ID, Key: e.Entity.Key},
		Before: e.Before, After: e.After, KillSwitch: e.KillSwitch,
	}
	if !e.Project.IsZero() {
		p := openapi_types.UUID(e.Project.Bytes())
		out.ProjectId = &p
	}
	if !e.Environment.IsZero() {
		env := openapi_types.UUID(e.Environment.Bytes())
		out.EnvironmentId = &env
	}
	if e.RequestID != "" {
		r := e.RequestID
		out.RequestId = &r
	}
	if !e.ChangeRequest.IsZero() {
		cr := openapi_types.UUID(e.ChangeRequest.Bytes())
		out.ChangeRequestId = &cr
	}
	return out
}
