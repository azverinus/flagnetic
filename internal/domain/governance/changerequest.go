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
	// Segment names the segment of a segment's definition.
	Segment *SegmentRef
	// Base is the document of the entity as the change request was based
	// on it, and BaseVersion that document's version.
	Base        Document
	BaseVersion int64
	// BaseReferrers are, for a segment's definition, the flags whose
	// configurations named the segment in some environment when the change
	// request was based on it. A write to one of their configurations moves
	// what the definition was reviewed with, even a write that stops naming
	// the segment.
	BaseReferrers []flags.FlagID
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

// SegmentRef names a segment by identifier, and by the key it had when
// read.
type SegmentRef struct {
	ID  flags.SegmentID
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
	// RevertTo is, for a revert, the version of its configuration it
	// returns to, nil for any other change request. The server fills
	// Proposed from it and nobody edits that: whoever may propose a revert
	// cannot slip any other change in under its name.
	RevertTo *int64
	Author   Author
	// Approval and Rejection are the decision taken on the change request,
	// at most one of them; Applied is its application.
	Approval  *Decision
	Rejection *Decision
	Applied   *Application
	// ApprovalCovers are, for a change request applied in every
	// environment, the environments protected when it was approved: its
	// application changes them although they are protected, and no other.
	ApprovalCovers []workspace.EnvironmentID
	// SupersededBy is the audit entry of the write that moved what the
	// change request was based on and so superseded it; zero when no write
	// did, as when it was found moved only on approval or application.
	SupersededBy EntryID
	// ApplyAt is when the system applies the change request once it is
	// approved, nil when it is applied by hand; ScheduleFailure is why the
	// last scheduled application failed, nil when none did.
	ApplyAt         *time.Time
	ScheduleFailure *ScheduleFailure
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Version         int64
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

// MaxScheduleLead is how far ahead a change request may be scheduled: a
// release planned further out is proposed closer to its time, against what
// the entity is then.
const MaxScheduleLead = 365 * 24 * time.Hour

// ScheduleFailure is why the system failed to apply a scheduled change
// request: when it last failed, the code and message of that refusal, how
// many attempts in a row failed, and whether the system stopped trying.
type ScheduleFailure struct {
	At       time.Time
	Code     string
	Message  string
	Attempts int
	Stopped  bool
}

// checkApplyAt returns at in UTC to the microsecond the database keeps, or
// the violation of an apply time sooner than minLead from now or further
// than MaxScheduleLead. A time inside the lead could pass before anyone
// approves the change request, which would then apply the moment it is
// approved, unreviewed by the clock it named.
func checkApplyAt(at, now time.Time, minLead time.Duration) (time.Time, *apperrors.Violation) {
	at = at.UTC().Truncate(time.Microsecond)
	switch lead := at.Sub(now); {
	case lead < minLead:
		return at, &apperrors.Violation{
			Pointer: "/apply_at", Code: "too_soon", Detail: "an apply time is at least " + minLead.String() + " ahead",
			Params: map[string]any{"min_lead_seconds": int64(minLead / time.Second)},
		}
	case lead > MaxScheduleLead:
		return at, &apperrors.Violation{
			Pointer: "/apply_at", Code: "too_late", Detail: "an apply time is at most 365 days ahead",
			Params: map[string]any{"max_days": int64(MaxScheduleLead / (24 * time.Hour))},
		}
	}
	return at, nil
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
	// RevertTo is the version a revert returns to, Proposed filled from
	// it; nil for any other change request.
	RevertTo *int64
	// Open asks for the change request to be opened for review at once
	// rather than drafted.
	Open   bool
	Author Author
	// ApplyAt schedules the change request, nil for none; MinLead is how
	// far ahead of Now it must be at least.
	ApplyAt *time.Time
	MinLead time.Duration
	Now     time.Time
}

// NewChangeRequest returns the change request in describes, drafted or
// opened, at version 1; or invalid_change_request naming every field to fix.
func NewChangeRequest(in NewChangeRequestInput) (ChangeRequest, error) {
	title, description, err := checkProposal(in.Proposal)
	var applyAt *time.Time
	if in.ApplyAt != nil {
		at, violation := checkApplyAt(*in.ApplyAt, in.Now, in.MinLead)
		applyAt = &at
		err = withViolation(err, violation)
	}
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
		Proposed: in.Proposed, Diff: diff, RevertTo: in.RevertTo, Author: in.Author, ApplyAt: applyAt, CreatedAt: in.Now,
		UpdatedAt: in.Now, Version: 1,
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

// withViolation returns err, an invalid_change_request or nil, with v added
// to its violations when v is not nil, so every field to fix is named at
// once.
func withViolation(err error, v *apperrors.Violation) error {
	if v == nil {
		return err
	}
	violations := []apperrors.Violation{}
	if e, ok := apperrors.From(err); ok {
		violations = append(violations, e.Violations...)
	} else if err != nil {
		return err
	}
	violations = append(violations, *v)
	return apperrors.Invalid("invalid_change_request", "the change request is not valid", violations...)
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
	// byAuthorDeciderOrSystem: the author, the member who approved it,
	// whoever may decide, or the system applying at the scheduled time.
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
	if err := rule.who.allows(cr, by); err != nil {
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

func (w who) allows(cr ChangeRequest, by Taker) error {
	isAuthor := !by.System && cr.Author.Is(by.As)
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
		// The member who approved it applies what they approved, even
		// once they may no longer decide others.
		isApprover := !by.System && cr.Approval != nil && by.As.Member != nil && *by.As.Member == cr.Approval.Member
		if !isAuthor && !isApprover && !by.System && !by.MayDecide {
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
	// Schedule sets or clears the apply time; nil leaves it as it is.
	// MinLead is how far ahead of the edit a new apply time must be.
	Schedule *ScheduleEdit
	MinLead  time.Duration
}

// ScheduleEdit is the apply time an edit leaves: At, or none when nil.
type ScheduleEdit struct{ At *time.Time }

// Edit returns cr edited by by at now, its diff recomputed against its base,
// its version incremented, and whether anything changed; cr itself when
// nothing did, so an edit that changes nothing writes nothing. Only the
// author edits, a draft or an open change request.
func (cr ChangeRequest) Edit(by Taker, e Edit, now time.Time) (ChangeRequest, bool, error) {
	if _, err := cr.Next(TransitionEdit, by); err != nil {
		return cr, false, err
	}
	if cr.RevertTo != nil && e.Proposed != nil {
		return cr, false, errRevertProposed
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
	out := cr
	if e.Schedule != nil {
		out.ApplyAt = nil
		switch at := e.Schedule.At; {
		case at != nil && cr.ApplyAt != nil && at.Equal(*cr.ApplyAt):
			// The apply time sent back unchanged is not set again: a
			// client resending the whole change request with an edit of
			// its title is not refused because its time drew near.
			out.ApplyAt = cr.ApplyAt
		case at != nil:
			checked, violation := checkApplyAt(*at, now, e.MinLead)
			out.ApplyAt, err = &checked, withViolation(err, violation)
		}
	}
	if err != nil {
		return cr, false, err
	}
	out.Title, out.Description, out.Proposed = title, description, p.Proposed
	out.Diff = Diff(cr.Base, p.Proposed)
	return cr.changedTo(out, now)
}

// Rebase returns cr based on current, the entity as it is now, its diff
// recomputed from it to what cr proposes, and whether anything changed. Only
// the author rebases, and only a draft: an open change request is under
// review against the diff its reviewers see.
func (cr ChangeRequest) Rebase(by Taker, current Subject, now time.Time) (ChangeRequest, bool, error) {
	if _, err := cr.Next(TransitionRebase, by); err != nil {
		return cr, false, err
	}
	out := cr
	out.Base, out.BaseVersion, out.BaseReferrers = current.Base, current.BaseVersion, current.BaseReferrers
	out.Diff = Diff(current.Base, cr.Proposed)
	return cr.changedTo(out, now)
}

// RebaseRevert returns cr, a revert, based on current, the configuration as
// it is now, proposing proposed, the version it returns to as reverting
// current to it would write it now, its diff recomputed, and whether
// anything changed. A revert's proposal is the server's, so a rebase fills
// it again from the same target rather than keeping it. Only the author
// rebases, and only a draft.
func (cr ChangeRequest) RebaseRevert(by Taker, current Subject, proposed Document, now time.Time) (ChangeRequest, bool, error) {
	if _, err := cr.Next(TransitionRebase, by); err != nil {
		return cr, false, err
	}
	out := cr
	out.Base, out.BaseVersion, out.Proposed = current.Base, current.BaseVersion, proposed
	out.Diff = Diff(current.Base, proposed)
	return cr.changedTo(out, now)
}

// errRevertProposed refuses an edit of what a revert proposes, which the
// server fills from the version it returns to.
var errRevertProposed = apperrors.Invalid("invalid_change_request", "the change request is not valid", apperrors.Violation{
	Pointer: "/proposed", Code: "not_allowed", Detail: "a revert proposes the version it returns to, which nobody edits",
})

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
	out := cr.moved(state, now)
	// Nothing will try to apply it again.
	out.ScheduleFailure = nil
	return out, nil
}

// Current is what a change request is judged against when it is approved
// or applied: the version of its entity now, and the environments of its
// project protected now.
type Current struct {
	Version   int64
	Protected []workspace.EnvironmentID
}

// Approve returns cr approved by by at now with comment, which is optional;
// a change request applied in every environment records the environments
// protected now as those its approval covers. When the entity's version now
// is not the one cr was based on, what would be approved is no longer what
// was reviewed, and Approve answers base_moved with cr superseded, which
// the caller stores although it refuses the approval.
func (cr ChangeRequest) Approve(by Taker, comment string, current Current, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionApprove, by)
	if err != nil {
		return cr, err
	}
	comment, err = checkComment(comment, false)
	if err != nil {
		return cr, err
	}
	if current.Version != cr.BaseVersion {
		return cr.moved(StateSuperseded, now), baseMoved(cr.BaseVersion, current.Version)
	}
	out := cr.moved(state, now)
	out.Approval = &Decision{Member: *by.As.Member, At: now, Comment: comment}
	if cr.Kind != KindFlagConfiguration {
		out.ApprovalCovers = slices.Clone(current.Protected)
	}
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

// Apply returns cr applied by by at now. When the entity's version now is
// not the one cr was based on, applying it would write over a change nobody
// who approved it saw; when an environment protected now was not when cr
// was approved, applying it would change that environment without anyone
// having approved changing it. Either way Apply answers base_moved with cr
// superseded, which the caller stores although it refuses the application.
func (cr ChangeRequest) Apply(by Taker, current Current, now time.Time) (ChangeRequest, error) {
	state, err := cr.Next(TransitionApply, by)
	if err != nil {
		return cr, err
	}
	if cr.ApplyAt != nil && !by.System {
		return cr, errScheduled
	}
	if current.Version != cr.BaseVersion {
		return cr.moved(StateSuperseded, now), baseMoved(cr.BaseVersion, current.Version)
	}
	if cr.Kind != KindFlagConfiguration {
		for _, e := range current.Protected {
			if !slices.Contains(cr.ApprovalCovers, e) {
				return cr.moved(StateSuperseded, now), apperrors.Conflict("base_moved",
					"an environment was protected since the change request was approved, so its approval does not cover it; "+
						"copy it to propose it again")
			}
		}
	}
	out := cr.moved(state, now)
	out.ScheduleFailure = nil
	out.Applied = &Application{At: now, System: by.System}
	if !by.System {
		out.Applied.By = by.As
	}
	return out, nil
}

// Covers returns the environments applying cr may change although they are
// protected: a configuration's environment, whose protection is why it was
// proposed; or, for a change request applied in every environment, those
// protected when it was approved.
func (cr ChangeRequest) Covers() []workspace.EnvironmentID {
	if cr.Kind == KindFlagConfiguration && cr.Environment != nil {
		return []workspace.EnvironmentID{cr.Environment.ID}
	}
	return cr.ApprovalCovers
}

// errScheduled refuses applying a scheduled change request by hand: the
// schedule is the one path, so what was approved goes live when it said.
var errScheduled = apperrors.Conflict("scheduled",
	"the change request is applied by the system at its apply time; cancel it and copy it to apply it now")

// Due reports whether the system applies cr at now: it is approved, its
// apply time has come, late included, and its failures have not stopped
// the system trying.
func (cr ChangeRequest) Due(now time.Time) bool {
	return cr.State == StateApproved && cr.ApplyAt != nil && !cr.ApplyAt.After(now) &&
		(cr.ScheduleFailure == nil || !cr.ScheduleFailure.Stopped)
}

// FailSchedule returns cr with a failed scheduled application recorded: at
// now, refused with code and message, one more attempt in a row, stopped
// once attempts reach maxFailures, so a change that keeps failing is not
// tried forever. Its state, version and update time stay: nothing about
// what it proposes or who decided it changed.
func (cr ChangeRequest) FailSchedule(code, message string, now time.Time, maxFailures int) ChangeRequest {
	attempts := 1
	if cr.ScheduleFailure != nil {
		attempts = cr.ScheduleFailure.Attempts + 1
	}
	cr.ScheduleFailure = &ScheduleFailure{
		At: now, Code: code, Message: message, Attempts: attempts, Stopped: attempts >= maxFailures,
	}
	return cr
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
	sameSchedule := (out.ApplyAt == nil) == (cr.ApplyAt == nil) && (out.ApplyAt == nil || out.ApplyAt.Equal(*cr.ApplyAt))
	if sameSchedule && out.Title == cr.Title && out.Description == cr.Description && out.BaseVersion == cr.BaseVersion &&
		reflect.DeepEqual(out.Proposed, cr.Proposed) && reflect.DeepEqual(out.Base, cr.Base) &&
		slices.Equal(out.BaseReferrers, cr.BaseReferrers) {
		return cr, false, nil
	}
	out.UpdatedAt, out.Version = now, cr.Version+1
	return out, true, nil
}

// Waiting is how many change requests wait on one configuration: open, under
// review, and approved, ready to be applied; and the earliest time one of
// them is scheduled to be applied, nil when none is.
type Waiting struct {
	Open        int
	Approved    int
	NextApplyAt *time.Time
}
