package governance

import (
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ChangeRequestID identifies a change request.
type ChangeRequestID = id.Of[ChangeRequest]

// ChangeRequestKind is what a change request proposes to change.
type ChangeRequestKind string

// The kinds of change request. One resource carries them all, the kind
// choosing the entity it names and the document it proposes, so every
// transition, filter and audit action is written once.
const (
	// KindFlagConfiguration proposes one flag's configuration in one
	// environment.
	KindFlagConfiguration ChangeRequestKind = "flag_configuration"
	// KindDefinition proposes a flag's or a segment's definition in every
	// environment.
	KindDefinition ChangeRequestKind = "definition"
	// KindExperiment proposes an experiment's start, stop or traffic
	// allocation.
	KindExperiment ChangeRequestKind = "experiment"
)

// ChangeRequestState is where a change request is in its life.
type ChangeRequestState string

// The states of a change request. Rejected, applied, cancelled and
// superseded are final: nothing moves a change request out of them, and only
// a copy carries its work on.
const (
	StateDraft      ChangeRequestState = "draft"
	StateOpen       ChangeRequestState = "open"
	StateApproved   ChangeRequestState = "approved"
	StateRejected   ChangeRequestState = "rejected"
	StateApplied    ChangeRequestState = "applied"
	StateCancelled  ChangeRequestState = "cancelled"
	StateSuperseded ChangeRequestState = "superseded"
)

// ChangeRequestStates returns every state, in the order of a change
// request's life.
func ChangeRequestStates() []ChangeRequestState {
	return []ChangeRequestState{StateDraft, StateOpen, StateApproved, StateRejected, StateApplied, StateCancelled, StateSuperseded}
}

// The limits of what an author writes about a change request.
const (
	MaxChangeRequestTitle       = 200
	MaxChangeRequestDescription = 10_000
	// MaxDecisionComment bounds the comment of an approval or a rejection.
	MaxDecisionComment = 2_000
)

// Author is who proposed a change request: a member, or a project token,
// which acts for no member. Exactly one is set.
type Author struct {
	Member *access.MemberID
	Token  *access.APITokenID
	// Name is the member's display name or the token's name as it is now,
	// filled in when a change request is read; it plays no part in who the
	// author is.
	Name string
}

// Is reports whether a is b.
func (a Author) Is(b Author) bool {
	switch {
	case a.Member != nil && b.Member != nil:
		return *a.Member == *b.Member
	case a.Token != nil && b.Token != nil:
		return *a.Token == *b.Token
	}
	return false
}

// IsToken reports whether a is a project token.
func (a Author) IsToken() bool { return a.Token != nil }

// Document is a JSON value as encoding/json decodes it into an any: what a
// change request proposes and what it was based on. The governance context
// compares and diffs documents without knowing what they describe; the
// context owning the entity validates them.
type Document = any

// Subject is the entity a change request names, and the document of it the
// change request was based on.
type Subject struct {
	Kind ChangeRequestKind
	// Flag names the flag of a configuration or of a flag's definition.
	Flag *FlagRef
	// Environment names a configuration's environment.
	Environment *EnvironmentRef
	// Base is the document of the entity as the change request was based
	// on it, and BaseVersion that document's version.
	Base        Document
	BaseVersion int64
}

// FlagRef names a flag by identifier, and by the key it had when read.
type FlagRef struct {
	ID  flags.FlagID
	Key string
}

// EnvironmentRef names an environment by identifier, and by the key it had
// when read.
type EnvironmentRef struct {
	ID  workspace.EnvironmentID
	Key string
}

// ChangeRequest is a proposed change to one entity, reviewed before it is
// made.
type ChangeRequest struct {
	ID      ChangeRequestID
	Project workspace.ProjectID
	Subject
	State       ChangeRequestState
	Title       string
	Description string
	// Proposed is the document the change request would leave the entity
	// with, and Diff the RFC 6902 patch turning Base into it.
	Proposed Document
	Diff     []PatchOperation
	Author   Author
	// Approval and Rejection are the decision taken on the change request,
	// at most one of them; Applied is its application.
	Approval  *Decision
	Rejection *Decision
	Applied   *Application
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Decision is an approval or a rejection: the member who took it, when, and
// what they said.
type Decision struct {
	Member access.MemberID
	// Name is the member's display name as it is now, filled in when a
	// change request is read.
	Name    string
	At      time.Time
	Comment string
}

// Application is who applied a change request and when: an author, a
// member who may decide, or the system at the scheduled time.
type Application struct {
	At time.Time
	// By is the member or project token that applied it; zero when the
	// system did. Its Name is filled in when a change request is read.
	By     Author
	System bool
}

// Proposal is what an author writes of a change request: its title and
// description, and the document proposed, already checked by the context
// owning the entity.
type Proposal struct {
	Title       string
	Description string
	Proposed    Document
}

// NewChangeRequestInput is a change request as its author creates it.
type NewChangeRequestInput struct {
	ID      ChangeRequestID
	Project workspace.ProjectID
	Subject Subject
	Proposal
	// Open asks for the change request to be opened for review at once
	// rather than drafted.
	Open   bool
	Author Author
	Now    time.Time
}

// NewChangeRequest returns the change request in describes, drafted or
// opened, at version 1; or invalid_change_request naming every field to fix.
func NewChangeRequest(in NewChangeRequestInput) (ChangeRequest, error) {
	title, description, err := checkProposal(in.Proposal)
	if err != nil {
		return ChangeRequest{}, err
	}
	diff := Diff(in.Subject.Base, in.Proposed)
	state := StateDraft
	if in.Open {
		state = StateOpen
	}
	return ChangeRequest{
		ID: in.ID, Project: in.Project, Subject: in.Subject, State: state, Title: title, Description: description,
		Proposed: in.Proposed, Diff: diff, Author: in.Author, CreatedAt: in.Now, UpdatedAt: in.Now, Version: 1,
	}, nil
}

// checkProposal returns p's title and description trimmed, or
// invalid_change_request with every one to fix.
func checkProposal(p Proposal) (string, string, error) {
	var violations []apperrors.Violation
	title := strings.TrimSpace(p.Title)
	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		violations = append(violations, apperrors.Violation{Pointer: "/title", Code: "required", Detail: "a change request has a title"})
	case n > MaxChangeRequestTitle:
		violations = append(violations, apperrors.Violation{
			Pointer: "/title", Code: "too_long", Detail: "a title has at most 200 characters",
			Params: map[string]any{"max": MaxChangeRequestTitle},
		})
	}
	description := strings.TrimSpace(p.Description)
	if utf8.RuneCountInString(description) > MaxChangeRequestDescription {
		violations = append(violations, apperrors.Violation{
			Pointer: "/description", Code: "too_long", Detail: "a description has at most 10,000 characters",
			Params: map[string]any{"max": MaxChangeRequestDescription},
		})
	}
	if violations != nil {
		return "", "", apperrors.Invalid("invalid_change_request", "the change request is not valid", violations...)
	}
	return title, description, nil
}

// Transition is a move of a change request from one state to another, or
// within one.
type Transition string

// The transitions of a change request after it is created.
const (
	TransitionEdit      Transition = "edit"
	TransitionRebase    Transition = "rebase"
	TransitionOpen      Transition = "open"
	TransitionCancel    Transition = "cancel"
	TransitionApprove   Transition = "approve"
	TransitionReject    Transition = "reject"
	TransitionApply     Transition = "apply"
	TransitionSupersede Transition = "supersede"
)

// Taker is who takes a transition: an author as a member or a project token,
// whether they may decide change requests (approve, reject, and cancel or
// apply another's), or the system, which applies scheduled changes and
// supersedes change requests whose base moved.
type Taker struct {
	As        Author
	MayDecide bool
	System    bool
}

// who is who may take a transition.
type who int

const (
	// byAuthor: the author only, since what is proposed and when it is
	// offered for review is theirs.
	byAuthor who = iota
	// byAuthorOrDecider: the author, or whoever may decide.
	byAuthorOrDecider
	// byDecider: a member who may decide and is not the author, so an
	// approval is always a second person, and never a project token, so a
	// pipeline proposes and a human decides.
	byDecider
	// byAuthorDeciderOrSystem: the author, whoever may decide, or the
	// system applying at the scheduled time.
	byAuthorDeciderOrSystem
	// bySystem: the system alone.
	bySystem
)

// transitions is the state machine: from which states each transition is
// taken, the state it leaves, and who may take it.
var transitions = map[Transition]struct {
	from []ChangeRequestState
	to   ChangeRequestState
	who  who
}{
	TransitionEdit:      {from: []ChangeRequestState{StateDraft, StateOpen}, who: byAuthor},
	TransitionRebase:    {from: []ChangeRequestState{StateDraft}, to: StateDraft, who: byAuthor},
	TransitionOpen:      {from: []ChangeRequestState{StateDraft}, to: StateOpen, who: byAuthor},
	TransitionCancel:    {from: []ChangeRequestState{StateDraft, StateOpen, StateApproved}, to: StateCancelled, who: byAuthorOrDecider},
	TransitionApprove:   {from: []ChangeRequestState{StateOpen}, to: StateApproved, who: byDecider},
	TransitionReject:    {from: []ChangeRequestState{StateOpen}, to: StateRejected, who: byDecider},
	TransitionApply:     {from: []ChangeRequestState{StateApproved}, to: StateApplied, who: byAuthorDeciderOrSystem},
	TransitionSupersede: {from: []ChangeRequestState{StateOpen, StateApproved}, to: StateSuperseded, who: bySystem},
}

// The refusals of a transition.
var (
	ErrNotAuthor = apperrors.Forbidden("not_author", "only the author of the change request may do this")
	errNotYours  = apperrors.Forbidden("not_author",
		"only the author of the change request, or whoever may approve change requests, may do this")
	errAuthorCannotApprove = apperrors.Forbidden("author_cannot_approve", "the author of a change request cannot decide it")
	errTokenCannotApprove  = apperrors.Forbidden("token_cannot_approve", "a project token cannot decide a change request")
	errCannotDecide        = apperrors.Forbidden("forbidden", "deciding a change request needs the permission change_request.approve")
	errSystemOnly          = apperrors.Forbidden("forbidden", "only the system takes this transition")
)

// Next returns the state t leaves cr in; or, in this order, the refusal of
// by (not_author, author_cannot_approve, token_cannot_approve), then
// invalid_state when cr's state does not allow t. Who may act is answered
// first, so a caller who may never take t is not told the state of a change
// request they cannot touch.
func (cr ChangeRequest) Next(t Transition, by Taker) (ChangeRequestState, error) {
	rule, ok := transitions[t]
	if !ok {
		return "", errSystemOnly
	}
	if err := rule.who.allows(cr.Author, by); err != nil {
		return "", err
	}
	if !slices.Contains(rule.from, cr.State) {
		return "", apperrors.Conflict("invalid_state", "the change request is "+string(cr.State)+", which does not allow this").
			WithExtension("state", string(cr.State))
	}
	if rule.to == "" {
		return cr.State, nil
	}
	return rule.to, nil
}

func (w who) allows(author Author, by Taker) error {
	isAuthor := !by.System && author.Is(by.As)
	switch w {
	case byAuthor:
		if !isAuthor {
			return ErrNotAuthor
		}
	case byAuthorOrDecider:
		if !isAuthor && (by.System || !by.MayDecide) {
			return errNotYours
		}
	case byDecider:
		switch {
		case by.System:
			return errSystemOnly
		case by.As.IsToken():
			return errTokenCannotApprove
		case isAuthor:
			return errAuthorCannotApprove
		case !by.MayDecide:
			return errCannotDecide
		}
	case byAuthorDeciderOrSystem:
		if !isAuthor && !by.System && !by.MayDecide {
			return errNotYours
		}
	case bySystem:
		if !by.System {
			return errSystemOnly
		}
	}
	return nil
}

// Edit is what an author changes of a change request: nil leaves a field as
// it is.
type Edit struct {
	Title       *string
	Description *string
	// Proposed is the document now proposed, checked by the context owning
	// the entity; nil leaves it as it is.
	Proposed Document
}

// Edit returns cr edited by by at now, its diff recomputed against its base,
// its version incremented, and whether anything changed; cr itself when
// nothing did, so an edit that changes nothing writes nothing. Only the
// author edits, a draft or an open change request.
func (cr ChangeRequest) Edit(by Taker, e Edit, now time.Time) (ChangeRequest, bool, error) {
	if _, err := cr.Next(TransitionEdit, by); err != nil {
		return cr, false, err
	}
	p := Proposal{Title: cr.Title, Description: cr.Description, Proposed: cr.Proposed}
	if e.Title != nil {
		p.Title = *e.Title
	}
	if e.Description != nil {
		p.Description = *e.Description
	}
	if e.Proposed != nil {
		p.Proposed = e.Proposed
	}
	title, description, err := checkProposal(p)
	if err != nil {
		return cr, false, err
	}
	out := cr
	out.Title, out.Description, out.Proposed = title, description, p.Proposed
	out.Diff = Diff(cr.Base, p.Proposed)
	return cr.changedTo(out, now)
}

// Rebase returns cr based on base, the entity's document as it is now at
// version baseVersion, its diff recomputed from it to what cr proposes, and
// whether anything changed. Only the author rebases, and only a draft: an
// open change request is under review against the diff its reviewers see.
func (cr ChangeRequest) Rebase(by Taker, base Document, baseVersion int64, now time.Time) (ChangeRequest, bool, error) {
	if _, err := cr.Next(TransitionRebase, by); err != nil {
		return cr, false, err
	}
	out := cr
	out.Base, out.BaseVersion = base, baseVersion
	out.Diff = Diff(base, cr.Proposed)
	return cr.changedTo(out, now)
}

// Open returns cr opened for review by by at now. currentVersion is the
// version of the entity now: when it is not the one cr was based on, the
// diff no longer shows what applying it would do, and opening is refused as
// base_moved until the author rebases it.
func (cr ChangeRequest) Open(by Taker, currentVersion int64, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionOpen, by)
	if err != nil {
		return cr, err
	}
	if currentVersion != cr.BaseVersion {
		return cr, apperrors.Conflict("base_moved",
			"what the change request is based on has changed since; rebase it, then open it").
			WithExtension("base_version", cr.BaseVersion).WithExtension("current_version", currentVersion)
	}
	return cr.moved(state, now), nil
}

// Cancel returns cr cancelled by by at now.
func (cr ChangeRequest) Cancel(by Taker, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionCancel, by)
	if err != nil {
		return cr, err
	}
	return cr.moved(state, now), nil
}

// Approve returns cr approved by by at now with comment, which is
// optional. currentVersion is the version of the entity now: when it is not
// the one cr was based on, what would be approved is no longer what was
// reviewed, and Approve answers base_moved with cr superseded, which the
// caller stores although it refuses the approval.
func (cr ChangeRequest) Approve(by Taker, comment string, currentVersion int64, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionApprove, by)
	if err != nil {
		return cr, err
	}
	comment, err = checkComment(comment, false)
	if err != nil {
		return cr, err
	}
	if currentVersion != cr.BaseVersion {
		return cr.moved(StateSuperseded, now), baseMoved(cr.BaseVersion, currentVersion)
	}
	out := cr.moved(state, now)
	out.Approval = &Decision{Member: *by.As.Member, At: now, Comment: comment}
	return out, nil
}

// Reject returns cr rejected by by at now, with comment telling the author
// why, which is required.
func (cr ChangeRequest) Reject(by Taker, comment string, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionReject, by)
	if err != nil {
		return cr, err
	}
	if comment, err = checkComment(comment, true); err != nil {
		return cr, err
	}
	out := cr.moved(state, now)
	out.Rejection = &Decision{Member: *by.As.Member, At: now, Comment: comment}
	return out, nil
}

// Apply returns cr applied by by at now. currentVersion is the version of
// the entity now: when it is not the one cr was based on, applying it would
// write over a change nobody who approved it saw, and Apply answers
// base_moved with cr superseded, which the caller stores although it
// refuses the application.
func (cr ChangeRequest) Apply(by Taker, currentVersion int64, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionApply, by)
	if err != nil {
		return cr, err
	}
	if currentVersion != cr.BaseVersion {
		return cr.moved(StateSuperseded, now), baseMoved(cr.BaseVersion, currentVersion)
	}
	out := cr.moved(state, now)
	out.Applied = &Application{At: now, System: by.System}
	if !by.System {
		out.Applied.By = by.As
	}
	return out, nil
}

// baseMoved refuses to approve or apply a change request whose base moved.
func baseMoved(baseVersion, currentVersion int64) error {
	return apperrors.Conflict("base_moved",
		"what the change request is based on has changed since it was reviewed; copy it to propose it again").
		WithExtension("base_version", baseVersion).WithExtension("current_version", currentVersion)
}

// checkComment returns the comment of a decision trimmed, or
// invalid_change_request when it is over the limit, or blank and required.
func checkComment(comment string, required bool) (string, error) {
	comment = strings.TrimSpace(comment)
	switch n := utf8.RuneCountInString(comment); {
	case n == 0 && required:
		return "", apperrors.Invalid("invalid_change_request", "the decision is not valid", apperrors.Violation{
			Pointer: "/comment", Code: "required", Detail: "a rejection tells the author why",
		})
	case n > MaxDecisionComment:
		return "", apperrors.Invalid("invalid_change_request", "the decision is not valid", apperrors.Violation{
			Pointer: "/comment", Code: "too_long", Detail: "a comment has at most 2,000 characters",
			Params: map[string]any{"max": MaxDecisionComment},
		})
	}
	return comment, nil
}

// moved returns cr in state at now, as its next version.
func (cr ChangeRequest) moved(state ChangeRequestState, now time.Time) ChangeRequest {
	cr.State, cr.UpdatedAt, cr.Version = state, now, cr.Version+1
	return cr
}

// changedTo returns out as the next version of cr at now, and whether
// anything changed; cr itself when nothing did.
func (cr ChangeRequest) changedTo(out ChangeRequest, now time.Time) (ChangeRequest, bool, error) {
	if out.Title == cr.Title && out.Description == cr.Description && out.BaseVersion == cr.BaseVersion &&
		reflect.DeepEqual(out.Proposed, cr.Proposed) && reflect.DeepEqual(out.Base, cr.Base) {
		return cr, false, nil
	}
	out.UpdatedAt, out.Version = now, cr.Version+1
	return out, true, nil
}
