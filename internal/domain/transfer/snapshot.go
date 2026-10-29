package transfer

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Snapshot is a project's configuration as the contexts hold it, read in one
// consistent view.
type Snapshot struct {
	Project      workspace.Project
	Environments []workspace.Environment
	UnitTypes    []workspace.UnitType
	// Attributes include the standard ones, which a document never lists.
	Attributes []workspace.Attribute
	Segments   []flags.Segment
	Flags      []flags.Flag
	// Configurations are each flag's, in the environments it has one in.
	Configurations map[flags.FlagID][]flags.EnvironmentConfiguration
	Experiments    []experiments.Experiment
	// Emails are the addresses of the members who own a flag or an
	// experiment: members do not travel, so an owner does as an address.
	Emails map[access.MemberID]string
}

// FromSnapshot returns the document that holds s, and nothing else: no
// identifier but the rules', no author of an override, no member, credential
// or history.
func FromSnapshot(s Snapshot) Document {
	d := Document{
		SchemaVersion: Version,
		Project:       &Project{Key: s.Project.Key, Name: s.Project.Name, Description: s.Project.Description},
		Environments:  make([]Environment, 0, len(s.Environments)),
		UnitTypes:     make([]UnitType, 0, len(s.UnitTypes)),
		Attributes:    []Attribute{},
		Segments:      make([]Segment, 0, len(s.Segments)),
		Flags:         make([]Flag, 0, len(s.Flags)),
		Experiments:   make([]Experiment, 0, len(s.Experiments)),
	}
	for _, e := range s.Environments {
		d.Environments = append(d.Environments, Environment{Key: e.Key, Name: e.Name, Protected: e.Protected})
	}
	for _, u := range s.UnitTypes {
		d.UnitTypes = append(d.UnitTypes, UnitType{Key: u.Key, Name: u.Name, Description: u.Description})
	}
	for _, a := range s.Attributes {
		if !a.Standard {
			d.Attributes = append(d.Attributes, Attribute{Key: a.Key, Type: string(a.Type), Name: a.Name, Description: a.Description})
		}
	}
	for _, seg := range s.Segments {
		d.Segments = append(d.Segments, Segment{
			Key: seg.Key, Name: seg.Name, Description: seg.Description, Private: seg.Private, Conditions: conditionsOf(seg.Conditions),
		})
	}
	for _, f := range s.Flags {
		d.Flags = append(d.Flags, flagOf(f, s.Configurations[f.ID], s.Emails))
	}
	for _, e := range s.Experiments {
		d.Experiments = append(d.Experiments, experimentOf(e, s.Emails))
	}
	return d
}

func conditionsOf(in []flags.Condition) []Condition {
	out := make([]Condition, len(in))
	for i, c := range in {
		out[i] = Condition{
			Kind: c.Kind, Attribute: c.Attribute, Op: c.Op, Value: c.Value, Segment: c.Segment, Flag: c.Flag, Variation: c.Variation,
		}
	}
	return out
}

func flagOf(f flags.Flag, configurations []flags.EnvironmentConfiguration, emails map[access.MemberID]string) Flag {
	out := Flag{
		Key: f.Key, Type: string(f.Type), Name: f.Name, Description: f.Description, Owner: emails[f.Owner], Tags: f.Tags,
		Lifetime: string(f.Lifetime), ExpectedRemovalAt: utc(f.ExpectedRemovalAt), AutomaticExposure: f.AutomaticExposure,
		Archived:       f.ArchivedAt != nil,
		Variations:     make([]Variation, len(f.Variations)),
		Configurations: make(map[string]Configuration, len(configurations)),
	}
	for i, v := range f.Variations {
		out.Variations[i] = Variation{Key: v.Key, Value: v.Value, Name: v.Name, Description: v.Description}
	}
	for _, ec := range configurations {
		out.Configurations[ec.Environment] = configurationOf(ec.Configuration)
	}
	return out
}

func configurationOf(c flags.Configuration) Configuration {
	out := Configuration{
		On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough,
		Prerequisites: make([]Prerequisite, len(c.Prerequisites)),
		Overrides:     make([]Override, len(c.Overrides)),
		Rules:         make([]Rule, len(c.Rules)),
	}
	for i, p := range c.Prerequisites {
		out.Prerequisites[i] = Prerequisite{Flag: p.Flag, Variation: p.Variation}
	}
	for i, o := range c.Overrides {
		out.Overrides[i] = Override{UnitType: o.UnitType, UnitID: o.UnitID, Variation: o.Variation, Note: o.Note}
	}
	for i, r := range c.Rules {
		out.Rules[i] = Rule{ID: r.ID.String(), Description: r.Description, Conditions: conditionsOf(r.Conditions), Serve: serveOf(r.Serve)}
	}
	return out
}

// serveOf returns the serve with the fields of its kind only, as the
// management API shows it.
func serveOf(s flags.Serve) Serve {
	out := Serve{Kind: s.Kind, Variation: s.Variation}
	switch s.Kind {
	case flags.ServeExperiment:
		out.Experiment = s.Experiment
	case flags.ServeRollout:
		percentage := s.Percentage
		out.UnitType, out.Percentage, out.Salt = s.UnitType, &percentage, s.Salt
	}
	return out
}

func experimentOf(e experiments.Experiment, emails map[access.MemberID]string) Experiment {
	out := Experiment{
		Key: e.Key, Name: e.Name, Hypothesis: e.Hypothesis, Owner: emails[e.Owner], Tags: e.Tags, Flag: e.FlagKey,
		UnitType: e.UnitType, Variations: make([]ExperimentVariation, len(e.Variations)), TrafficAllocation: e.TrafficAllocation,
		InclusionSalt: e.InclusionSalt, VariationSalt: e.VariationSalt, Status: string(e.Status),
		StartedAt: utc(e.StartedAt), StoppedAt: utc(e.StoppedAt), ArchivedAt: utc(e.ArchivedAt),
	}
	for i, w := range e.Variations {
		out.Variations[i] = ExperimentVariation{Variation: w.Variation, Weight: w.Weight}
	}
	if e.ExpectedEndAt != nil {
		out.ExpectedEndAt = &Date{Time: e.ExpectedEndAt.UTC()}
	}
	if d := e.Decision; d != nil {
		out.Decision = &Decision{Outcome: string(d.Outcome), WinnerVariation: d.WinnerVariation, Note: d.Note}
	}
	return out
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
