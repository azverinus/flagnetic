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

// CreateChangeRequest proposes a flag's configuration in one environment, a
// flag's or a segment's definition in every environment, or a transition of
// an experiment in every environment holding its rule.
type CreateChangeRequest struct{ d ChangeRequestDeps }

// NewCreateChangeRequest returns the handler.
func NewCreateChangeRequest(d ChangeRequestDeps) *CreateChangeRequest {
	return &CreateChangeRequest{d: d}
}

// CreateChangeRequestCommand is the change request to create.
type CreateChangeRequestCommand struct {
	Project workspace.ProjectID
	Target
	Title       string
	Description string
	Proposed    Proposed
	// RevertTo, in place of Proposed, proposes reverting a flag's
	// configuration to this version of it; nil for any other change.
	RevertTo *int64
	// Open opens the change request for review at once.
	Open bool
	// ApplyAt schedules the change request, nil for none.
	ApplyAt *time.Time
	Author  governance.Author
	Request governance.Request
}

// Handle creates the change request, based on its entity as it is now, and
// returns it. The kind and the entity named are checked first; the proposal
// is checked as writing it would be, so a change request that could never
// be applied is refused now and not at review.
func (h *CreateChangeRequest) Handle(ctx context.Context, cmd CreateChangeRequestCommand) (governance.ChangeRequest, error) {
	if err := cmd.check(); err != nil {
		return governance.ChangeRequest{}, err
	}
	now := h.d.now()
	var out governance.ChangeRequest
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		cr, err := propose(ctx, h.d, cmd.Project, cmd.Target, cmd.Proposed, cmd.RevertTo, governance.Proposal{
			Title: cmd.Title, Description: cmd.Description,
		}, cmd.Open, cmd.ApplyAt, cmd.Author, now)
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

// check returns invalid_change_request naming every field of cmd to fix:
// those of its target, and a revert's target version given with a proposal,
// which the server fills from it, or for anything but a flag's
// configuration.
func (cmd CreateChangeRequestCommand) check() error {
	err := cmd.Target.check()
	if cmd.RevertTo == nil {
		return err
	}
	var violations []apperrors.Violation
	if e, ok := apperrors.From(err); ok {
		violations = e.Violations
	} else if err != nil {
		return err
	}
	switch {
	case cmd.Kind != governance.KindFlagConfiguration:
		violations = append(violations, apperrors.Violation{
			Pointer: "/revert_to", Code: "not_allowed", Detail: "a revert returns a flag's configuration to an earlier version",
		})
	case cmd.Proposed != (Proposed{}):
		violations = append(violations, apperrors.Violation{
			Pointer: "/revert_to", Code: "not_allowed", Detail: "a revert proposes the version it returns to, not a proposal of its own",
		})
	}
	if violations != nil {
		return apperrors.Invalid("invalid_change_request", "the change request is not valid", violations...)
	}
	return nil
}

// propose returns a new change request of author proposing in for t in
// project, or reverting t to revertTo unless nil, based on its entity as it
// is now, to be applied at applyAt unless nil.
func propose(ctx context.Context, d ChangeRequestDeps, project workspace.ProjectID, t Target, in Proposed, revertTo *int64,
	p governance.Proposal, open bool, applyAt *time.Time, author governance.Author, now time.Time,
) (governance.ChangeRequest, error) {
	var (
		subject  governance.Subject
		proposed governance.Document
		err      error
	)
	if revertTo != nil {
		subject, proposed, err = d.proposeRevert(ctx, project, t, *revertTo)
	} else {
		subject, proposed, err = d.propose(ctx, project, t, in, nil)
	}
	if err != nil {
		return governance.ChangeRequest{}, err
	}
	p.Proposed = proposed
	return governance.NewChangeRequest(governance.NewChangeRequestInput{
		ID: id.New[governance.ChangeRequest](d.IDs), Project: project, Subject: subject, Proposal: p, RevertTo: revertTo,
		Open: open, Author: author, ApplyAt: applyAt, MinLead: d.MinLead, Now: now,
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
	// Proposed is the document now proposed, the one the change request's
	// kind and entity call for.
	Proposed *Proposed
	// Schedule sets or clears the apply time.
	Schedule *governance.ScheduleEdit
}

// Handle edits the change request and returns it. A new proposal is checked
// against its entity as it is now, as writing it would be, and the diff
// recomputed from the base the change request keeps: an edit is no rebase.
// Who may edit and the state are judged before the proposal, the version
// the edit was based on after it; an edit that changes nothing stores and
// records nothing.
func (h *UpdateChangeRequest) Handle(ctx context.Context, cmd UpdateChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd.ChangeRequestCommand, "change_request.updated",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionEdit, cmd.By); err != nil {
				return cr, false, err
			}
			if cr.RevertTo != nil && cmd.Proposed != nil {
				// What a revert proposes is the server's: the domain
				// refuses it before anything is checked against it.
				return cr.Edit(cmd.By, governance.Edit{Proposed: cr.Proposed}, now)
			}
			e := governance.Edit{Title: cmd.Title, Description: cmd.Description, Schedule: cmd.Schedule, MinLead: h.d.MinLead}
			if cmd.Proposed != nil {
				// The rules the change request proposes keep the
				// identifiers and salts they were given when it was made.
				_, proposed, err := h.d.propose(ctx, cr.Project, TargetOf(cr), *cmd.Proposed, cr.Proposed)
				if err != nil {
					return cr, false, err
				}
				e.Proposed = proposed
			}
			return cr.Edit(cmd.By, e, now)
		})
}

// RebaseChangeRequest bases a draft change request on its entity as it is
// now.
type RebaseChangeRequest struct{ d ChangeRequestDeps }

// NewRebaseChangeRequest returns the handler.
func NewRebaseChangeRequest(d ChangeRequestDeps) *RebaseChangeRequest {
	return &RebaseChangeRequest{d: d}
}

// Handle rebases the change request and returns it: what it proposes stays,
// and its diff is recomputed from its entity now; a revert's proposal is
// filled again from the version it returns to, checked as reverting to it
// now would be. Rebasing onto the base it has already stores and records
// nothing.
func (h *RebaseChangeRequest) Handle(ctx context.Context, cmd ChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd, "change_request.rebased",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionRebase, cmd.By); err != nil {
				return cr, false, err
			}
			if cr.RevertTo != nil {
				subject, proposed, err := h.d.proposeRevert(ctx, cr.Project, TargetOf(cr), *cr.RevertTo)
				if err != nil {
					return cr, false, err
				}
				return cr.RebaseRevert(cmd.By, subject, proposed, now)
			}
			subject, err := h.d.current(ctx, cr)
			if err != nil {
				return cr, false, err
			}
			return cr.Rebase(cmd.By, subject, now)
		})
}

// OpenChangeRequest opens a draft change request for review.
type OpenChangeRequest struct{ d ChangeRequestDeps }

// NewOpenChangeRequest returns the handler.
func NewOpenChangeRequest(d ChangeRequestDeps) *OpenChangeRequest { return &OpenChangeRequest{d: d} }

// Handle opens the change request and returns it, or refuses it as
// base_moved when its entity was written since it was based on it, or, for
// an experiment, when other environments hold its rule now.
func (h *OpenChangeRequest) Handle(ctx context.Context, cmd ChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd, "change_request.opened",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionOpen, cmd.By); err != nil {
				return cr, false, err
			}
			subject, err := h.d.current(ctx, cr)
			if err != nil {
				return cr, false, err
			}
			opened, err := cr.Open(cmd.By, governance.Current{Version: subject.BaseVersion, Holders: subject.BaseHolders}, now)
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
// the caller's with its title, description and proposal, unscheduled, since
// the time it named may have passed, based on its entity as it is now and
// checked against it as a new change request is: a rejected or abandoned
// proposal is resumed on the current base. A revert's copy reverts to the
// same version, its proposal filled from it again. The
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
		var in Proposed
		if source.RevertTo == nil {
			if in, err = proposedOf(source); err != nil {
				return err
			}
		}
		cr, err := propose(ctx, h.d, cmd.Project, TargetOf(source), in, source.RevertTo, governance.Proposal{
			Title: source.Title, Description: source.Description,
		}, false, nil, cmd.Author, now)
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

// WaitingCounter counts the change requests waiting on a configuration.
type WaitingCounter interface {
	// WaitingOn returns how many of the change requests on flag's
	// configuration in environment are open and how many approved.
	WaitingOn(ctx context.Context, flag flags.FlagID, environment workspace.EnvironmentID) (governance.Waiting, error)
}

// CountWaitingChangeRequests counts the change requests waiting on a flag's
// configuration in one environment, which a member reads with the
// configuration before writing it.
type CountWaitingChangeRequests struct{ store WaitingCounter }

// NewCountWaitingChangeRequests returns the handler.
func NewCountWaitingChangeRequests(store WaitingCounter) *CountWaitingChangeRequests {
	return &CountWaitingChangeRequests{store: store}
}

// Handle returns the change requests waiting on flag's configuration in
// environment and the earliest time one of them is scheduled for.
func (h *CountWaitingChangeRequests) Handle(ctx context.Context, flag flags.FlagID, environment workspace.EnvironmentID,
) (governance.Waiting, error) {
	return h.store.WaitingOn(ctx, flag, environment)
}

// ChangeRequestFilter selects a project's change requests; every field set
// narrows the selection.
type ChangeRequestFilter struct {
	Project workspace.ProjectID
	// Flag, Environment and Segment select by the keys of the flag, the
	// environment and the segment a change request names.
	Flag        string
	Environment string
	Segment     string
	// Experiment selects by the key of the experiment a change request
	// proposes a transition of.
	Experiment string
	Kind       governance.ChangeRequestKind
	State      governance.ChangeRequestState
	// Author selects the change requests of a member or a project token,
	// by its identifier.
	Author string
	// AwaitingDecisionOf, when set, selects the open change requests it
	// may approve or reject: none unless it may decide change requests and
	// is a member, since a project token never decides, and none it
	// authored.
	AwaitingDecisionOf *governance.Taker
	// NotAuthoredBy leaves out the change requests of the member with this
	// identifier.
	NotAuthoredBy string
	// Scheduled, when set, selects the change requests with an apply time
	// (true) or without one (false).
	Scheduled *bool
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
	if by := f.AwaitingDecisionOf; by != nil {
		if !by.MayDecide || by.As.Member == nil || (f.State != "" && f.State != governance.StateOpen) {
			return []governance.ChangeRequest{}, governance.ChangeRequestID{}, nil
		}
		f.State, f.NotAuthoredBy = governance.StateOpen, by.As.Member.String()
	}
	list, err := h.store.ChangeRequests(ctx, f, p.After, p.Limit+1)
	if err != nil || len(list) <= p.Limit {
		return list, governance.ChangeRequestID{}, err
	}
	list = list[:p.Limit]
	return list, list[p.Limit-1].ID, nil
}

// DecideChangeRequestCommand is an approval or a rejection of a change
// request, and what the member deciding says.
type DecideChangeRequestCommand struct {
	ChangeRequestCommand
	Comment string
}

// ApproveChangeRequest approves an open change request.
type ApproveChangeRequest struct{ d ChangeRequestDeps }

// NewApproveChangeRequest returns the handler.
func NewApproveChangeRequest(d ChangeRequestDeps) *ApproveChangeRequest {
	return &ApproveChangeRequest{d: d}
}

// Handle approves the change request and returns it; a definition records
// the environments protected now as those its approval covers. When its
// entity was written since the change request was based on it, what would
// be approved is not what was reviewed: the change request is superseded
// and the approval refused as base_moved.
func (h *ApproveChangeRequest) Handle(ctx context.Context, cmd DecideChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd.ChangeRequestCommand, "change_request.approved",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			if _, err := cr.Next(governance.TransitionApprove, cmd.By); err != nil {
				return cr, false, err
			}
			current, err := h.d.judged(ctx, cr)
			if err != nil {
				return cr, false, err
			}
			approved, err := cr.Approve(cmd.By, cmd.Comment, current, now)
			return supersededOr(cr, approved, err)
		})
}

// RejectChangeRequest rejects an open change request.
type RejectChangeRequest struct{ d ChangeRequestDeps }

// NewRejectChangeRequest returns the handler.
func NewRejectChangeRequest(d ChangeRequestDeps) *RejectChangeRequest {
	return &RejectChangeRequest{d: d}
}

// Handle rejects the change request and returns it.
func (h *RejectChangeRequest) Handle(ctx context.Context, cmd DecideChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd.ChangeRequestCommand, "change_request.rejected",
		func(_ context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			rejected, err := cr.Reject(cmd.By, cmd.Comment, now)
			return rejected, err == nil, err
		})
}

// ApplyChangeRequest applies an approved change request.
type ApplyChangeRequest struct{ d ChangeRequestDeps }

// NewApplyChangeRequest returns the handler.
func NewApplyChangeRequest(d ChangeRequestDeps) *ApplyChangeRequest {
	return &ApplyChangeRequest{d: d}
}

// Handle applies the change request and returns it: under the project's
// lock it re-reads the change request and its entity, and when the entity
// is still the one the change request was based on, the context owning it
// writes the proposal in the same transaction, allowed to change the
// environments the change request covers although they are protected, and
// its entries carry the change request's identifier. When the entity was
// written since, or an environment its approval does not cover was
// protected since, the change request is superseded and the application
// refused as base_moved; when the write is refused, nothing is written and
// the change request stays approved.
func (h *ApplyChangeRequest) Handle(ctx context.Context, cmd ChangeRequestCommand) (governance.ChangeRequest, error) {
	return transition(ctx, h.d, cmd, "change_request.applied",
		func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
			return applyChange(ctx, h.d, cr, cmd.By, cmd.Request, now)
		})
}

// applyChange is what applying cr by by at now in req changes, as a
// transition's change answers it: what cr proposes written by the context
// owning its entity when the entity is still cr's base, cr superseded when it
// moved.
func applyChange(ctx context.Context, d ChangeRequestDeps, cr governance.ChangeRequest, by governance.Taker,
	req governance.Request, now time.Time,
) (governance.ChangeRequest, bool, error) {
	if _, err := cr.Next(governance.TransitionApply, by); err != nil {
		return cr, false, err
	}
	current, err := d.judged(ctx, cr)
	if err != nil {
		return cr, false, err
	}
	applied, err := cr.Apply(by, current, now)
	if err != nil {
		return supersededOr(cr, applied, err)
	}
	if err = d.write(ctx, cr, by.As.Member, req); err != nil {
		return cr, false, under(err, "/proposed")
	}
	return applied, true, nil
}
