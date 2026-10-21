package flags_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// app is the flags context's use cases on a real PostgreSQL, with a project
// of its own in each round.
type app struct {
	t        *testing.T
	ids      *id.Source
	ws       *pgworkspace.Store
	store    *pgflags.Store
	admin    access.MemberID
	req      governance.Request
	create   *appflags.CreateFlag
	update   *appflags.UpdateFlag
	replace  *appflags.ReplaceConfiguration
	segment  *appflags.CreateSegment
	deleteSg *appflags.DeleteSegment
}

func newApp(t *testing.T) *app {
	pool := testkit.Pool(t)
	c := clock.System{}
	ids := id.NewSource(c)
	tx, ws, store := postgres.NewTransactor(pool), pgworkspace.NewStore(pool), pgflags.NewStore(pool)
	rec := appflags.NewRecorder(pggovernance.NewAuditLog(pool), c, ids)
	rulesets := appdelivery.NewBumpRulesets(pgdelivery.NewStore(pool))
	owners := appaccess.NewOwnership(pgaccess.NewStore(pool))
	return &app{
		t: t, ids: ids, ws: ws, store: store, admin: testkit.NewMember(t, pool, testkit.AsInstanceAdmin()).ID,
		req: governance.Request{Actor: governance.CLIActor("test")},
		create: appflags.NewCreateFlag(appflags.CreateFlagDeps{
			Tx: tx, Projects: store, Environments: ws, Store: store, Owners: owners, Rulesets: rulesets, Recorder: rec,
		}),
		update: appflags.NewUpdateFlag(appflags.UpdateFlagDeps{
			Tx: tx, Projects: store, Store: store, Owners: owners, Rulesets: rulesets, Recorder: rec,
		}),
		replace: appflags.NewReplaceConfiguration(appflags.ReplaceConfigurationDeps{
			Tx: tx, Projects: store, Declarations: ws, Store: store, Rulesets: rulesets, Recorder: rec,
		}),
		segment: appflags.NewCreateSegment(appflags.CreateSegmentDeps{
			Tx: tx, Projects: store, Declarations: ws, Store: store, Rulesets: rulesets, Recorder: rec,
		}),
		deleteSg: appflags.NewDeleteSegment(appflags.DeleteSegmentDeps{
			Tx: tx, Projects: store, Store: store, Rulesets: rulesets, Recorder: rec,
		}),
	}
}

// project stores a new project with its default environments.
func (a *app) project() workspace.ProjectID {
	a.t.Helper()
	ctx := a.t.Context()
	p, err := workspace.NewProject(id.New[workspace.Project](a.ids), "p"+id.New[workspace.Project](a.ids).String()[24:], "P", "", testkit.Start)
	if err != nil {
		a.t.Fatal(err)
	}
	if err = a.ws.CreateProject(ctx, p); err != nil {
		a.t.Fatal(err)
	}
	for _, e := range workspace.DefaultsOf(p, envIDs{a.ids}).Environments {
		if err = a.ws.CreateEnvironment(ctx, e); err != nil {
			a.t.Fatal(err)
		}
	}
	return p.ID
}

type envIDs struct{ src *id.Source }

func (i envIDs) Environment() workspace.EnvironmentID { return id.New[workspace.Environment](i.src) }
func (i envIDs) UnitType() workspace.UnitTypeID       { return id.New[workspace.UnitType](i.src) }
func (i envIDs) Attribute() workspace.AttributeID     { return id.New[workspace.Attribute](i.src) }

func (a *app) flag(project workspace.ProjectID, key string, typ flags.Type, vs ...flags.VariationInput) {
	a.t.Helper()
	cmd := appflags.CreateFlagCommand{Project: project, Key: key, Type: typ, Name: key, Owner: &a.admin, Request: a.req}
	if vs != nil {
		cmd.Variations = &vs
	}
	if _, err := a.create.Handle(a.t.Context(), cmd); err != nil {
		a.t.Fatal(err)
	}
}

// put replaces flag's configuration in production with in, based on
// version 1.
func (a *app) put(ctx context.Context, project workspace.ProjectID, flag string, in flags.ConfigurationInput) error {
	_, err := a.replace.Handle(ctx, appflags.ReplaceConfigurationCommand{
		Configuration: appflags.ConfigurationCommand{
			Project: project, Flag: flag, Environment: "production", Expected: precondition.On(1), Request: a.req,
		},
		Input: in,
	})
	return err
}

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := apperrors.From(err); ok {
		return e.Code
	}
	return err.Error()
}

// race runs both writes at once and returns their refusals' codes, sorted.
func race(first, second func() error) []string {
	codes := make([]string, 2)
	var wg sync.WaitGroup
	wg.Go(func() { codes[0] = codeOf(first()) })
	wg.Go(func() { codes[1] = codeOf(second()) })
	wg.Wait()
	slices.Sort(codes)
	return codes
}

func requiring(flag, variation string, off string) flags.ConfigurationInput {
	return flags.ConfigurationInput{OffVariation: off, Fallthrough: off,
		Prerequisites: []flags.Prerequisite{{Flag: flag, Variation: variation}}}
}

// Two writers each making one flag require the other would together close
// a cycle; the project's lock lets the second see the first, so exactly one
// is accepted.
func TestTwoWritersClosingACycleFromBothEndsLeaveOneRequirement(t *testing.T) {
	a := newApp(t)
	for round := range 10 {
		p := a.project()
		a.flag(p, "left", flags.TypeBoolean)
		a.flag(p, "right", flags.TypeBoolean)
		codes := race(
			func() error { return a.put(t.Context(), p, "left", requiring("right", "true", "false")) },
			func() error { return a.put(t.Context(), p, "right", requiring("left", "true", "false")) },
		)
		if !slices.Equal(codes, []string{"", "invalid_configuration"}) {
			t.Fatalf("round %d: refusals = %q, want one accepted and one invalid_configuration", round, codes)
		}
	}
}

// Removing a variation while another flag's write starts requiring it, or
// deleting a segment while a rule starts naming it, never both succeed:
// whichever runs second sees what the first did.
func TestRemovingWhatAWriteStartsNamingNeverBothSucceed(t *testing.T) {
	a := newApp(t)
	for round := range 10 {
		p := a.project()
		a.flag(p, "copy", flags.TypeString, variation("a", `"A"`), variation("b", `"B"`))
		a.flag(p, "banner", flags.TypeBoolean)
		codes := race(
			func() error {
				vs := []flags.VariationInput{variation("a", `"A"`)}
				_, err := a.update.Handle(t.Context(), appflags.UpdateFlagCommand{
					Project: p, Key: "copy", Change: flags.FlagChange{Variations: &vs}, Expected: precondition.On(1), Request: a.req,
				})
				return err
			},
			func() error { return a.put(t.Context(), p, "banner", requiring("copy", "b", "false")) },
		)
		if !slices.Equal(codes, []string{"", "in_use"}) && !slices.Equal(codes, []string{"", "invalid_configuration"}) {
			t.Fatalf("round %d: removing a variation: refusals = %q, want exactly one accepted", round, codes)
		}

		if _, err := a.segment.Handle(t.Context(), appflags.CreateSegmentCommand{Project: p, Key: "beta", Name: "Beta", Request: a.req}); err != nil {
			t.Fatal(err)
		}
		codes = race(
			func() error {
				return a.deleteSg.Handle(t.Context(), appflags.SegmentCommand{Project: p, Key: "beta", Expected: precondition.On(1), Request: a.req})
			},
			func() error {
				return a.put(t.Context(), p, "copy", flags.ConfigurationInput{OffVariation: "a", Fallthrough: "a", Rules: []flags.RuleInput{{
					Conditions: []flags.Condition{{Kind: flags.ConditionInSegment, Segment: "beta"}},
					Serve:      flags.ServeInput{Kind: flags.ServeVariation, Variation: "a"},
				}}})
			},
		)
		if !slices.Equal(codes, []string{"", "in_use"}) && !slices.Equal(codes, []string{"", "invalid_configuration"}) {
			t.Fatalf("round %d: deleting a segment: refusals = %q, want exactly one accepted", round, codes)
		}
	}
}

func variation(key, value string) flags.VariationInput {
	return flags.VariationInput{Key: key, Value: []byte(value)}
}
