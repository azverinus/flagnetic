package management

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// The bounds of a page of experiments.
const (
	defaultExperimentLimit = 50
	maxExperimentLimit     = 200
)

// ListExperiments answers a page of the project's experiments by key.
func (a *api) ListExperiments(ctx context.Context, req ListExperimentsRequestObject) (ListExperimentsResponseObject, error) {
	page := appexperiments.ExperimentPage{Limit: defaultExperimentLimit}
	page.Flag, page.Tag = valueOf(req.Params.Flag), valueOf(req.Params.Tag)
	page.IncludeArchived = valueOr(req.Params.IncludeArchived, false)
	page.PastExpectedEnd = valueOr(req.Params.PastExpectedEnd, false)
	page.AwaitingCodeRemoval = valueOr(req.Params.AwaitingCodeRemoval, false)
	page.Owner = ownerOf(req.Params.Owner)
	if st := req.Params.Status; st != nil {
		page.Status = experiments.Status(*st)
		if !slices.Contains([]experiments.Status{experiments.StatusDraft, experiments.StatusRunning, experiments.StatusStopped, experiments.StatusArchived}, page.Status) {
			return nil, flaghttp.InvalidRequest("status must be one of draft, running, stopped and archived")
		}
	}
	if l := req.Params.Limit; l != nil {
		if *l < 1 || *l > maxExperimentLimit {
			return nil, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxExperimentLimit))
		}
		page.Limit = *l
	}
	if c := req.Params.Cursor; c != nil {
		after, err := decodeExperimentCursor(*c)
		if err != nil {
			return nil, err
		}
		page.After = after
	}
	list, next, err := a.h.ListExperiments.Handle(ctx, projectID(ctx), page)
	if err != nil {
		return nil, err
	}
	out := ListExperiments200JSONResponse{Items: each(list, experimentOut)}
	if next != nil {
		cursor := encodeExperimentCursor(*next)
		out.NextCursor = &cursor
	}
	return out, nil
}

// encodeExperimentCursor returns the cursor of the page starting after the
// experiment keyed key. It is opaque, so the order a page follows can change
// without breaking a client that only passes it back.
func encodeExperimentCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// decodeExperimentCursor reads cursor, refusing one this API did not return.
func decodeExperimentCursor(cursor string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(b) == 0 || !utf8.Valid(b) {
		return "", flaghttp.InvalidRequest("cursor is not a next_cursor this API returned")
	}
	return string(b), nil
}

// CreateExperiment creates an experiment as a draft.
func (a *api) CreateExperiment(ctx context.Context, req CreateExperimentRequestObject) (CreateExperimentResponseObject, error) {
	b := req.Body
	cmd := appexperiments.CreateExperimentCommand{
		Project: projectID(ctx), Key: b.Key, Name: b.Name, Hypothesis: valueOf(b.Hypothesis), Owner: ownerOf(b.Owner),
		Caller: callerMember(ctx), Tags: valueOr(b.Tags, nil), Flag: b.Flag, UnitType: b.UnitType,
		Variations: each(b.Variations, func(w ExperimentWeight) experiments.Weight {
			return experiments.Weight{Variation: w.Variation, Weight: w.Weight}
		}),
		TrafficAllocation: b.TrafficAllocation, Request: requestOf(ctx),
	}
	if d := b.ExpectedEndAt; d != nil {
		cmd.ExpectedEndAt = &d.Time
	}
	e, err := a.h.CreateExperiment.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return CreateExperiment201JSONResponse{Body: experimentOut(e), Headers: CreateExperiment201ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// GetExperiment answers an experiment.
func (a *api) GetExperiment(ctx context.Context, req GetExperimentRequestObject) (GetExperimentResponseObject, error) {
	e, err := a.h.GetExperiment.Handle(ctx, projectID(ctx), req.Experiment)
	if err != nil {
		return nil, err
	}
	return GetExperiment200JSONResponse{Body: experimentOut(e), Headers: GetExperiment200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// UpdateExperiment changes a draft experiment.
func (a *api) UpdateExperiment(ctx context.Context, req UpdateExperimentRequestObject) (UpdateExperimentResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	change := experiments.Change{
		Key: b.Key, Name: b.Name, Hypothesis: b.Hypothesis, Tags: b.Tags, FlagKey: b.Flag, UnitType: b.UnitType,
		TrafficAllocation: b.TrafficAllocation,
	}
	if b.Variations != nil {
		ws := each(*b.Variations, func(w ExperimentWeight) experiments.Weight {
			return experiments.Weight{Variation: w.Variation, Weight: w.Weight}
		})
		change.Variations = &ws
	}
	if d := b.ExpectedEndAt; d != nil {
		change.ExpectedEndAt = &d.Time
	}
	e, err := a.h.UpdateExperiment.Handle(ctx, appexperiments.UpdateExperimentCommand{
		Project: projectID(ctx), Key: req.Experiment, Change: change, Owner: ownerOf(b.Owner), Expected: expected,
		Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateExperiment200JSONResponse{Body: experimentOut(e), Headers: UpdateExperiment200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// experimentCommand returns the command naming experiment of the request's
// project, based on the version header names.
func experimentCommand(ctx context.Context, experiment string, header *IfMatch) (appexperiments.ExperimentCommand, error) {
	expected, err := ifMatch(header)
	return appexperiments.ExperimentCommand{Project: projectID(ctx), Key: experiment, Expected: expected, Request: requestOf(ctx)}, err
}

// DeleteExperiment deletes a draft experiment.
func (a *api) DeleteExperiment(ctx context.Context, req DeleteExperimentRequestObject) (DeleteExperimentResponseObject, error) {
	cmd, err := experimentCommand(ctx, req.Experiment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.h.DeleteExperiment.Handle(ctx, cmd); err != nil {
		return nil, err
	}
	return DeleteExperiment204Response{}, nil
}

// RegenerateExperimentSalts draws new salts for a draft experiment.
func (a *api) RegenerateExperimentSalts(ctx context.Context, req RegenerateExperimentSaltsRequestObject) (RegenerateExperimentSaltsResponseObject, error) {
	cmd, err := experimentCommand(ctx, req.Experiment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	e, err := a.h.RegenerateExperimentSalts.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return RegenerateExperimentSalts200JSONResponse{Body: experimentOut(e), Headers: RegenerateExperimentSalts200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// StartExperiment starts a draft experiment.
func (a *api) StartExperiment(ctx context.Context, req StartExperimentRequestObject) (StartExperimentResponseObject, error) {
	cmd, err := experimentCommand(ctx, req.Experiment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	e, err := a.h.StartExperiment.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return StartExperiment200JSONResponse{Body: experimentOut(e), Headers: StartExperiment200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// StopExperiment stops a running experiment with a decision.
func (a *api) StopExperiment(ctx context.Context, req StopExperimentRequestObject) (StopExperimentResponseObject, error) {
	cmd, err := experimentCommand(ctx, req.Experiment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if !b.Outcome.Valid() {
		return nil, flaghttp.InvalidRequest("outcome must be winner or removed")
	}
	e, err := a.h.StopExperiment.Handle(ctx, appexperiments.StopExperimentCommand{
		Experiment: cmd, Caller: callerMember(ctx),
		Decision: experiments.Decision{
			Outcome: experiments.Outcome(b.Outcome), WinnerVariation: valueOr(b.WinnerVariation, ""), Note: valueOr(b.Note, ""),
		},
	})
	if err != nil {
		return nil, err
	}
	return StopExperiment200JSONResponse{Body: experimentOut(e), Headers: StopExperiment200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// SetExperimentDecisionNote changes the note of a stopped experiment's
// decision.
func (a *api) SetExperimentDecisionNote(ctx context.Context, req SetExperimentDecisionNoteRequestObject) (SetExperimentDecisionNoteResponseObject, error) {
	cmd, err := experimentCommand(ctx, req.Experiment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	e, err := a.h.SetExperimentDecisionNote.Handle(ctx, appexperiments.SetDecisionNoteCommand{Experiment: cmd, Note: req.Body.Note})
	if err != nil {
		return nil, err
	}
	return SetExperimentDecisionNote200JSONResponse{Body: experimentOut(e), Headers: SetExperimentDecisionNote200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// ArchiveExperiment archives a draft or stopped experiment.
func (a *api) ArchiveExperiment(ctx context.Context, req ArchiveExperimentRequestObject) (ArchiveExperimentResponseObject, error) {
	cmd, err := experimentCommand(ctx, req.Experiment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	e, err := a.h.ArchiveExperiment.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return ArchiveExperiment200JSONResponse{Body: experimentOut(e), Headers: ArchiveExperiment200ResponseHeaders{ETag: etag(e.Version)}}, nil
}

// CloneExperiment copies an experiment into a new draft.
func (a *api) CloneExperiment(ctx context.Context, req CloneExperimentRequestObject) (CloneExperimentResponseObject, error) {
	e, err := a.h.CloneExperiment.Handle(ctx, appexperiments.CloneExperimentCommand{
		Project: projectID(ctx), Source: req.Experiment, Key: req.Body.Key, Name: req.Body.Name, Caller: callerMember(ctx),
		Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CloneExperiment201JSONResponse{Body: experimentOut(e), Headers: CloneExperiment201ResponseHeaders{ETag: etag(e.Version)}}, nil
}

func experimentOut(e experiments.Experiment) Experiment {
	out := Experiment{
		Id: openapi_types.UUID(e.ID.Bytes()), Key: e.Key, Name: e.Name, Hypothesis: e.Hypothesis,
		Owner: openapi_types.UUID(e.Owner.Bytes()), Tags: e.Tags, Flag: e.FlagKey, UnitType: e.UnitType,
		Variations: each(e.Variations, func(w experiments.Weight) ExperimentWeight {
			return ExperimentWeight{Variation: w.Variation, Weight: w.Weight}
		}),
		TrafficAllocation: e.TrafficAllocation, InclusionSalt: e.InclusionSalt, VariationSalt: e.VariationSalt,
		Status: ExperimentStatus(e.Status), StartedAt: e.StartedAt, StoppedAt: e.StoppedAt, ArchivedAt: e.ArchivedAt,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, Version: e.Version,
	}
	if d := e.ExpectedEndAt; d != nil {
		out.ExpectedEndAt = &openapi_types.Date{Time: *d}
	}
	if e.CreatedBy != nil {
		m := openapi_types.UUID(e.CreatedBy.Bytes())
		out.CreatedBy = &m
	}
	if d := e.Decision; d != nil {
		out.Decision = &ExperimentDecision{Outcome: ExperimentOutcome(d.Outcome), Note: d.Note}
		if d.WinnerVariation != "" {
			out.Decision.WinnerVariation = &d.WinnerVariation
		}
	}
	return out
}

// GetExperimentAssignment answers how an experiment treats one unit.
func (a *api) GetExperimentAssignment(ctx context.Context, req GetExperimentAssignmentRequestObject) (GetExperimentAssignmentResponseObject, error) {
	unitID := req.Params.UnitId
	if unitID == "" || len(unitID) > flags.MaxUnitIDBytes {
		return nil, flaghttp.InvalidRequest(fmt.Sprintf("unit_id must be 1 to %d bytes", flags.MaxUnitIDBytes))
	}
	as, err := a.h.GetExperimentAssignment.Handle(ctx, projectID(ctx), req.Experiment, unitID)
	if err != nil {
		return nil, err
	}
	return GetExperimentAssignment200JSONResponse{
		Experiment: req.Experiment, UnitType: as.UnitType, UnitId: as.UnitID,
		InclusionSalt: as.InclusionSalt, VariationSalt: as.VariationSalt,
		InclusionBucket: as.InclusionBucket, VariationBucket: as.VariationBucket,
		TrafficAllocation: as.TrafficAllocation, Included: as.Included, Variation: as.Variation, Sql: as.SQL,
	}, nil
}

// CheckHashUniformity runs the uniformity check on the unit IDs sent.
func (a *api) CheckHashUniformity(_ context.Context, req CheckHashUniformityRequestObject) (CheckHashUniformityResponseObject, error) {
	b := req.Body
	u, err := a.h.CheckHashUniformity.Handle(appexperiments.UniformityCommand{
		UnitIDs: b.UnitIds, Salt: b.Salt, SecondSalt: b.SecondSalt, Bins: b.Bins,
	})
	if err != nil {
		return nil, err
	}
	out := CheckHashUniformity200JSONResponse{
		Salt: u.Salt, SecondSalt: u.SecondSalt, Bins: u.Bins, Counts: u.Counts, Min: u.Min, Max: u.Max,
		ChiSquare: u.ChiSquare, Df: u.DF, PValue: u.PValue, Verdict: HashCheckVerdict(u.Verdict),
	}
	if i := u.Independence; i != nil {
		out.Independence = &HashCheckIndependence{
			ChiSquare: i.ChiSquare, Df: i.DF, PValue: i.PValue, Verdict: HashCheckIndependenceVerdict(i.Verdict),
		}
	}
	return out, nil
}
