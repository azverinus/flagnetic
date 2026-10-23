package management

import (
	"context"
	"encoding/base64"
	"fmt"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
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
	return out
}
