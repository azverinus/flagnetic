package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The bounds of a page of change requests.
const (
	defaultChangeRequestLimit = 50
	maxChangeRequestLimit     = 200
)

// ListChangeRequests answers a page of the project's change requests, newest
// first.
func (a *api) ListChangeRequests(ctx context.Context, req ListChangeRequestsRequestObject) (ListChangeRequestsResponseObject, error) {
	p := req.Params
	f := appgovernance.ChangeRequestFilter{Project: projectID(ctx), Flag: valueOf(p.Flag), Environment: valueOf(p.Environment)}
	// An unknown kind or state would match nothing, which reads as no
	// change request of it rather than as a filter misspelt.
	if p.Kind != nil {
		if !p.Kind.Valid() {
			return nil, flaghttp.InvalidRequest("kind must be one of flag_configuration, definition and experiment")
		}
		f.Kind = governance.ChangeRequestKind(*p.Kind)
	}
	if p.State != nil {
		if !p.State.Valid() {
			return nil, flaghttp.InvalidRequest("state must be one of draft, open, approved, rejected, applied, cancelled and superseded")
		}
		f.State = governance.ChangeRequestState(*p.State)
	}
	if p.Author != nil {
		f.Author = p.Author.String()
	}
	page := appgovernance.ChangeRequestPage{Limit: defaultChangeRequestLimit}
	if l := p.Limit; l != nil {
		if *l < 1 || *l > maxChangeRequestLimit {
			return nil, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxChangeRequestLimit))
		}
		page.Limit = *l
	}
	if c := p.Cursor; c != nil {
		after, err := decodeChangeRequestCursor(*c)
		if err != nil {
			return nil, err
		}
		page.After = after
	}
	list, next, err := a.h.ListChangeRequests.Handle(ctx, f, page)
	if err != nil {
		return nil, err
	}
	items := make([]ChangeRequest, len(list))
	for i, cr := range list {
		if items[i], err = changeRequestOut(cr); err != nil {
			return nil, err
		}
	}
	out := ListChangeRequests200JSONResponse{Items: items}
	if !next.IsZero() {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(next.String()))
		out.NextCursor = &cursor
	}
	return out, nil
}

// decodeChangeRequestCursor reads cursor, refusing one this API did not
// return. It is opaque, so the order a page follows can change without
// breaking a client that only passes it back.
func decodeChangeRequestCursor(cursor string) (governance.ChangeRequestID, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err == nil {
		var after governance.ChangeRequestID
		if after, err = id.Parse[governance.ChangeRequest](string(b)); err == nil {
			return after, nil
		}
	}
	return governance.ChangeRequestID{}, flaghttp.InvalidRequest("cursor is not a next_cursor this API returned")
}

// CreateChangeRequest proposes a change.
func (a *api) CreateChangeRequest(ctx context.Context, req CreateChangeRequestRequestObject) (CreateChangeRequestResponseObject, error) {
	b := req.Body
	cr, err := a.h.CreateChangeRequest.Handle(ctx, appgovernance.CreateChangeRequestCommand{
		Project: projectID(ctx), Kind: governance.ChangeRequestKind(b.Kind), Flag: b.Flag, Environment: b.Environment,
		Title: b.Title, Description: valueOf(b.Description), Proposed: configurationIn(b.Proposed), Open: valueOr(b.Open, false),
		Author: authorOf(ctx), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	out, err := changeRequestOut(cr)
	if err != nil {
		return nil, err
	}
	return CreateChangeRequest201JSONResponse{Body: out, Headers: CreateChangeRequest201ResponseHeaders{ETag: etag(cr.Version)}}, nil
}

// GetChangeRequest answers a change request.
func (a *api) GetChangeRequest(ctx context.Context, req GetChangeRequestRequestObject) (GetChangeRequestResponseObject, error) {
	cr, err := a.h.GetChangeRequest.Handle(ctx, projectID(ctx), id.FromBytes[governance.ChangeRequest](req.ChangeRequest))
	if err != nil {
		return nil, err
	}
	out, err := changeRequestOut(cr)
	if err != nil {
		return nil, err
	}
	return GetChangeRequest200JSONResponse{Body: out, Headers: GetChangeRequest200ResponseHeaders{ETag: etag(cr.Version)}}, nil
}

// UpdateChangeRequest edits a change request.
func (a *api) UpdateChangeRequest(ctx context.Context, req UpdateChangeRequestRequestObject) (UpdateChangeRequestResponseObject, error) {
	cmd, err := changeRequestCommand(ctx, req.ChangeRequest, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	update := appgovernance.UpdateChangeRequestCommand{ChangeRequestCommand: cmd, Title: b.Title, Description: b.Description}
	if b.Proposed != nil {
		in := configurationIn(*b.Proposed)
		update.Proposed = &in
	}
	cr, err := a.h.UpdateChangeRequest.Handle(ctx, update)
	if err != nil {
		return nil, err
	}
	out, err := changeRequestOut(cr)
	if err != nil {
		return nil, err
	}
	return UpdateChangeRequest200JSONResponse{Body: out, Headers: UpdateChangeRequest200ResponseHeaders{ETag: etag(cr.Version)}}, nil
}

// OpenChangeRequest opens a draft change request for review.
func (a *api) OpenChangeRequest(ctx context.Context, req OpenChangeRequestRequestObject) (OpenChangeRequestResponseObject, error) {
	cr, err := a.changeRequestTransition(ctx, req.ChangeRequest, req.Params.IfMatch, a.h.OpenChangeRequest.Handle)
	if err != nil {
		return nil, err
	}
	return OpenChangeRequest200JSONResponse{Body: cr.out, Headers: OpenChangeRequest200ResponseHeaders{ETag: cr.etag}}, nil
}

// RebaseChangeRequest bases a draft change request on the configuration as
// it is now.
func (a *api) RebaseChangeRequest(ctx context.Context, req RebaseChangeRequestRequestObject) (RebaseChangeRequestResponseObject, error) {
	cr, err := a.changeRequestTransition(ctx, req.ChangeRequest, req.Params.IfMatch, a.h.RebaseChangeRequest.Handle)
	if err != nil {
		return nil, err
	}
	return RebaseChangeRequest200JSONResponse{Body: cr.out, Headers: RebaseChangeRequest200ResponseHeaders{ETag: cr.etag}}, nil
}

// CancelChangeRequest cancels a change request.
func (a *api) CancelChangeRequest(ctx context.Context, req CancelChangeRequestRequestObject) (CancelChangeRequestResponseObject, error) {
	cr, err := a.changeRequestTransition(ctx, req.ChangeRequest, req.Params.IfMatch, a.h.CancelChangeRequest.Handle)
	if err != nil {
		return nil, err
	}
	return CancelChangeRequest200JSONResponse{Body: cr.out, Headers: CancelChangeRequest200ResponseHeaders{ETag: cr.etag}}, nil
}

// CopyChangeRequest copies a change request into a new draft of the
// caller's.
func (a *api) CopyChangeRequest(ctx context.Context, req CopyChangeRequestRequestObject) (CopyChangeRequestResponseObject, error) {
	cr, err := a.h.CopyChangeRequest.Handle(ctx, appgovernance.CopyChangeRequestCommand{
		Project: projectID(ctx), ChangeRequest: id.FromBytes[governance.ChangeRequest](req.ChangeRequest),
		Author: authorOf(ctx), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	out, err := changeRequestOut(cr)
	if err != nil {
		return nil, err
	}
	return CopyChangeRequest201JSONResponse{Body: out, Headers: CopyChangeRequest201ResponseHeaders{ETag: etag(cr.Version)}}, nil
}

// transitioned is a change request as a transition answers it.
type transitioned struct {
	out  ChangeRequest
	etag *string
}

// changeRequestTransition runs handle on the change request changeRequest
// names, based on the version header names, and answers it.
func (a *api) changeRequestTransition(ctx context.Context, changeRequest ChangeRequestID, header *IfMatch,
	handle func(context.Context, appgovernance.ChangeRequestCommand) (governance.ChangeRequest, error),
) (transitioned, error) {
	cmd, err := changeRequestCommand(ctx, changeRequest, header)
	if err != nil {
		return transitioned{}, err
	}
	cr, err := handle(ctx, cmd)
	if err != nil {
		return transitioned{}, err
	}
	out, err := changeRequestOut(cr)
	return transitioned{out: out, etag: etag(cr.Version)}, err
}

func changeRequestCommand(ctx context.Context, changeRequest ChangeRequestID, header *IfMatch) (appgovernance.ChangeRequestCommand, error) {
	expected, err := ifMatch(header)
	if err != nil {
		return appgovernance.ChangeRequestCommand{}, err
	}
	return appgovernance.ChangeRequestCommand{
		Project: projectID(ctx), ChangeRequest: id.FromBytes[governance.ChangeRequest](changeRequest), Expected: expected,
		By: governance.Taker{As: authorOf(ctx)}, Request: requestOf(ctx),
	}, nil
}

// authorOf returns who the request ctx belongs to proposes as: the member
// its credential acts for, or the project token, which acts for none.
func authorOf(ctx context.Context) governance.Author {
	c := callerOf(ctx)
	if c.Member != nil {
		return governance.Author{Member: &c.Member.ID}
	}
	if c.Token != nil {
		return governance.Author{Token: &c.Token.ID}
	}
	return governance.Author{}
}

func changeRequestOut(cr governance.ChangeRequest) (ChangeRequest, error) {
	out := ChangeRequest{
		Id: openapi_types.UUID(cr.ID.Bytes()), Kind: ChangeRequestKind(cr.Kind), State: ChangeRequestState(cr.State),
		Title: cr.Title, Description: cr.Description, BaseVersion: cr.BaseVersion, Diff: make([]PatchOperation, len(cr.Diff)),
		Author: ChangeRequestAuthor{Kind: ChangeRequestAuthorKindMember, Name: cr.Author.Name}, CreatedAt: cr.CreatedAt,
		UpdatedAt: cr.UpdatedAt, Version: cr.Version,
	}
	switch {
	case cr.Author.Token != nil:
		out.Author.Kind, out.Author.Id = ChangeRequestAuthorKindToken, openapi_types.UUID(cr.Author.Token.Bytes())
	case cr.Author.Member != nil:
		out.Author.Id = openapi_types.UUID(cr.Author.Member.Bytes())
	}
	if cr.Flag != nil {
		out.Flag = &cr.Flag.Key
	}
	if cr.Environment != nil {
		out.Environment = &cr.Environment.Key
	}
	for i, op := range cr.Diff {
		out.Diff[i] = PatchOperation{Op: PatchOperationOp(op.Op), Path: op.Path}
		if op.Op != "remove" {
			v, err := json.Marshal(op.Value)
			if err != nil {
				return out, err
			}
			raw := json.RawMessage(v)
			out.Diff[i].Value = &raw
		}
	}
	// The document proposed is the one the API answers: the change request
	// keeps it in that shape so the diff's paths name its fields.
	b, err := json.Marshal(cr.Proposed)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(b, &out.Proposed)
}
