package management

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/events"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// GetExperimentExposures answers what an experiment's exposures in one
// environment came to.
func (a *api) GetExperimentExposures(ctx context.Context, req GetExperimentExposuresRequestObject) (GetExperimentExposuresResponseObject, error) {
	x, err := a.h.GetExperimentExposures.Handle(ctx, appevents.ExposureQuery{
		Project: projectID(ctx), Experiment: req.Experiment, Environment: req.Params.Environment,
	})
	if err != nil {
		return nil, err
	}
	// The exposures answer found the experiment and the environment, so the
	// check is read of the same two.
	check, err := a.h.GetSampleRatio.Handle(ctx, projectID(ctx), req.Experiment, req.Params.Environment)
	if err != nil {
		return nil, err
	}
	out := GetExperimentExposures200JSONResponse{
		SampleRatio: sampleRatioOf(check),
		Variations: each(x.Variations, func(v events.VariationExposures) VariationExposures {
			return VariationExposures{
				Variation: v.Variation, Exposures: v.Exposures, Overridden: v.Overridden, FirstExposures: v.FirstExposures,
				ObservedShare: v.ObservedShare, ExpectedShare: v.ExpectedShare,
			}
		}),
		Conflicts:  ConflictTotals{Total: x.ConflictTotal, Units: x.ConflictUnits},
		Rejections: []ExposureRejections{},
		AsOf:       x.AsOf,
	}
	for reason, n := range x.Rejections {
		out.Rejections = append(out.Rejections, ExposureRejections{Reason: string(reason), Count: n})
	}
	slices.SortFunc(out.Rejections, func(a, b ExposureRejections) int { return strings.Compare(a.Reason, b.Reason) })
	return out, nil
}

// GetExperimentExposureTimeline answers an experiment's exposure counts per
// day in one environment.
func (a *api) GetExperimentExposureTimeline(ctx context.Context, req GetExperimentExposureTimelineRequestObject,
) (GetExperimentExposureTimelineResponseObject, error) {
	days, err := a.h.GetExposureTimeline.Handle(ctx, appevents.ExposureQuery{
		Project: projectID(ctx), Experiment: req.Experiment, Environment: req.Params.Environment,
	}, dayOf(req.Params.From), dayOf(req.Params.To))
	if err != nil {
		return nil, err
	}
	return GetExperimentExposureTimeline200JSONResponse{Items: each(days, func(d events.ExposureTimelineDay) ExposureDay {
		return ExposureDay{
			Day: openapi_types.Date{Time: d.Day}, Variation: d.Variation, Exposures: d.Exposures, Overridden: d.Overridden,
			FirstExposures: d.FirstExposures,
		}
	})}, nil
}

// dayOf returns the start of d, nil for nil.
func dayOf(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

// GetExperimentConflicts answers a sample of an experiment's conflicting
// units in one environment.
func (a *api) GetExperimentConflicts(ctx context.Context, req GetExperimentConflictsRequestObject) (GetExperimentConflictsResponseObject, error) {
	conflicts, err := a.h.GetConflicts.Handle(ctx, appevents.ExposureQuery{
		Project: projectID(ctx), Experiment: req.Experiment, Environment: req.Params.Environment,
	})
	if err != nil {
		return nil, err
	}
	return GetExperimentConflicts200JSONResponse{Items: each(conflicts, func(c events.Conflict) ExposureConflict {
		return ExposureConflict{
			UnitType: c.UnitType, UnitId: c.UnitID, FirstVariation: c.FirstVariation,
			ConflictingVariation: c.ConflictingVariation, ConflictingAt: c.ConflictingAt,
		}
	})}, nil
}

// GetFlagEvaluations answers a flag's evaluations per day in one
// environment.
func (a *api) GetFlagEvaluations(ctx context.Context, req GetFlagEvaluationsRequestObject) (GetFlagEvaluationsResponseObject, error) {
	days, err := a.h.GetFlagEvaluations.Handle(ctx, appevents.EvaluationQuery{
		Project: projectID(ctx), Flag: req.Flag, Environment: req.Environment,
		From: dayOf(req.Params.From), To: dayOf(req.Params.To),
	})
	var rangeErr *events.RangeError
	if errors.As(err, &rangeErr) {
		return nil, flaghttp.InvalidRequest(rangeErr.Error())
	}
	if err != nil {
		return nil, err
	}
	return GetFlagEvaluations200JSONResponse{Items: each(days, func(d events.FlagEvaluationDay) FlagEvaluationDay {
		out := FlagEvaluationDay{
			Day: openapi_types.Date{Time: d.Day}, Source: EvaluationSource(d.Source), Reason: d.Reason, Evaluations: d.Evaluations,
		}
		if d.Variation != "" {
			v := d.Variation
			out.Variation = &v
		}
		return out
	})}, nil
}
