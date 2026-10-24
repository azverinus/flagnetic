package governance_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
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

	rebased, changed, err := cr.Rebase(byAda, doc(t, `{"on":true}`), 4, later)
	if err != nil || !changed || rebased.BaseVersion != 4 || rebased.Version != 2 || len(rebased.Diff) != 0 ||
		!reflect.DeepEqual(rebased.Proposed, cr.Proposed) {
		t.Errorf("rebased = %+v, %v, %v", rebased, changed, err)
	}
	same, changed, err := rebased.Rebase(byAda, doc(t, `{"on":true}`), 4, later.Add(time.Minute))
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
