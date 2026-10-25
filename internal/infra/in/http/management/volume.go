package management

import (
	"context"
	"errors"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/events"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// GetEventVolume answers the events the project received per day and the
// gaps retention left over the same days.
func (a *api) GetEventVolume(ctx context.Context, req GetEventVolumeRequestObject) (GetEventVolumeResponseObject, error) {
	v, err := a.h.GetEventVolume.Handle(ctx, appevents.VolumeQuery{
		Project: projectID(ctx), From: dayOf(req.Params.From), To: dayOf(req.Params.To),
	})
	var rangeErr *events.RangeError
	if errors.As(err, &rangeErr) {
		return nil, flaghttp.InvalidRequest(rangeErr.Error())
	}
	if err != nil {
		return nil, err
	}
	return GetEventVolume200JSONResponse{
		Days: each(v.Days, func(d events.VolumeDay) EventVolumeDay {
			return EventVolumeDay{Day: openapi_types.Date{Time: d.Day}, Type: EventType(d.Type), Events: d.Events}
		}),
		Gaps: each(v.Gaps, func(g events.Gap) EventGap {
			return EventGap{
				Stream: string(g.Stream), Type: EventType(g.EventType), PeriodStart: g.PeriodStart, PeriodEnd: g.PeriodEnd,
				Rows: g.Rows, DroppedAt: g.DroppedAt,
			}
		}),
	}, nil
}
