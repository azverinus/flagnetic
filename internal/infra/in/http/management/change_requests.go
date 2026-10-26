package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
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
	f := appgovernance.ChangeRequestFilter{
		Project: projectID(ctx), Flag: valueOf(p.Flag), Environment: valueOf(p.Environment), Segment: valueOf(p.Segment),
	}
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
	f.Scheduled = p.Scheduled
	if p.AwaitingMyDecision != nil && *p.AwaitingMyDecision {
		f.AwaitingDecisionOf = &governance.Taker{As: authorOf(ctx), MayDecide: mayDecide(ctx)}
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
	target := appgovernance.Target{
		Kind: governance.ChangeRequestKind(b.Kind), Flag: valueOf(b.Flag), Environment: valueOf(b.Environment),
		Segment: valueOf(b.Segment),
	}
	// A revert gives no proposal: the server fills it from revert_to.
	var proposed appgovernance.Proposed
	if b.Proposed != nil {
		var err error
		if proposed, err = proposedIn(target, *b.Proposed); err != nil {
			return nil, err
		}
	}
	cr, err := a.h.CreateChangeRequest.Handle(ctx, appgovernance.CreateChangeRequestCommand{
		Project: projectID(ctx), Target: target, Title: b.Title, Description: valueOf(b.Description), Proposed: proposed,
		RevertTo: b.RevertTo, Open: valueOr(b.Open, false), ApplyAt: b.ApplyAt, Author: authorOf(ctx), Request: requestOf(ctx),
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
		// What a change request proposes is read by its kind and entity,
		// which only the change request knows.
		current, readErr := a.h.GetChangeRequest.Handle(ctx, cmd.Project, cmd.ChangeRequest)
		if readErr != nil {
			return nil, readErr
		}
		in, readErr := proposedIn(appgovernance.TargetOf(current), *b.Proposed)
		if readErr != nil {
			return nil, readErr
		}
		update.Proposed = &in
	}
	if b.ApplyAt.IsSpecified() {
		update.Schedule = &governance.ScheduleEdit{}
		if !b.ApplyAt.IsNull() {
			at := b.ApplyAt.MustGet()
			update.Schedule.At = &at
		}
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

// ApproveChangeRequest approves an open change request.
func (a *api) ApproveChangeRequest(ctx context.Context, req ApproveChangeRequestRequestObject) (ApproveChangeRequestResponseObject, error) {
	cmd, err := changeRequestCommand(ctx, req.ChangeRequest, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	cr, err := a.h.ApproveChangeRequest.Handle(ctx, appgovernance.DecideChangeRequestCommand{
		ChangeRequestCommand: cmd, Comment: valueOf(req.Body.Comment),
	})
	if err != nil {
		return nil, err
	}
	out, err := changeRequestOut(cr)
	if err != nil {
		return nil, err
	}
	return ApproveChangeRequest200JSONResponse{Body: out, Headers: ApproveChangeRequest200ResponseHeaders{ETag: etag(cr.Version)}}, nil
}

// RejectChangeRequest rejects an open change request.
func (a *api) RejectChangeRequest(ctx context.Context, req RejectChangeRequestRequestObject) (RejectChangeRequestResponseObject, error) {
	cmd, err := changeRequestCommand(ctx, req.ChangeRequest, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	cr, err := a.h.RejectChangeRequest.Handle(ctx, appgovernance.DecideChangeRequestCommand{
		ChangeRequestCommand: cmd, Comment: req.Body.Comment,
	})
	if err != nil {
		return nil, err
	}
	out, err := changeRequestOut(cr)
	if err != nil {
		return nil, err
	}
	return RejectChangeRequest200JSONResponse{Body: out, Headers: RejectChangeRequest200ResponseHeaders{ETag: etag(cr.Version)}}, nil
}

// ApplyChangeRequest applies an approved change request.
func (a *api) ApplyChangeRequest(ctx context.Context, req ApplyChangeRequestRequestObject) (ApplyChangeRequestResponseObject, error) {
	cr, err := a.changeRequestTransition(ctx, req.ChangeRequest, req.Params.IfMatch, a.h.ApplyChangeRequest.Handle)
	if err != nil {
		return nil, err
	}
	return ApplyChangeRequest200JSONResponse{Body: cr.out, Headers: ApplyChangeRequest200ResponseHeaders{ETag: cr.etag}}, nil
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

// proposedIn reads in as the document t calls for: a configuration, a
// flag's definition or a segment's. A target calling for none proposes
// nothing, which the change request refuses as it refuses the target.
func proposedIn(t appgovernance.Target, in ProposedInput) (appgovernance.Proposed, error) {
	var out appgovernance.Proposed
	switch {
	case t.Kind == governance.KindFlagConfiguration:
		c, err := in.AsFlagConfigurationPut()
		if err != nil {
			return out, flaghttp.InvalidRequest("proposed is not a flag configuration")
		}
		configuration := configurationIn(c)
		out.Configuration = &configuration
	case t.Kind == governance.KindDefinition && t.Segment != "":
		d, err := in.AsSegmentDefinitionPut()
		if err != nil {
			return out, flaghttp.InvalidRequest("proposed is not a segment's definition")
		}
		conditions := conditionsIn(d.Conditions)
		if conditions == nil {
			conditions = []flags.Condition{}
		}
		out.Segment = &flags.SegmentChange{Conditions: &conditions, Private: &d.Private}
	case t.Kind == governance.KindDefinition:
		d, err := in.AsFlagDefinitionPut()
		if err != nil {
			return out, flaghttp.InvalidRequest("proposed is not a flag's definition")
		}
		out.Flag = &flags.FlagChange{AutomaticExposure: &d.AutomaticExposure, Variations: variationsIn(&d.Variations)}
	}
	return out, nil
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
		By: governance.Taker{As: authorOf(ctx), MayDecide: mayDecide(ctx)}, Request: requestOf(ctx),
	}, nil
}

// mayDecide reports whether the caller of ctx may decide change requests:
// whoever may also cancels or applies another's. A project token never holds
// the permission.
func mayDecide(ctx context.Context) bool {
	return slices.Contains(projectOf(ctx).permissions, access.PermChangeRequestApprove)
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

// decisionOut returns d as the API answers it, nil for none.
func decisionOut(d *governance.Decision) *ChangeRequestDecision {
	if d == nil {
		return nil
	}
	return &ChangeRequestDecision{
		Member: ChangeRequestMember{Id: openapi_types.UUID(d.Member.Bytes()), Name: d.Name}, At: d.At, Comment: d.Comment,
	}
}

func changeRequestOut(cr governance.ChangeRequest) (ChangeRequest, error) {
	out := ChangeRequest{
		Id: openapi_types.UUID(cr.ID.Bytes()), Kind: ChangeRequestKind(cr.Kind), State: ChangeRequestState(cr.State),
		Title: cr.Title, Description: cr.Description, BaseVersion: cr.BaseVersion, Diff: make([]PatchOperation, len(cr.Diff)),
		Author: ChangeRequestAuthor{Kind: ChangeRequestAuthorKindMember, Name: cr.Author.Name}, CreatedAt: cr.CreatedAt,
		UpdatedAt: cr.UpdatedAt, Version: cr.Version, RevertTo: cr.RevertTo,
	}
	switch {
	case cr.Author.Token != nil:
		out.Author.Kind, out.Author.Id = ChangeRequestAuthorKindToken, openapi_types.UUID(cr.Author.Token.Bytes())
	case cr.Author.Member != nil:
		out.Author.Id = openapi_types.UUID(cr.Author.Member.Bytes())
	}
	out.Approval, out.Rejection = decisionOut(cr.Approval), decisionOut(cr.Rejection)
	if a := cr.Applied; a != nil {
		out.AppliedAt, out.AppliedBy = &a.At, &ChangeRequestApplier{Kind: ChangeRequestApplierKindSystem, Name: a.By.Name}
		switch {
		case a.By.Token != nil:
			applier := openapi_types.UUID(a.By.Token.Bytes())
			out.AppliedBy.Kind, out.AppliedBy.Id = ChangeRequestApplierKindToken, &applier
		case a.By.Member != nil:
			applier := openapi_types.UUID(a.By.Member.Bytes())
			out.AppliedBy.Kind, out.AppliedBy.Id = ChangeRequestApplierKindMember, &applier
		}
	}
	if !cr.SupersededBy.IsZero() {
		entry := openapi_types.UUID(cr.SupersededBy.Bytes())
		out.SupersededBy = &entry
	}
	out.ApplyAt = cr.ApplyAt
	if f := cr.ScheduleFailure; f != nil {
		out.ScheduleFailure = &ScheduleFailure{At: f.At, Code: f.Code, Message: f.Message, Attempts: f.Attempts, Stopped: f.Stopped}
	}
	if cr.Flag != nil {
		out.Flag = &cr.Flag.Key
	}
	if cr.Environment != nil {
		out.Environment = &cr.Environment.Key
	}
	if cr.Segment != nil {
		out.Segment = &cr.Segment.Key
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
