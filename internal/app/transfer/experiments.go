package transfer

import (
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// experimentImport returns the experiment e of a document as the import port
// takes it.
func experimentImport(project workspace.ProjectID, e transfer.Experiment, owner, caller *access.MemberID,
	avoid []string, req governance.Request,
) ExperimentImport {
	in := ExperimentImport{
		Project: project, Key: e.Key, Name: e.Name, Hypothesis: e.Hypothesis, Owner: owner, Caller: caller, Tags: e.Tags,
		Flag: e.Flag, UnitType: e.UnitType, Variations: make([]experiments.Weight, len(e.Variations)),
		TrafficAllocation: e.TrafficAllocation, InclusionSalt: e.InclusionSalt, VariationSalt: e.VariationSalt,
		Status: experiments.Status(e.Status), StartedAt: e.StartedAt, StoppedAt: e.StoppedAt, ArchivedAt: e.ArchivedAt,
		Avoid: avoid, Request: req,
	}
	for i, w := range e.Variations {
		in.Variations[i] = experiments.Weight{Variation: w.Variation, Weight: w.Weight}
	}
	if e.ExpectedEndAt != nil {
		end := e.ExpectedEndAt.Time
		in.ExpectedEndAt = &end
	}
	if d := e.Decision; d != nil {
		in.Decision = &experiments.Decision{Outcome: experiments.Outcome(d.Outcome), WinnerVariation: d.WinnerVariation, Note: d.Note}
	}
	return in
}

// saltsOf returns every salt the document names, of rollouts and of
// experiments: the ones a salt drawn for an experiment must not be.
func saltsOf(d transfer.Document) []string {
	var out []string
	for _, f := range d.Flags {
		for _, c := range f.Configurations {
			for _, rule := range c.Rules {
				if rule.Serve.Kind == flags.ServeRollout && rule.Serve.Salt != "" {
					out = append(out, rule.Serve.Salt)
				}
			}
		}
	}
	for _, e := range d.Experiments {
		out = append(out, e.InclusionSalt, e.VariationSalt)
	}
	return out
}
