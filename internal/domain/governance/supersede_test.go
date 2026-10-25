package governance_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	checkout   = id.MustParse[flags.Flag]("0190d2a0-0000-7000-8000-000000000f01")
	banner     = id.MustParse[flags.Flag]("0190d2a0-0000-7000-8000-000000000f02")
	production = id.MustParse[workspace.Environment]("0190d2a0-0000-7000-8000-000000000e01")
	staging    = id.MustParse[workspace.Environment]("0190d2a0-0000-7000-8000-000000000e02")
	moving     = id.MustParse[governance.Entry]("0190d2a0-0000-7000-8000-0000000000a1")
)

// configurationRequest returns a change request in state proposing flag's
// configuration in environment.
func configurationRequest(t *testing.T, n byte, flag flags.FlagID, environment workspace.EnvironmentID,
	state governance.ChangeRequestState,
) governance.ChangeRequest {
	t.Helper()
	cr := inState(t, state)
	cr.ID = id.FromBytes[governance.ChangeRequest]([16]byte{0x01, 0x90, 15: n})
	cr.Flag = &governance.FlagRef{ID: flag, Key: "flag"}
	cr.Environment = &governance.EnvironmentRef{ID: environment, Key: "environment"}
	return cr
}

func flagEntry(action string, flag flags.FlagID, environment workspace.EnvironmentID) governance.Entry {
	return governance.Entry{ID: moving, OccurredAt: now, Record: governance.Record{
		Environment: environment, Action: action, Entity: governance.Entity{Kind: "flag", ID: flag.String(), Key: "flag"},
	}}
}

func idsOf(superseded []governance.Superseded) []governance.ChangeRequestID {
	out := []governance.ChangeRequestID{}
	for _, s := range superseded {
		if s.After.ID != s.Before.ID || s.After.State != governance.StateSuperseded || s.After.SupersededBy != moving {
			panic("a change request superseded by another entry")
		}
		out = append(out, s.Before.ID)
	}
	return out
}

func TestAnEntryMovesTheWaitingChangeRequestsOnWhatItChanged(t *testing.T) {
	onCheckoutProduction := configurationRequest(t, 1, checkout, production, governance.StateOpen)
	approvedCheckoutProduction := configurationRequest(t, 2, checkout, production, governance.StateApproved)
	onCheckoutStaging := configurationRequest(t, 3, checkout, staging, governance.StateOpen)
	onBannerProduction := configurationRequest(t, 4, banner, production, governance.StateApproved)
	candidates := []governance.ChangeRequest{onCheckoutProduction, approvedCheckoutProduction, onCheckoutStaging, onBannerProduction}

	for _, tc := range []struct {
		name  string
		entry governance.Entry
		want  []governance.ChangeRequestID
	}{
		{"a direct write", flagEntry("flag.configuration_updated", checkout, production),
			[]governance.ChangeRequestID{onCheckoutProduction.ID, approvedCheckoutProduction.ID}},
		{"switching on", flagEntry("flag.switched_on", checkout, production),
			[]governance.ChangeRequestID{onCheckoutProduction.ID, approvedCheckoutProduction.ID}},
		{"the kill switch", flagEntry("flag.killed", checkout, staging), []governance.ChangeRequestID{onCheckoutStaging.ID}},
		{"an override set", flagEntry("flag.override_set", checkout, staging), []governance.ChangeRequestID{onCheckoutStaging.ID}},
		{"an override removed", flagEntry("flag.override_removed", banner, production),
			[]governance.ChangeRequestID{onBannerProduction.ID}},
		{"a salt regenerated", flagEntry("flag.salt_regenerated", banner, production),
			[]governance.ChangeRequestID{onBannerProduction.ID}},
		{"a copy", flagEntry("flag.configuration_copied", checkout, staging), []governance.ChangeRequestID{onCheckoutStaging.ID}},
		{"a revert", flagEntry("flag.reverted", checkout, staging), []governance.ChangeRequestID{onCheckoutStaging.ID}},
		{"archiving the flag", flagEntry("flag.archived", checkout, workspace.EnvironmentID{}),
			[]governance.ChangeRequestID{onCheckoutProduction.ID, approvedCheckoutProduction.ID, onCheckoutStaging.ID}},
		{"deleting the flag", flagEntry("flag.deleted", banner, workspace.EnvironmentID{}),
			[]governance.ChangeRequestID{onBannerProduction.ID}},
		{"deleting the environment", governance.Entry{ID: moving, Record: governance.Record{
			Environment: production, Action: "environment.deleted",
			Entity: governance.Entity{Kind: "environment", ID: production.String(), Key: "production"},
		}}, []governance.ChangeRequestID{onCheckoutProduction.ID, approvedCheckoutProduction.ID, onBannerProduction.ID}},
		{"a definition edit", flagEntry("flag.updated", checkout, workspace.EnvironmentID{}), []governance.ChangeRequestID{}},
		{"restoring the flag", flagEntry("flag.restored", checkout, workspace.EnvironmentID{}), []governance.ChangeRequestID{}},
		{"updating the environment", governance.Entry{ID: moving, Record: governance.Record{
			Environment: production, Action: "environment.updated",
			Entity: governance.Entity{Kind: "environment", ID: production.String(), Key: "production"},
		}}, []governance.ChangeRequestID{}},
		{"another entity's entry in the environment", governance.Entry{ID: moving, Record: governance.Record{
			Environment: production, Action: "sdk_key.created", Entity: governance.Entity{Kind: "sdk_key", ID: "k", Key: "k"},
		}}, []governance.ChangeRequestID{}},
		{"a change request's own entry", governance.Entry{ID: moving, Record: governance.Record{
			Environment: production, Action: "change_request.applied",
			Entity: governance.Entity{Kind: "change_request", ID: onCheckoutProduction.ID.String(), Key: "t"},
		}}, []governance.ChangeRequestID{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := idsOf(governance.Supersedes(tc.entry, candidates)); !slices.Equal(got, tc.want) {
				t.Errorf("Supersedes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOnlyOpenAndApprovedChangeRequestsAreSuperseded(t *testing.T) {
	var candidates []governance.ChangeRequest
	for i, s := range governance.ChangeRequestStates() {
		candidates = append(candidates, configurationRequest(t, byte(i+1), checkout, production, s))
	}
	got := governance.Supersedes(flagEntry("flag.killed", checkout, production), candidates)
	var states []governance.ChangeRequestState
	for _, s := range got {
		states = append(states, s.Before.State)
	}
	if want := []governance.ChangeRequestState{governance.StateOpen, governance.StateApproved}; !slices.Equal(states, want) {
		t.Errorf("superseded states = %v, want %v", states, want)
	}
}

func TestTheChangeRequestBeingAppliedIsNotSupersededByItsOwnWrite(t *testing.T) {
	applying := configurationRequest(t, 1, checkout, production, governance.StateApproved)
	other := configurationRequest(t, 2, checkout, production, governance.StateApproved)
	e := flagEntry("flag.switched_on", checkout, production)
	e.ChangeRequest = applying.ID
	if got := idsOf(governance.Supersedes(e, []governance.ChangeRequest{applying, other})); !slices.Equal(got, []governance.ChangeRequestID{other.ID}) {
		t.Errorf("Supersedes = %v, want only %v", got, other.ID)
	}
}

func TestWhatAnEntryMayMoveNarrowsTheChangeRequestsToRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry governance.Entry
		want  governance.Moved
		ok    bool
	}{
		{"a configuration write", flagEntry("flag.killed", checkout, production),
			governance.Moved{Flag: checkout, Environment: production}, true},
		{"archiving a flag", flagEntry("flag.archived", checkout, workspace.EnvironmentID{}), governance.Moved{Flag: checkout}, true},
		{"deleting a flag", flagEntry("flag.deleted", checkout, workspace.EnvironmentID{}), governance.Moved{Flag: checkout}, true},
		{"deleting an environment", governance.Entry{Record: governance.Record{
			Environment: staging, Action: "environment.deleted",
			Entity: governance.Entity{Kind: "environment", ID: staging.String(), Key: "staging"},
		}}, governance.Moved{Environment: staging}, true},
		{"a definition edit", flagEntry("flag.updated", checkout, workspace.EnvironmentID{}), governance.Moved{}, false},
		{"a member's entry", governance.Entry{Record: governance.Record{
			Action: "member.created", Entity: governance.Entity{Kind: "member", ID: "m", Key: "m"},
		}}, governance.Moved{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := governance.MovedBy(tc.entry)
			if got != tc.want || ok != tc.ok {
				t.Errorf("MovedBy = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestSupersedingNamesTheEntryOfTheMovingWrite(t *testing.T) {
	for _, s := range []governance.ChangeRequestState{governance.StateOpen, governance.StateApproved} {
		cr := inState(t, s)
		later := now.Add(time.Minute)
		got, err := cr.Supersede(moving, later)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if got.State != governance.StateSuperseded || got.SupersededBy != moving || got.Version != cr.Version+1 ||
			!got.UpdatedAt.Equal(later) {
			t.Errorf("%s: superseded = %+v", s, got)
		}
	}
	if _, err := inState(t, governance.StateDraft).Supersede(moving, now); codeOf(err) != "invalid_state" {
		t.Errorf("superseding a draft: %v, want invalid_state", err)
	}
}
