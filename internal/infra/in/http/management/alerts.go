package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListExperimentAlerts answers an experiment's alerts.
func (a *api) ListExperimentAlerts(ctx context.Context, req ListExperimentAlertsRequestObject) (ListExperimentAlertsResponseObject, error) {
	alerts, err := a.h.ListExperimentAlerts.Handle(ctx, projectID(ctx), req.Experiment, req.Params.Active)
	if err != nil {
		return nil, err
	}
	return ListExperimentAlerts200JSONResponse{Items: each(alerts, alertOf)}, nil
}

// AcknowledgeExperimentAlert acknowledges an experiment's alert.
func (a *api) AcknowledgeExperimentAlert(ctx context.Context, req AcknowledgeExperimentAlertRequestObject,
) (AcknowledgeExperimentAlertResponseObject, error) {
	alert, err := a.h.AcknowledgeExperimentAlert.Handle(ctx, appexperiments.AcknowledgeAlertCommand{
		Project: projectID(ctx), Key: req.Experiment, Alert: id.FromBytes[experiments.Alert](req.Alert), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return AcknowledgeExperimentAlert200JSONResponse(alertOf(alert)), nil
}

func alertOf(v appexperiments.AlertView) ExperimentAlert {
	out := ExperimentAlert{
		Id: openapi_types.UUID(v.ID.Bytes()), Kind: ExperimentAlertKind(v.Kind), Environment: v.EnvironmentKey,
		RaisedAt: v.RaisedAt, ClearedAt: v.ClearedAt, AcknowledgedAt: v.AcknowledgedAt,
	}
	if r := v.Details.SampleRatio; r != nil && r.PValue != nil {
		out.Details.SampleRatio = &SampleRatioAlertDetails{
			N: r.N, ChiSquare: *r.ChiSquare, PValue: *r.PValue,
			Variations: each(r.Variations, func(v experiments.SampleRatioVariation) SampleRatioVariation {
				return SampleRatioVariation{
					Variation: v.Variation, Observed: v.Observed, Expected: v.Expected,
					ObservedShare: v.ObservedShare, ExpectedShare: v.ExpectedShare,
				}
			}),
		}
	}
	return out
}

// sampleRatioOf is the latest sample-ratio check as answered, nil before the
// first.
func sampleRatioOf(st *appexperiments.SampleRatioState) *SampleRatioCheck {
	if st == nil {
		return nil
	}
	out := &SampleRatioCheck{N: st.N, ChiSquare: st.ChiSquare, PValue: st.PValue, Active: st.Active, CheckedAt: st.CheckedAt}
	if st.Skipped != "" {
		reason := SampleRatioSkippedReason(st.Skipped)
		out.SkippedReason = &reason
	}
	return out
}
