package management_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/oapi-codegen/nullable"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// scheduleFixture is a change request fixture on a clock the test moves,
// with a maintainer to approve the developer's change requests.
type scheduleFixture struct {
	changeRequestFixture
	clock      *clock.Fake
	maintainer *testkit.Browser
}

func startSchedules(t *testing.T) scheduleFixture {
	t.Helper()
	c := testkit.Clock()
	f := startChangeRequests(t, testkit.WithClock(c))
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	return scheduleFixture{changeRequestFixture: f, clock: c, maintainer: maintainer}
}

// scheduled returns an approved change request of the developer's
// proposing proposed in production, to be applied at at. Whatever the test
// leaves of it scheduled is cancelled when it ends, so no later test's
// scheduler finds it due.
func (f scheduleFixture) scheduled(t *testing.T, proposed management.FlagConfigurationPut, at time.Time) management.ChangeRequest {
	t.Helper()
	body := configurationRequest("Release the checkout title", proposed)
	body.Open, body.ApplyAt = ptr(true), &at
	cr := mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key, createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, body))
	t.Cleanup(func() {
		// The test's context is done by now.
		ctx := context.Background()
		now, err := f.admin.GetChangeRequestWithResponse(ctx, f.project.Key, cr.Id)
		if err != nil || now.JSON200 == nil || now.JSON200.State != management.ChangeRequestStateApproved {
			return
		}
		if _, err = f.admin.CancelChangeRequestWithResponse(ctx, f.project.Key, cr.Id,
			&management.CancelChangeRequestParams{IfMatch: ifMatch(now.JSON200.Version)}); err != nil {
			t.Error(err)
		}
	})
	return cr
}

// tick runs one round of the scheduler of srv.
func tick(t *testing.T, srv *testkit.Server) error {
	t.Helper()
	return srv.Tick(t, worker.ChangeRequestSchedulerName)
}

func mustTick(t *testing.T, srv *testkit.Server) {
	t.Helper()
	if err := tick(t, srv); err != nil {
		t.Fatalf("scheduler round: %v", err)
	}
}

func TestAnApplyTimeIsSetOnCreateAndEditBetweenTheLeadAndAYear(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	now := f.clock.Now()
	for name, tc := range map[string]struct {
		at   time.Time
		code string
	}{
		"in the past":      {at: now.Add(-time.Hour), code: "too_soon"},
		"inside the lead":  {at: now.Add(59 * time.Second), code: "too_soon"},
		"over a year away": {at: now.Add(366 * 24 * time.Hour), code: "too_late"},
	} {
		body := configurationRequest("Too soon or too late", proposedOn())
		body.ApplyAt = &tc.at
		res := do(f.dev.CreateChangeRequestWithResponse(t.Context(), key, body))
		p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_change_request")
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != "/apply_at" || (*p.Errors)[0].Code != tc.code {
			t.Errorf("%s: errors = %+v, want /apply_at %s", name, p.Errors, tc.code)
		}
	}

	at := now.Add(2 * time.Hour).In(time.FixedZone("UTC+3", 3*3600))
	body := configurationRequest("Release at noon", proposedOn())
	body.ApplyAt = &at
	cr := createChangeRequest(t, f.dev.ClientWithResponses, key, body)
	if cr.ApplyAt == nil || !cr.ApplyAt.Equal(at) || cr.ApplyAt.Location() != time.UTC || cr.ScheduleFailure != nil {
		t.Errorf("created with apply_at %v, failure %+v; want %v in UTC", cr.ApplyAt, cr.ScheduleFailure, at.UTC())
	}

	f.clock.Advance(time.Hour)
	later := f.clock.Now().Add(3 * time.Hour)
	res := updateChangeRequest(t, f.dev.ClientWithResponses, key, cr, management.ChangeRequestPatch{ApplyAt: nullable.NewNullableWithValue(later)})
	if res.JSON200 == nil || res.JSON200.ApplyAt == nil || !res.JSON200.ApplyAt.Equal(later) || res.JSON200.Version != 2 {
		t.Fatalf("moving the apply time = %d: %s", res.StatusCode(), res.Body)
	}
	moved := *res.JSON200
	// The lead is judged from the edit.
	soon := f.clock.Now().Add(30 * time.Second)
	refused := updateChangeRequest(t, f.dev.ClientWithResponses, key, moved, management.ChangeRequestPatch{ApplyAt: nullable.NewNullableWithValue(soon)})
	assertProblem(t, "an apply time inside the lead", refused.StatusCode(), refused.Body, http.StatusUnprocessableEntity, "invalid_change_request")
	kept := updateChangeRequest(t, f.dev.ClientWithResponses, key, moved, management.ChangeRequestPatch{Title: ptr("Release later")})
	if kept.JSON200 == nil || kept.JSON200.ApplyAt == nil || !kept.JSON200.ApplyAt.Equal(later) {
		t.Fatalf("an edit without apply_at = %d: %s", kept.StatusCode(), kept.Body)
	}
	cleared := updateChangeRequest(t, f.dev.ClientWithResponses, key, *kept.JSON200,
		management.ChangeRequestPatch{ApplyAt: nullable.NewNullNullable[time.Time]()})
	if cleared.JSON200 == nil || cleared.JSON200.ApplyAt != nil {
		t.Fatalf("clearing the apply time = %d: %s", cleared.StatusCode(), cleared.Body)
	}
	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions,
		[]string{"change_request.updated", "change_request.updated", "change_request.updated", "change_request.drafted"}) {
		t.Errorf("audit = %v", actions)
	}
}

func TestAScheduledChangeRequestIsListedRefusedByHandAndCountedOnItsConfiguration(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	at := f.clock.Now().Add(time.Hour)
	cr := f.scheduled(t, proposedOn(), at)
	unscheduled := openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())

	for _, by := range []*testkit.Browser{f.dev, f.maintainer} {
		res := applyChangeRequest(t, by.ClientWithResponses, key, cr)
		assertProblem(t, "applying a scheduled change request by hand", res.StatusCode(), res.Body, http.StatusConflict, "scheduled")
	}

	list := func(scheduled bool) []management.ChangeRequest {
		t.Helper()
		res := do(f.dev.ListChangeRequestsWithResponse(t.Context(), key, &management.ListChangeRequestsParams{Scheduled: &scheduled}))
		if res.JSON200 == nil {
			t.Fatalf("list scheduled=%v = %d: %s", scheduled, res.StatusCode(), res.Body)
		}
		return res.JSON200.Items
	}
	if got := list(true); len(got) != 1 || got[0].Id != cr.Id {
		t.Errorf("scheduled = %v, want the one scheduled", got)
	}
	if got := list(false); len(got) != 1 || got[0].Id != unscheduled.Id {
		t.Errorf("not scheduled = %v, want the other", got)
	}

	c := getConfiguration(t, f.admin, key, "checkout_title", "production")
	if w := c.ChangeRequests; w == nil || w.Open != 1 || w.Approved != 1 || w.NextApplyAt == nil || !w.NextApplyAt.Equal(at) {
		t.Errorf("waiting = %+v, want the apply time named", w)
	}
}

func TestTheSchedulerAppliesAnApprovedChangeRequestAtItsTimeAsTheSystem(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	at := f.clock.Now().Add(time.Hour)
	cr := f.scheduled(t, proposedOn(), at)
	base := getConfiguration(t, f.admin, key, "checkout_title", "production")

	f.clock.Advance(time.Hour - time.Second)
	mustTick(t, f.srv)
	if got := getChangeRequest(t, f.admin, key, cr); got.State != management.ChangeRequestStateApproved {
		t.Fatalf("before its time: %s", got.State)
	}

	f.clock.Advance(time.Second)
	mustTick(t, f.srv)
	applied := getChangeRequest(t, f.admin, key, cr)
	if applied.State != management.ChangeRequestStateApplied || applied.AppliedAt == nil || !applied.AppliedAt.Equal(at) ||
		applied.AppliedBy == nil || applied.AppliedBy.Kind != management.ChangeRequestApplierKindSystem ||
		applied.AppliedBy.Id != nil || !applied.ApplyAt.Equal(at) || applied.ScheduleFailure != nil {
		t.Fatalf("at its time: %+v applied by %+v", applied, applied.AppliedBy)
	}
	if c := getConfiguration(t, f.admin, key, "checkout_title", "production"); !c.On || c.Version != base.Version+1 {
		t.Errorf("configuration = on %v at %d, want on at %d", c.On, c.Version, base.Version+1)
	}
	entries := applicationEntries(t, f.srv, cr)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
		if e.Actor.Kind != management.ActorKindSystem {
			t.Errorf("%s by %+v, want the system", e.Action, e.Actor)
		}
	}
	if !slices.Contains(actions, "change_request.applied") || len(actions) < 2 {
		t.Errorf("entries of the application = %v", actions)
	}

	mustTick(t, f.srv)
	if again := applicationEntries(t, f.srv, cr); len(again) != len(entries) {
		t.Errorf("another round wrote %d entries, want none", len(again)-len(entries))
	}
}

func TestAChangeRequestDueWhileNoReplicaRanIsAppliedLate(t *testing.T) {
	f := startSchedules(t)
	at := f.clock.Now().Add(time.Hour)
	cr := f.scheduled(t, proposedOn(), at)
	f.clock.Advance(5 * time.Hour)
	mustTick(t, f.srv)
	if got := getChangeRequest(t, f.admin, f.project.Key, cr); got.State != management.ChangeRequestStateApplied ||
		!got.AppliedAt.Equal(at.Add(4*time.Hour)) {
		t.Errorf("late: %s at %v", got.State, got.AppliedAt)
	}
}

func TestACancelledScheduledChangeRequestIsNeverApplied(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	cr := f.scheduled(t, proposedOn(), f.clock.Now().Add(time.Hour))
	base := getConfiguration(t, f.admin, key, "checkout_title", "production")
	if res := cancelChangeRequest(t, f.maintainer.ClientWithResponses, key, cr); res.JSON200 == nil {
		t.Fatalf("cancel = %d: %s", res.StatusCode(), res.Body)
	}
	f.clock.Advance(2 * time.Hour)
	mustTick(t, f.srv)
	if got := getChangeRequest(t, f.admin, key, cr); got.State != management.ChangeRequestStateCancelled || got.AppliedAt != nil {
		t.Errorf("cancelled: %s applied at %v", got.State, got.AppliedAt)
	}
	if c := getConfiguration(t, f.admin, key, "checkout_title", "production"); c.Version != base.Version {
		t.Errorf("configuration moved to %d", c.Version)
	}
}

func TestAScheduledChangeRequestOnAMovedBaseIsSupersededNotFailed(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	cr := f.scheduled(t, proposedOn(), f.clock.Now().Add(time.Hour))
	moveBehind(t, cr)
	f.clock.Advance(time.Hour)
	mustTick(t, f.srv)
	got := getChangeRequest(t, f.admin, key, cr)
	if got.State != management.ChangeRequestStateSuperseded || got.SupersededBy != nil || got.ScheduleFailure != nil ||
		got.AppliedAt != nil {
		t.Errorf("on a moved base: %+v", got)
	}
	if entries := auditOf(t, f.srv, "change_request.superseded", cr.Id.String()); len(entries) != 1 ||
		entries[0].Actor.Kind != management.ActorKindSystem {
		t.Errorf("superseded entries = %+v, want one by the system", entries)
	}
}

// moveBehind moves the configuration cr is based on without superseding
// cr, as a write committed while cr was being applied would: the test
// writes the version straight into the table, there being no API path that
// moves a configuration and leaves its change requests approved.
func moveBehind(t *testing.T, cr management.ChangeRequest) {
	t.Helper()
	if _, err := testkit.Pool(t).Exec(t.Context(), `
		UPDATE flag_configurations SET version = version + 1
		WHERE flag_id = (SELECT flag_id FROM change_requests WHERE id = $1)
		  AND environment_id = (SELECT environment_id FROM change_requests WHERE id = $1)`, cr.Id); err != nil {
		t.Fatal(err)
	}
}

func TestTwoReplicasApplyAScheduledChangeRequestOnce(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	other := testkit.StartServer(t, testkit.WithClock(f.clock))
	at := f.clock.Now().Add(time.Hour)
	var crs []management.ChangeRequest
	for _, env := range []string{"production", "staging"} {
		for i := range 3 {
			flag := "release_" + env + "_" + string(rune('a'+i))
			stringFlag(t, f.admin, key, flag)
			body := configurationRequest("Release "+flag, proposedOn())
			body.Flag, body.Environment, body.Open, body.ApplyAt = ptr(flag), ptr(env), ptr(true), &at
			crs = append(crs, mustApprove(t, f.maintainer.ClientWithResponses, key, createChangeRequest(t, f.dev.ClientWithResponses, key, body)))
		}
	}
	f.clock.Advance(time.Hour)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, srv := range []*testkit.Server{f.srv, other, f.srv, other} {
		wg.Go(func() {
			for range 5 {
				if err := srv.Tick(t, worker.ChangeRequestSchedulerName); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	for _, cr := range crs {
		got := getChangeRequest(t, f.admin, key, cr)
		c := getConfiguration(t, f.admin, key, *cr.Flag, *cr.Environment)
		if got.State != management.ChangeRequestStateApplied || !c.On {
			t.Errorf("%s: %s, configuration on %v", *cr.Flag, got.State, c.On)
		}
		if applied := auditOf(t, f.srv, "change_request.applied", cr.Id.String()); len(applied) != 1 {
			t.Errorf("%s applied %d times", *cr.Flag, len(applied))
		}
	}
}

// failing returns a scheduled change request whose application fails once
// it is due: it names the flag gate as a prerequisite, and gate is archived
// after the approval. It returns the change request and gate's version.
func (f scheduleFixture) failing(t *testing.T) (management.ChangeRequest, int64) {
	t.Helper()
	gate := stringFlag(t, f.admin, f.project.Key, "gate")
	gated := proposedOn()
	gated.Prerequisites = &[]management.Prerequisite{{Flag: "gate", Variation: "b"}}
	cr := f.scheduled(t, gated, f.clock.Now().Add(time.Hour))
	archived := archive(t, f.admin, f.project.Key, "gate", gate.Version)
	if archived.JSON200 == nil {
		t.Fatalf("archive gate = %d: %s", archived.StatusCode(), archived.Body)
	}
	f.clock.Advance(time.Hour)
	return cr, archived.JSON200.Version
}

// failingTick runs a scheduler round that fails, and returns the change
// request as it is then.
func (f scheduleFixture) failingTick(t *testing.T, cr management.ChangeRequest) management.ChangeRequest {
	t.Helper()
	if err := tick(t, f.srv); err == nil {
		t.Fatal("a round failing to apply a change request succeeded")
	}
	f.clock.Advance(time.Minute)
	return getChangeRequest(t, f.admin, f.project.Key, cr)
}

func TestAScheduleFailureIsRecordedAndTheSchedulerStopsAtTheLimit(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	cr, gateVersion := f.failing(t)
	base := getConfiguration(t, f.admin, key, "checkout_title", "production")

	var last management.ChangeRequest
	for n := 1; n <= 5; n++ {
		failedAt := f.clock.Now()
		last = f.failingTick(t, cr)
		sf := last.ScheduleFailure
		if sf == nil || sf.Code != "invalid_configuration" || sf.Message == "" || sf.Attempts != n || sf.Stopped != (n == 5) ||
			!sf.At.Equal(failedAt) {
			t.Fatalf("after %d failures: %+v", n, sf)
		}
		if last.State != management.ChangeRequestStateApproved || last.Version != cr.Version || !last.UpdatedAt.Equal(cr.UpdatedAt) {
			t.Errorf("a failure moved the change request: %s at version %d", last.State, last.Version)
		}
	}
	mustTick(t, f.srv)
	if stopped := getChangeRequest(t, f.admin, key, cr); *stopped.ScheduleFailure != *last.ScheduleFailure {
		t.Errorf("a round after the stop changed the failure to %+v", stopped.ScheduleFailure)
	}
	c := getConfiguration(t, f.admin, key, "checkout_title", "production")
	if c.Version != base.Version {
		t.Errorf("configuration moved to %d", c.Version)
	}
	// The stopped change request still waits approved, but nothing will
	// apply it at its time.
	if w := c.ChangeRequests; w == nil || w.Approved != 1 || w.NextApplyAt != nil {
		t.Errorf("waiting on the configuration = %+v, want one approved and no apply time", w)
	}
	entries := auditOf(t, f.srv, "change_request.schedule_failed", cr.Id.String())
	if len(entries) != 1 || entries[0].Actor.Kind != management.ActorKindSystem {
		t.Fatalf("schedule_failed entries = %+v, want one by the system", entries)
	}
	before := entries[0].Before.(map[string]any)["schedule_failure"].(map[string]any)
	after := entries[0].After.(map[string]any)["schedule_failure"].(map[string]any)
	if before["attempts"] != 4.0 || before["stopped"] != false || after["attempts"] != 5.0 || after["stopped"] != true ||
		after["code"] != "invalid_configuration" {
		t.Errorf("entry before %v, after %v", before, after)
	}

	cancelled := cancelChangeRequest(t, f.maintainer.ClientWithResponses, key, last)
	if cancelled.JSON200 == nil || cancelled.JSON200.ScheduleFailure != nil {
		t.Fatalf("cancel = %d: %s, want the failure cleared", cancelled.StatusCode(), cancelled.Body)
	}
	if res := restore(t, f.admin, key, "gate", gateVersion); res.JSON200 == nil {
		t.Fatalf("restore gate = %d: %s", res.StatusCode(), res.Body)
	}
	copied := do(f.dev.CopyChangeRequestWithResponse(t.Context(), key, cr.Id))
	if copied.JSON201 == nil || copied.JSON201.ApplyAt != nil || copied.JSON201.ScheduleFailure != nil {
		t.Errorf("copy = %d: %s, want neither apply time nor failure", copied.StatusCode(), copied.Body)
	}
}

func TestAScheduleFailureIsClearedWhenTheChangeRequestApplies(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	cr, gateVersion := f.failing(t)
	f.failingTick(t, cr)
	if failed := f.failingTick(t, cr); failed.ScheduleFailure == nil || failed.ScheduleFailure.Attempts != 2 {
		t.Fatalf("after two failures: %+v", failed.ScheduleFailure)
	}
	if res := restore(t, f.admin, key, "gate", gateVersion); res.JSON200 == nil {
		t.Fatalf("restore gate = %d: %s", res.StatusCode(), res.Body)
	}
	mustTick(t, f.srv)
	if got := getChangeRequest(t, f.admin, key, cr); got.State != management.ChangeRequestStateApplied || got.ScheduleFailure != nil {
		t.Errorf("applied = %s, failure %+v", got.State, got.ScheduleFailure)
	}
	if entries := auditOf(t, f.srv, "change_request.schedule_failed", cr.Id.String()); len(entries) != 0 {
		t.Errorf("%d schedule_failed entries below the limit", len(entries))
	}
}

func TestAScheduleFailureIsKeptWhenTheChangeRequestIsSuperseded(t *testing.T) {
	f := startSchedules(t)
	cr, _ := f.failing(t)
	f.failingTick(t, cr)
	c := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "production")
	mustPut(t, f.admin, f.project.Key, "checkout_title", "production", c.Version,
		management.FlagConfigurationPut{On: false, OffVariation: "b", Fallthrough: "a"})
	got := getChangeRequest(t, f.admin, f.project.Key, cr)
	if got.State != management.ChangeRequestStateSuperseded || got.ScheduleFailure == nil || got.ScheduleFailure.Attempts != 1 ||
		got.ScheduleFailure.Code != "invalid_configuration" {
		t.Errorf("superseded = %s, failure %+v", got.State, got.ScheduleFailure)
	}
}

// A definition is scheduled as a configuration is: refused by hand, then
// applied by the system in every environment, protected production
// included, as one edit of the flag.
func TestTheSchedulerAppliesAScheduledDefinition(t *testing.T) {
	f := startSchedules(t)
	key := f.project.Key
	setProtected(t, f.admin, key, "production", true)
	at := f.clock.Now().Add(time.Hour)
	body := flagDefinitionRequest("checkout_title", management.FlagDefinitionPut{
		Variations: *variations("a", `"Welcome"`, "b", `"B"`, "c", `"C"`),
	})
	body.ApplyAt = &at
	cr := mustApprove(t, f.maintainer.ClientWithResponses, key, createChangeRequest(t, f.dev.ClientWithResponses, key, body))
	res := applyChangeRequest(t, f.dev.ClientWithResponses, key, cr)
	assertProblem(t, "applying a scheduled definition by hand", res.StatusCode(), res.Body, http.StatusConflict, "scheduled")
	before := rulesetVersions(t, f.project)

	f.clock.Advance(time.Hour)
	mustTick(t, f.srv)
	got := getChangeRequest(t, f.admin, key, cr)
	if got.State != management.ChangeRequestStateApplied || got.AppliedBy == nil ||
		got.AppliedBy.Kind != management.ChangeRequestApplierKindSystem {
		t.Fatalf("at its time: %s applied by %+v", got.State, got.AppliedBy)
	}
	if flag := getFlag(t, f.admin, key, "checkout_title"); flag.Version != cr.BaseVersion+1 ||
		string(flag.Variations[0].Value) != `"Welcome"` {
		t.Errorf("flag = version %d, a = %s; want version %d with a = Welcome", flag.Version, flag.Variations[0].Value,
			cr.BaseVersion+1)
	}
	if moved := bumped(t, "the application", before, rulesetVersions(t, f.project)); !slices.Equal(moved, everyEnvironment) {
		t.Errorf("rulesets moved = %v, want every environment", moved)
	}
	entries := applicationEntries(t, f.srv, cr)
	if actions := actionsOf(entries); !slices.Equal(actions, []string{"change_request.applied", "flag.updated"}) {
		t.Fatalf("entries of the application = %v", actions)
	}
	for _, e := range entries {
		if e.Actor.Kind != management.ActorKindSystem {
			t.Errorf("%s by %+v, want the system", e.Action, e.Actor)
		}
	}
}
