package governance_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	ada      = id.MustParse[access.Member]("0190d2a0-0000-7000-8000-00000000000a")
	bob      = id.MustParse[access.Member]("0190d2a0-0000-7000-8000-00000000000b")
	pipeline = id.MustParse[access.APIToken]("0190d2a0-0000-7000-8000-00000000000c")

	byAda      = governance.Taker{As: governance.Author{Member: &ada}}
	byBob      = governance.Taker{As: governance.Author{Member: &bob}}
	byDecider  = governance.Taker{As: governance.Author{Member: &bob}, MayDecide: true}
	byPipeline = governance.Taker{As: governance.Author{Token: &pipeline}}
	bySystem   = governance.Taker{System: true}
)

func proposal(t *testing.T) governance.NewChangeRequestInput {
	t.Helper()
	return governance.NewChangeRequestInput{
		Subject:  governance.Subject{Kind: governance.KindFlagConfiguration, Base: doc(t, `{"on":false}`), BaseVersion: 3},
		Proposal: governance.Proposal{Title: "  Turn the checkout on  ", Description: " For everyone. ", Proposed: doc(t, `{"on":true}`)},
		Author:   governance.Author{Member: &ada},
		Now:      now,
	}
}

func newChangeRequest(t *testing.T, in governance.NewChangeRequestInput) governance.ChangeRequest {
	t.Helper()
	cr, err := governance.NewChangeRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	return cr
}

func inState(t *testing.T, s governance.ChangeRequestState) governance.ChangeRequest {
	t.Helper()
	cr := newChangeRequest(t, proposal(t))
	cr.State = s
	return cr
}

func codeOf(err error) string {
	if e, ok := apperrors.From(err); ok {
		return e.Code
	}
	return ""
}

func TestAChangeRequestIsDraftedWithItsDiffAtVersionOne(t *testing.T) {
	cr := newChangeRequest(t, proposal(t))
	want := []governance.PatchOperation{{Op: "replace", Path: "/on", Value: true}}
	if cr.State != governance.StateDraft || cr.Version != 1 || cr.Title != "Turn the checkout on" ||
		cr.Description != "For everyone." || !reflect.DeepEqual(cr.Diff, want) || cr.BaseVersion != 3 ||
		!cr.CreatedAt.Equal(now) || !cr.UpdatedAt.Equal(now) {
		t.Errorf("change request = %+v", cr)
	}
	in := proposal(t)
	in.Open = true
	if cr := newChangeRequest(t, in); cr.State != governance.StateOpen {
		t.Errorf("created open: state %s", cr.State)
	}
}

func TestATitleIsRequiredAndBothTextsAreBounded(t *testing.T) {
	for name, tc := range map[string]struct {
		title, description string
		pointers           []string
	}{
		"blank title":          {title: "  ", pointers: []string{"/title"}},
		"title of 201":         {title: strings.Repeat("é", 201), pointers: []string{"/title"}},
		"description of 10001": {title: "t", description: strings.Repeat("d", 10_001), pointers: []string{"/description"}},
		"both":                 {description: strings.Repeat("d", 10_001), pointers: []string{"/title", "/description"}},
	} {
		in := proposal(t)
		in.Title, in.Description = tc.title, tc.description
		_, err := governance.NewChangeRequest(in)
		e, ok := apperrors.From(err)
		if !ok || e.Code != "invalid_change_request" {
			t.Errorf("%s: %v, want invalid_change_request", name, err)
			continue
		}
		var pointers []string
		for _, v := range e.Violations {
			pointers = append(pointers, v.Pointer)
		}
		if !slices.Equal(pointers, tc.pointers) {
			t.Errorf("%s: violations at %v, want %v", name, pointers, tc.pointers)
		}
	}
	in := proposal(t)
	in.Title, in.Description = strings.Repeat("é", 200), strings.Repeat("d", 10_000)
	if _, err := governance.NewChangeRequest(in); err != nil {
		t.Errorf("at the limits: %v", err)
	}
}

// TestTheStateMachine walks every transition from every state for every
// kind of taker against the table of the state machine.
func TestTheStateMachine(t *testing.T) {
	type outcome struct {
		to   governance.ChangeRequestState
		code string
	}
	all := governance.ChangeRequestStates()
	// allowed lists, per transition, the states it is taken from and the
	// state it leaves ("" for the same state).
	allowed := map[governance.Transition]struct {
		from []governance.ChangeRequestState
		to   governance.ChangeRequestState
	}{
		governance.TransitionEdit:      {from: []governance.ChangeRequestState{"draft", "open"}},
		governance.TransitionRebase:    {from: []governance.ChangeRequestState{"draft"}, to: "draft"},
		governance.TransitionOpen:      {from: []governance.ChangeRequestState{"draft"}, to: "open"},
		governance.TransitionCancel:    {from: []governance.ChangeRequestState{"draft", "open", "approved"}, to: "cancelled"},
		governance.TransitionApprove:   {from: []governance.ChangeRequestState{"open"}, to: "approved"},
		governance.TransitionReject:    {from: []governance.ChangeRequestState{"open"}, to: "rejected"},
		governance.TransitionApply:     {from: []governance.ChangeRequestState{"approved"}, to: "applied"},
		governance.TransitionSupersede: {from: []governance.ChangeRequestState{"open", "approved"}, to: "superseded"},
	}
	// refusal is the code who is refused t with whatever the state, ""
	// when they may take it.
	refusal := func(tr governance.Transition, who string) string {
		isAuthor := who == "author" || who == "token author"
		switch tr {
		case governance.TransitionEdit, governance.TransitionRebase, governance.TransitionOpen:
			if !isAuthor {
				return "not_author"
			}
		case governance.TransitionCancel:
			if !isAuthor && who != "decider" {
				return "not_author"
			}
		case governance.TransitionApprove, governance.TransitionReject:
			switch who {
			case "author":
				return "author_cannot_approve"
			case "token author", "token":
				return "token_cannot_approve"
			case "other member":
				return "forbidden"
			case "system":
				return "forbidden"
			}
		case governance.TransitionApply:
			if who == "other member" || who == "token" {
				return "not_author"
			}
		case governance.TransitionSupersede:
			if who != "system" {
				return "forbidden"
			}
		}
		return ""
	}
	tokenAuthored := func(cr governance.ChangeRequest) governance.ChangeRequest {
		cr.Author = governance.Author{Token: &pipeline}
		return cr
	}
	takers := map[string]struct {
		by      governance.Taker
		authors func(governance.ChangeRequest) governance.ChangeRequest
	}{
		"author":       {by: byAda},
		"other member": {by: byBob},
		"decider":      {by: byDecider},
		"token author": {by: byPipeline, authors: tokenAuthored},
		"token":        {by: byPipeline},
		"system":       {by: bySystem},
	}
	for tr, rule := range allowed {
		for _, s := range all {
			for who, tk := range takers {
				cr := inState(t, s)
				if tk.authors != nil {
					cr = tk.authors(cr)
				}
				var want outcome
				switch code := refusal(tr, who); {
				case code != "":
					want.code = code
				case !slices.Contains(rule.from, s):
					want.code = "invalid_state"
				case rule.to == "":
					want.to = s
				default:
					want.to = rule.to
				}
				to, err := cr.Next(tr, tk.by)
				if got := (outcome{to: to, code: codeOf(err)}); got != want {
					t.Errorf("%s from %s by %s = %+v, want %+v", tr, s, who, got, want)
				}
			}
		}
	}
}

func TestAnEditRecomputesTheDiffAndAnEditChangingNothingWritesNothing(t *testing.T) {
	cr := newChangeRequest(t, proposal(t))
	later := now.Add(time.Minute)

	edited, changed, err := cr.Edit(byAda, governance.Edit{
		Title: ptrTo("Turn it on and pin a unit"), Proposed: doc(t, `{"on":true,"overrides":[{"unit_id":"u1"}]}`),
	}, later)
	if err != nil || !changed {
		t.Fatalf("edit = %v, %v", changed, err)
	}
	want := []governance.PatchOperation{
		{Op: "replace", Path: "/on", Value: true},
		{Op: "add", Path: "/overrides", Value: []any{map[string]any{"unit_id": "u1"}}},
	}
	if edited.Version != 2 || !edited.UpdatedAt.Equal(later) || edited.Title != "Turn it on and pin a unit" ||
		edited.Description != "For everyone." || !reflect.DeepEqual(edited.Diff, want) {
		t.Errorf("edited = %+v", edited)
	}

	again, changed, err := edited.Edit(byAda, governance.Edit{
		Title: ptrTo(" Turn it on and pin a unit "), Proposed: doc(t, `{"on":true,"overrides":[{"unit_id":"u1"}]}`),
	}, later.Add(time.Minute))
	if err != nil || changed || again.Version != 2 || !again.UpdatedAt.Equal(later) {
		t.Errorf("the same edit again = %+v, %v, %v; want nothing changed", again, changed, err)
	}

	if _, _, err := cr.Edit(byAda, governance.Edit{Title: ptrTo("")}, later); codeOf(err) != "invalid_change_request" {
		t.Errorf("blank title = %v", err)
	}
	if _, _, err := cr.Edit(byBob, governance.Edit{Title: ptrTo("x")}, later); codeOf(err) != "not_author" {
		t.Errorf("edit by another = %v", err)
	}
}

func TestARebaseMovesTheBaseAndRecomputesTheDiff(t *testing.T) {
	cr := newChangeRequest(t, proposal(t))
	later := now.Add(time.Minute)

	rebased, changed, err := cr.Rebase(byAda, governance.Subject{Base: doc(t, `{"on":true}`), BaseVersion: 4}, later)
	if err != nil || !changed || rebased.BaseVersion != 4 || rebased.Version != 2 || len(rebased.Diff) != 0 ||
		!reflect.DeepEqual(rebased.Proposed, cr.Proposed) {
		t.Errorf("rebased = %+v, %v, %v", rebased, changed, err)
	}
	same, changed, err := rebased.Rebase(byAda, governance.Subject{Base: doc(t, `{"on":true}`), BaseVersion: 4}, later.Add(time.Minute))
	if err != nil || changed || same.Version != 2 {
		t.Errorf("rebasing on the same base = %+v, %v, %v; want nothing changed", same, changed, err)
	}
}

func TestOpeningIsRefusedOnAMovedBase(t *testing.T) {
	cr := newChangeRequest(t, proposal(t))
	later := now.Add(time.Minute)

	_, err := cr.Open(byAda, 4, later)
	if e, ok := apperrors.From(err); !ok || e.Code != "base_moved" || e.Kind != apperrors.KindConflict {
		t.Errorf("open on a moved base = %v, want base_moved", err)
	}
	opened, err := cr.Open(byAda, 3, later)
	if err != nil || opened.State != governance.StateOpen || opened.Version != 2 || !opened.UpdatedAt.Equal(later) {
		t.Errorf("opened = %+v, %v", opened, err)
	}
	// Who may open is answered before the base.
	if _, err := cr.Open(byBob, 4, later); codeOf(err) != "not_author" {
		t.Errorf("open by another on a moved base = %v, want not_author", err)
	}
}

func TestCancelling(t *testing.T) {
	cr := newChangeRequest(t, proposal(t))
	cancelled, err := cr.Cancel(byAda, now.Add(time.Minute))
	if err != nil || cancelled.State != governance.StateCancelled || cancelled.Version != 2 {
		t.Errorf("cancelled = %+v, %v", cancelled, err)
	}
	if _, err := cancelled.Cancel(byAda, now); codeOf(err) != "invalid_state" {
		t.Errorf("cancelling again = %v", err)
	}
	if _, err := cr.Cancel(byBob, now); codeOf(err) != "not_author" {
		t.Errorf("cancel by another = %v", err)
	}
}

func TestAProjectTokenIsTheAuthorOfItsOwnChangeRequestsOnly(t *testing.T) {
	in := proposal(t)
	in.Author = governance.Author{Token: &pipeline}
	cr := newChangeRequest(t, in)
	if _, _, err := cr.Edit(byPipeline, governance.Edit{Title: ptrTo("x")}, now); err != nil {
		t.Errorf("the token editing its own = %v", err)
	}
	if _, _, err := cr.Edit(byAda, governance.Edit{Title: ptrTo("x")}, now); codeOf(err) != "not_author" {
		t.Errorf("a member editing the token's = %v", err)
	}
	other := id.MustParse[access.APIToken]("0190d2a0-0000-7000-8000-00000000000d")
	if _, _, err := cr.Edit(governance.Taker{As: governance.Author{Token: &other}}, governance.Edit{Title: ptrTo("x")}, now); codeOf(err) != "not_author" {
		t.Errorf("another token editing it = %v", err)
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestApprovingRecordsTheDeciderAndAnOptionalComment(t *testing.T) {
	cr := inState(t, governance.StateOpen)
	later := now.Add(time.Minute)

	approved, err := cr.Approve(byDecider, "  Looks right  ", governance.Current{Version: 3}, later)
	want := &governance.Decision{Member: bob, At: later, Comment: "Looks right"}
	if err != nil || approved.State != governance.StateApproved || approved.Version != 2 || !approved.UpdatedAt.Equal(later) ||
		!reflect.DeepEqual(approved.Approval, want) || approved.Rejection != nil {
		t.Errorf("approved = %+v, %v", approved, err)
	}
	silent, err := cr.Approve(byDecider, " ", governance.Current{Version: 3}, later)
	if err != nil || silent.Approval == nil || silent.Approval.Comment != "" {
		t.Errorf("approved without a comment = %+v, %v", silent.Approval, err)
	}
	if _, err = cr.Approve(byDecider, strings.Repeat("é", 2_000), governance.Current{Version: 3}, later); err != nil {
		t.Errorf("a comment at the limit = %v", err)
	}
	_, err = cr.Approve(byDecider, strings.Repeat("é", 2_001), governance.Current{Version: 3}, later)
	if e, ok := apperrors.From(err); !ok || e.Code != "invalid_change_request" || len(e.Violations) != 1 ||
		e.Violations[0].Pointer != "/comment" || e.Violations[0].Code != "too_long" {
		t.Errorf("a comment over the limit = %v", err)
	}
}

func TestRejectingNeedsAComment(t *testing.T) {
	cr := inState(t, governance.StateOpen)
	later := now.Add(time.Minute)

	rejected, err := cr.Reject(byDecider, " Not before the release ", later)
	want := &governance.Decision{Member: bob, At: later, Comment: "Not before the release"}
	if err != nil || rejected.State != governance.StateRejected || rejected.Version != 2 ||
		!reflect.DeepEqual(rejected.Rejection, want) || rejected.Approval != nil {
		t.Errorf("rejected = %+v, %v", rejected, err)
	}
	_, err = cr.Reject(byDecider, "  ", later)
	if e, ok := apperrors.From(err); !ok || e.Code != "invalid_change_request" || len(e.Violations) != 1 ||
		e.Violations[0].Pointer != "/comment" || e.Violations[0].Code != "required" {
		t.Errorf("a blank comment = %v", err)
	}
	if _, err := cr.Reject(byDecider, strings.Repeat("d", 2_001), later); codeOf(err) != "invalid_change_request" {
		t.Errorf("a comment over the limit = %v", err)
	}
	// Who may decide is answered before the comment.
	if _, err := cr.Reject(byAda, "", later); codeOf(err) != "author_cannot_approve" {
		t.Errorf("the author rejecting without a comment = %v", err)
	}
}

func TestApplyingRecordsWhoAppliedAndWhen(t *testing.T) {
	cr := inState(t, governance.StateApproved)
	later := now.Add(time.Minute)
	for name, tc := range map[string]struct {
		by   governance.Taker
		want governance.Application
	}{
		"the author":         {by: byAda, want: governance.Application{At: later, By: byAda.As}},
		"a decider":          {by: byDecider, want: governance.Application{At: later, By: byDecider.As}},
		"the system":         {by: bySystem, want: governance.Application{At: later, System: true}},
		"the token as maker": {by: byPipeline, want: governance.Application{At: later, By: byPipeline.As}},
	} {
		applying := cr
		if name == "the token as maker" {
			applying.Author = governance.Author{Token: &pipeline}
		}
		applied, err := applying.Apply(tc.by, governance.Current{Version: 3}, later)
		if err != nil || applied.State != governance.StateApplied || applied.Version != 2 || applied.Applied == nil ||
			!reflect.DeepEqual(*applied.Applied, tc.want) {
			t.Errorf("applied by %s = %+v, %v", name, applied.Applied, err)
		}
	}
	if _, err := cr.Apply(byBob, governance.Current{Version: 3}, later); codeOf(err) != "not_author" {
		t.Errorf("applied by another member = %v", err)
	}
	// Whoever approved it applies it, even once they may no longer decide.
	approvedByBob := cr
	approvedByBob.Approval = &governance.Decision{Member: bob, At: now}
	if applied, err := approvedByBob.Apply(byBob, governance.Current{Version: 3}, later); err != nil || applied.State != governance.StateApplied {
		t.Errorf("applied by its approver = %v", err)
	}
}

// Approving or applying a change request whose base moved would decide or
// write what nobody reviewed: it is answered base_moved, and the change
// request is left superseded for the caller to store.
func TestApprovingOrApplyingOnAMovedBaseSupersedes(t *testing.T) {
	later := now.Add(time.Minute)
	for name, decide := range map[string]func(governance.ChangeRequest) (governance.ChangeRequest, error){
		"approve": func(cr governance.ChangeRequest) (governance.ChangeRequest, error) {
			return cr.Approve(byDecider, "", governance.Current{Version: 4}, later)
		},
		"apply": func(cr governance.ChangeRequest) (governance.ChangeRequest, error) {
			return cr.Apply(byAda, governance.Current{Version: 4}, later)
		},
	} {
		from := governance.StateOpen
		if name == "apply" {
			from = governance.StateApproved
		}
		superseded, err := decide(inState(t, from))
		if e, ok := apperrors.From(err); !ok || e.Code != "base_moved" || e.Kind != apperrors.KindConflict {
			t.Errorf("%s on a moved base = %v, want base_moved", name, err)
		}
		if superseded.State != governance.StateSuperseded || superseded.Version != 2 || !superseded.UpdatedAt.Equal(later) ||
			superseded.Approval != nil && name == "approve" || superseded.Applied != nil {
			t.Errorf("%s on a moved base left %+v, want it superseded", name, superseded)
		}
	}
	// Who may act and the state are answered before the base.
	if _, err := inState(t, governance.StateOpen).Approve(byAda, "", governance.Current{Version: 4}, later); codeOf(err) != "author_cannot_approve" {
		t.Errorf("the author approving on a moved base = %v", err)
	}
	if _, err := inState(t, governance.StateDraft).Apply(byAda, governance.Current{Version: 4}, later); codeOf(err) != "invalid_state" {
		t.Errorf("applying a draft on a moved base = %v", err)
	}
}

// A definition is applied in every environment: its approval covers the
// environments protected when it was given, and an environment protected
// since was never approved to change, so applying it then supersedes it.
func TestADefinitionsApprovalCoversTheEnvironmentsProtectedThen(t *testing.T) {
	production := id.MustParse[workspace.Environment]("0190d2a0-0000-7000-8000-000000000e01")
	staging := id.MustParse[workspace.Environment]("0190d2a0-0000-7000-8000-000000000e02")
	later := now.Add(time.Minute)
	cr := inState(t, governance.StateOpen)
	cr.Kind, cr.Environment = governance.KindDefinition, nil

	approved, err := cr.Approve(byDecider, "", governance.Current{Version: 3, Protected: []workspace.EnvironmentID{production}}, later)
	if err != nil || !slices.Equal(approved.ApprovalCovers, []workspace.EnvironmentID{production}) ||
		!slices.Equal(approved.Covers(), []workspace.EnvironmentID{production}) {
		t.Fatalf("approved = %+v, %v; want covering production", approved, err)
	}
	// An environment no longer protected needs no cover.
	if applied, applyErr := approved.Apply(byAda, governance.Current{Version: 3}, later); applyErr != nil || applied.State != governance.StateApplied {
		t.Errorf("applied once production is unprotected = %+v, %v", applied, applyErr)
	}
	superseded, err := approved.Apply(byAda, governance.Current{Version: 3, Protected: []workspace.EnvironmentID{production, staging}}, later)
	if codeOf(err) != "base_moved" || superseded.State != governance.StateSuperseded || superseded.Applied != nil {
		t.Errorf("applied once staging is protected = %+v, %v; want base_moved and superseded", superseded, err)
	}

	// A configuration's change request covers its environment, whatever is
	// protected.
	configuration := inState(t, governance.StateOpen)
	configuration.Environment = &governance.EnvironmentRef{ID: staging, Key: "staging"}
	approvedConfiguration, err := configuration.Approve(byDecider, "", governance.Current{Version: 3, Protected: []workspace.EnvironmentID{production}}, later)
	if err != nil || approvedConfiguration.ApprovalCovers != nil || !slices.Equal(approvedConfiguration.Covers(), []workspace.EnvironmentID{staging}) {
		t.Errorf("approved configuration = %+v, %v; want covering staging", approvedConfiguration, err)
	}
	if _, err := approvedConfiguration.Apply(byAda, governance.Current{Version: 3, Protected: []workspace.EnvironmentID{production, staging}}, later); err != nil {
		t.Errorf("applying the configuration = %v", err)
	}
}

// Rebasing a segment's definition takes the flags naming it now with the
// segment as it is now.
func TestARebaseTakesTheReferrersOfTheNewBase(t *testing.T) {
	checkout := id.MustParse[flags.Flag]("0190d2a0-0000-7000-8000-000000000f01")
	cr := newChangeRequest(t, proposal(t))
	rebased, changed, err := cr.Rebase(byAda, governance.Subject{Base: cr.Base, BaseVersion: cr.BaseVersion,
		BaseReferrers: []flags.FlagID{checkout}}, now)
	if err != nil || !changed || !slices.Equal(rebased.BaseReferrers, []flags.FlagID{checkout}) {
		t.Errorf("rebased = %+v, %v, %v; want checkout as its referrer", rebased, changed, err)
	}
}

func TestAnApplyTimeIsBetweenTheLeadAndAYearAhead(t *testing.T) {
	lead := time.Minute
	for name, tc := range map[string]struct {
		at   time.Time
		code string
	}{
		"in the past":          {at: now.Add(-time.Second), code: "too_soon"},
		"inside the lead":      {at: now.Add(lead - time.Microsecond), code: "too_soon"},
		"at the lead":          {at: now.Add(lead)},
		"a year ahead":         {at: now.Add(governance.MaxScheduleLead)},
		"more than a year":     {at: now.Add(governance.MaxScheduleLead + time.Microsecond), code: "too_late"},
		"in another time zone": {at: now.Add(time.Hour).In(time.FixedZone("UTC+3", 3*3600))},
	} {
		in := proposal(t)
		in.ApplyAt, in.MinLead = &tc.at, lead
		cr, err := governance.NewChangeRequest(in)
		if tc.code != "" {
			e, ok := apperrors.From(err)
			if !ok || e.Code != "invalid_change_request" || len(e.Violations) != 1 || e.Violations[0].Pointer != "/apply_at" ||
				e.Violations[0].Code != tc.code {
				t.Errorf("%s: %v, want /apply_at %s", name, err, tc.code)
			}
			continue
		}
		if err != nil || cr.ApplyAt == nil || !cr.ApplyAt.Equal(tc.at) || cr.ApplyAt.Location() != time.UTC {
			t.Errorf("%s: apply time %v, %v; want %v in UTC", name, cr.ApplyAt, err, tc.at)
		}
	}
	if cr := newChangeRequest(t, proposal(t)); cr.ApplyAt != nil {
		t.Errorf("unscheduled change request has apply time %v", cr.ApplyAt)
	}
}

func TestAnEditSetsOrClearsTheApplyTime(t *testing.T) {
	cr := newChangeRequest(t, proposal(t))
	later := now.Add(time.Minute)
	at := later.Add(time.Hour)
	scheduled, changed, err := cr.Edit(byAda, governance.Edit{Schedule: &governance.ScheduleEdit{At: &at}, MinLead: time.Minute}, later)
	if err != nil || !changed || scheduled.ApplyAt == nil || !scheduled.ApplyAt.Equal(at) || scheduled.Version != 2 {
		t.Fatalf("scheduled = %+v, %v, %v", scheduled.ApplyAt, changed, err)
	}
	if _, again, againErr := scheduled.Edit(byAda, governance.Edit{Schedule: &governance.ScheduleEdit{At: &at}}, later); againErr != nil || again {
		t.Errorf("the same apply time again: changed %v, %v", again, againErr)
	}
	// An apply time sent back unchanged is not judged again, though it is
	// now inside the lead.
	near := at.Add(-30 * time.Second)
	if kept, again, againErr := scheduled.Edit(byAda, governance.Edit{Title: ptrTo("Other"),
		Schedule: &governance.ScheduleEdit{At: &at}, MinLead: time.Minute}, near); againErr != nil || !again ||
		kept.ApplyAt == nil || !kept.ApplyAt.Equal(at) {
		t.Errorf("the same apply time inside the lead: %v, changed %v, %v", kept.ApplyAt, again, againErr)
	}
	// The lead is judged from the edit, not from when the change request
	// was created.
	soon := later.Add(30 * time.Second)
	if _, _, soonErr := scheduled.Edit(byAda, governance.Edit{Schedule: &governance.ScheduleEdit{At: &soon}, MinLead: time.Minute},
		later); codeOf(soonErr) != "invalid_change_request" {
		t.Errorf("an apply time inside the lead = %v", soonErr)
	}
	cleared, changed, err := scheduled.Edit(byAda, governance.Edit{Schedule: &governance.ScheduleEdit{}}, later)
	if err != nil || !changed || cleared.ApplyAt != nil || cleared.Version != 3 {
		t.Errorf("cleared = %+v, %v, %v", cleared.ApplyAt, changed, err)
	}
	if kept, changed, err := scheduled.Edit(byAda, governance.Edit{Title: ptrTo("Other")}, later); err != nil || !changed ||
		kept.ApplyAt == nil || !kept.ApplyAt.Equal(at) {
		t.Errorf("an edit of the title left the apply time %v, %v", kept.ApplyAt, err)
	}
}

// scheduledApproved is an approved change request to be applied at at.
func scheduledApproved(t *testing.T, at time.Time) governance.ChangeRequest {
	t.Helper()
	cr := inState(t, governance.StateApproved)
	cr.ApplyAt = &at
	return cr
}

func TestAScheduledChangeRequestIsAppliedByTheSystemAlone(t *testing.T) {
	at := now.Add(time.Hour)
	cr := scheduledApproved(t, at)
	for name, by := range map[string]governance.Taker{"the author": byAda, "a decider": byDecider} {
		_, err := cr.Apply(by, governance.Current{Version: 3}, at)
		if e, ok := apperrors.From(err); !ok || e.Code != "scheduled" || e.Kind != apperrors.KindConflict {
			t.Errorf("applied by %s = %v, want scheduled", name, err)
		}
	}
	// Who may act is answered first.
	if _, err := cr.Apply(byBob, governance.Current{Version: 3}, at); codeOf(err) != "not_author" {
		t.Errorf("applied by another member = %v", err)
	}
	if applied, err := cr.Apply(bySystem, governance.Current{Version: 3}, at); err != nil || applied.State != governance.StateApplied {
		t.Errorf("applied by the system = %v", err)
	}
}

func TestAScheduledChangeRequestIsDueAtItsTimeUntilItsFailuresStopIt(t *testing.T) {
	at := now.Add(time.Hour)
	cr := scheduledApproved(t, at)
	if cr.Due(at.Add(-time.Microsecond)) || !cr.Due(at) || !cr.Due(at.Add(24*time.Hour)) {
		t.Errorf("due before, at and long after its time = %v %v %v", cr.Due(at.Add(-time.Microsecond)), cr.Due(at),
			cr.Due(at.Add(24*time.Hour)))
	}
	if inState(t, governance.StateApproved).Due(at) {
		t.Error("an unscheduled change request is due")
	}
	for _, s := range []governance.ChangeRequestState{governance.StateOpen, governance.StateCancelled, governance.StateApplied} {
		other := cr
		other.State = s
		if other.Due(at) {
			t.Errorf("a %s change request is due", s)
		}
	}
	failing := cr
	for n := 1; n <= 3; n++ {
		failedAt := at.Add(time.Duration(n) * time.Second)
		failing = failing.FailSchedule("invalid_configuration", "the gate is archived", failedAt, 3)
		want := governance.ScheduleFailure{At: failedAt, Code: "invalid_configuration", Message: "the gate is archived",
			Attempts: n, Stopped: n == 3}
		if failing.ScheduleFailure == nil || *failing.ScheduleFailure != want {
			t.Errorf("after %d failures: %+v, want %+v", n, failing.ScheduleFailure, want)
		}
		if failing.Version != cr.Version || !failing.UpdatedAt.Equal(cr.UpdatedAt) || failing.State != governance.StateApproved {
			t.Errorf("a failure moved the change request: %+v", failing)
		}
		if due := failing.Due(failedAt); due != (n < 3) {
			t.Errorf("due after %d failures = %v", n, due)
		}
	}
}

func TestAScheduleFailureIsClearedOnApplicationAndCancellationAndKeptOnSupersede(t *testing.T) {
	at := now.Add(time.Hour)
	failed := scheduledApproved(t, at).FailSchedule("invalid_configuration", "no", at, 5)
	if applied, err := failed.Apply(bySystem, governance.Current{Version: 3}, at.Add(time.Second)); err != nil || applied.ScheduleFailure != nil {
		t.Errorf("applied = %+v, %v; want the failure cleared", applied.ScheduleFailure, err)
	}
	if cancelled, err := failed.Cancel(byAda, at.Add(time.Second)); err != nil || cancelled.ScheduleFailure != nil {
		t.Errorf("cancelled = %+v, %v; want the failure cleared", cancelled.ScheduleFailure, err)
	}
	if superseded, _ := failed.Apply(bySystem, governance.Current{Version: 4}, at.Add(time.Second)); superseded.State != governance.StateSuperseded ||
		superseded.ScheduleFailure == nil || superseded.ScheduleFailure.Attempts != 1 {
		t.Errorf("superseded = %+v, want the failure kept", superseded.ScheduleFailure)
	}
	if superseded, err := failed.Supersede(governance.EntryID{}, at.Add(time.Second)); err != nil ||
		superseded.ScheduleFailure == nil {
		t.Errorf("superseded by a write = %+v, %v; want the failure kept", superseded.ScheduleFailure, err)
	}
}
