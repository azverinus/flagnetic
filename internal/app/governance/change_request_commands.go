package governance

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CreateChangeRequest proposes a flag's configuration in one environment.
type CreateChangeRequest struct{ d ChangeRequestDeps }

// NewCreateChangeRequest returns the handler.
func NewCreateChangeRequest(d ChangeRequestDeps) *CreateChangeRequest {
	return &CreateChangeRequest{d: d}
}

// CreateChangeRequestCommand is the change request to create.
type CreateChangeRequestCommand struct {
	Project     workspace.ProjectID
	Kind        governance.ChangeRequestKind
	Flag        string
	Environment string
	Title       string
	Description string
	Proposed    flags.ConfigurationInput
	// Open opens the change request for review at once.
	Open    bool
	Author  governance.Author
	Request governance.Request
}

// Handle creates the change request, based on the configuration as it is
// now, and returns it. The proposal is checked as replacing the
// configuration would be, so a change request that could never be applied
// is refused now and not at review.
func (h *CreateChangeRequest) Handle(ctx context.Context, cmd CreateChangeRequestCommand) (governance.ChangeRequest, error) {
	if cmd.Kind != governance.KindFlagConfiguration {
		return governance.ChangeRequest{}, apperrors.Invalid("invalid_change_request", "the change request is not valid",
			apperrors.Violation{Pointer: "/kind", Code: "invalid_value", Detail: "a change request proposes a flag configuration",
				Params: map[string]any{"kinds": []string{string(governance.KindFlagConfiguration)}}})
	}
	now := h.d.now()
	var out governance.ChangeRequest
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		cr, err := propose(ctx, h.d, cmd.Project, cmd.Flag, cmd.Environment, cmd.Proposed, governance.Proposal{
			Title: cmd.Title, Description: cmd.Description,
		}, cmd.Open, cmd.Author, now)
		if err != nil {
			return err
		}
		if err = h.d.Store.CreateChangeRequest(ctx, cr); err != nil {
			return err
		}
		action := "change_request.drafted"
		if cr.State == governance.StateOpen {
			action = "change_request.opened"
		}
		if err = h.d.record(ctx, now, cmd.Request, action, nil, cr, nil); err != nil {
			return err
		}
		// Read back, so the author is answered with their name as a later
		// read answers it.
		out, err = changeRequestOf(ctx, h.d.Store, cmd.Project, cr.ID)
		return err
	})
	return out, err
}

// propose returns a new change request of author proposing in for
// project's flag keyed flag in the environment keyed environment, based on
// its configuration now.
func propose(ctx context.Context, d ChangeRequestDeps, project workspace.ProjectID, flag, environment string,
	in flags.ConfigurationInput, p governance.Proposal, open bool, author governance.Author, now time.Time,
) (governance.ChangeRequest, error) {
	f, saved, proposed, err := d.Configurations.Propose(ctx, project, flag, environment, in)
	if err != nil {
		return governance.ChangeRequest{}, under(err, "/proposed")
	}
	subject, err := subjectOf(f, saved)
	if err != nil {
		return governance.ChangeRequest{}, err
	}
	if p.Proposed, err = configurationDocument(proposed); err != nil {
		return governance.ChangeRequest{}, err
	}
	return governance.NewChangeRequest(governance.NewChangeRequestInput{
		ID: id.New[governance.ChangeRequest](d.IDs), Project: project, Subject: subject, Proposal: p, Open: open,
		Author: author, Now: now,
	})
}

// UpdateChangeRequest edits a change request's title, description or
// proposal.
type UpdateChangeRequest struct{ d ChangeRequestDeps }

// NewUpdateChangeRequest returns the handler.
func NewUpdateChangeRequest(d ChangeRequestDeps) *UpdateChangeRequest {
	return &UpdateChangeRequest{d: d}
}

// UpdateChangeRequestCommand is what an author changes of a change request:
// nil leaves a field as it is.
type UpdateChangeRequestCommand struct {
	ChangeRequestCommand
	Title       *string
	Description *string
	Proposed    *flags.ConfigurationInput
}

// Handle edits the change request and returns it. A new proposal is checked
// against the configuration as it is now, as replacing it would be, and the
// diff recomputed from the base the change request keeps: an edit is no
// rebase. Who may edit and the state are judged before the proposal, the
// version the edit was based on after it; an edit that changes nothing
// stores and records nothing.
func (h *UpdateChangeRequest) Handle(ctx context.Context, cmd UpdateChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd.ChangeRequestCommand, "change_request.updated",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionEdit, cmd.By); err != nil {
				return cr, false, err
			}
			e := governance.Edit{Title: cmd.Title, Description: cmd.Description}
			if cmd.Proposed != nil {
				_, _, proposed, err := h.d.Configurations.Propose(ctx, cr.Project, cr.Flag.Key, cr.Environment.Key, *cmd.Proposed)
				if err != nil {
					return cr, false, under(err, "/proposed")
				}
				if e.Proposed, err = configurationDocument(proposed); err != nil {
					return cr, false, err
				}
			}
			return cr.Edit(cmd.By, e, now)
		})
}

// RebaseChangeRequest bases a draft change request on the configuration as
// it is now.
type RebaseChangeRequest struct{ d ChangeRequestDeps }

// NewRebaseChangeRequest returns the handler.
func NewRebaseChangeRequest(d ChangeRequestDeps) *RebaseChangeRequest {
	return &RebaseChangeRequest{d: d}
}

// Handle rebases the change request and returns it: what it proposes stays,
// and its diff is recomputed from the configuration now. Rebasing onto the
// base it has already stores and records nothing.
func (h *RebaseChangeRequest) Handle(ctx context.Context, cmd ChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd, "change_request.rebased",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionRebase, cmd.By); err != nil {
				return cr, false, err
			}
			f, saved, err := h.d.Configurations.Current(ctx, cr.Project, cr.Flag.Key, cr.Environment.Key)
			if err != nil {
				return cr, false, err
			}
			subject, err := subjectOf(f, saved)
			if err != nil {
				return cr, false, err
			}
			return cr.Rebase(cmd.By, subject.Base, subject.BaseVersion, now)
		})
}

// OpenChangeRequest opens a draft change request for review.
type OpenChangeRequest struct{ d ChangeRequestDeps }

// NewOpenChangeRequest returns the handler.
func NewOpenChangeRequest(d ChangeRequestDeps) *OpenChangeRequest { return &OpenChangeRequest{d: d} }

// Handle opens the change request and returns it, or refuses it as
// base_moved when the configuration was written since it was based on it.
func (h *OpenChangeRequest) Handle(ctx context.Context, cmd ChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd, "change_request.opened",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionOpen, cmd.By); err != nil {
				return cr, false, err
			}
			_, saved, err := h.d.Configurations.Current(ctx, cr.Project, cr.Flag.Key, cr.Environment.Key)
			if err != nil {
				return cr, false, err
			}
			opened, err := cr.Open(cmd.By, saved.Configuration.Version, now)
			return opened, err == nil, err
		})
}

// CancelChangeRequest cancels a change request.
type CancelChangeRequest struct{ d ChangeRequestDeps }

// NewCancelChangeRequest returns the handler.
func NewCancelChangeRequest(d ChangeRequestDeps) *CancelChangeRequest {
	return &CancelChangeRequest{d: d}
}

// Handle cancels the change request and returns it.
func (h *CancelChangeRequest) Handle(ctx context.Context, cmd ChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd, "change_request.cancelled",
		func(_ context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			cancelled, err := cr.Cancel(cmd.By, now)
			return cancelled, err == nil, err
		})
}

// CopyChangeRequest copies a change request into a new draft.
type CopyChangeRequest struct{ d ChangeRequestDeps }

// NewCopyChangeRequest returns the handler.
func NewCopyChangeRequest(d ChangeRequestDeps) *CopyChangeRequest { return &CopyChangeRequest{d: d} }

// CopyChangeRequestCommand names the change request to copy and who copies
// it.
type CopyChangeRequestCommand struct {
	Project       workspace.ProjectID
	ChangeRequest governance.ChangeRequestID
	Author        governance.Author
	Request       governance.Request
}

// Handle copies the change request, whatever its state, into a new draft of
// the caller's with its title, description and proposal, based on the
// configuration as it is now and checked against it as a new change request
// is: a rejected or abandoned proposal is resumed on the current base. The
// copy records the change request it was copied from.
func (h *CopyChangeRequest) Handle(ctx context.Context, cmd CopyChangeRequestCommand) (governance.ChangeRequest, error) {
	now := h.d.now()
	var out governance.ChangeRequest
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		source, err := changeRequestOf(ctx, h.d.Store, cmd.Project, cmd.ChangeRequest)
		if err != nil {
			return err
		}
		in, err := configurationInputOf(source.Proposed)
		if err != nil {
			return err
		}
		cr, err := propose(ctx, h.d, cmd.Project, source.Flag.Key, source.Environment.Key, in, governance.Proposal{
			Title: source.Title, Description: source.Description,
		}, false, cmd.Author, now)
		if err != nil {
			return err
		}
		if err = h.d.Store.CreateChangeRequest(ctx, cr); err != nil {
			return err
		}
		if err = h.d.record(ctx, now, cmd.Request, "change_request.copied", nil, cr,
			map[string]any{"copied_from": source.ID.String()}); err != nil {
			return err
		}
		out, err = changeRequestOf(ctx, h.d.Store, cmd.Project, cr.ID)
		return err
	})
	return out, err
}

// GetChangeRequest reads a change request.
type GetChangeRequest struct{ store ChangeRequestReader }

// NewGetChangeRequest returns the handler.
func NewGetChangeRequest(store ChangeRequestReader) *GetChangeRequest {
	return &GetChangeRequest{store: store}
}

// Handle returns project's change request with changeRequest, or not_found.
func (h *GetChangeRequest) Handle(ctx context.Context, project workspace.ProjectID, changeRequest governance.ChangeRequestID,
) (governance.ChangeRequest, error) {
	return changeRequestOf(ctx, h.store, project, changeRequest)
}

// ChangeRequestFilter selects a project's change requests; every field set
// narrows the selection.
type ChangeRequestFilter struct {
	Project workspace.ProjectID
	// Flag and Environment select by the keys of the flag and the
	// environment a change request names.
	Flag        string
	Environment string
	Kind        governance.ChangeRequestKind
	State       governance.ChangeRequestState
	// Author selects the change requests of a member or a project token,
	// by its identifier.
	Author string
}

// ChangeRequestPage asks for one page of change requests, newest first.
type ChangeRequestPage struct {
	// After is the last change request of the previous page, zero for the
	// first.
	After governance.ChangeRequestID
	Limit int
}

// ListChangeRequests reads a project's change requests a page at a time.
type ListChangeRequests struct{ store ChangeRequestReader }

// NewListChangeRequests returns the handler.
func NewListChangeRequests(store ChangeRequestReader) *ListChangeRequests {
	return &ListChangeRequests{store: store}
}

// Handle returns one page of the change requests matching f, newest first,
// and the last of the page when another follows (zero otherwise). Paging by
// identifier, a change request created meanwhile never shifts a page.
func (h *ListChangeRequests) Handle(ctx context.Context, f ChangeRequestFilter, p ChangeRequestPage,
) ([]governance.ChangeRequest, governance.ChangeRequestID, error) {
	list, err := h.store.ChangeRequests(ctx, f, p.After, p.Limit+1)
	if err != nil || len(list) <= p.Limit {
		return list, governance.ChangeRequestID{}, err
	}
	list = list[:p.Limit]
	return list, list[p.Limit-1].ID, nil
}
