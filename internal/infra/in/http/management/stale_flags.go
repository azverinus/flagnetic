package management

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// The size of a page of stale flags.
const (
	defaultStaleFlagLimit = 50
	maxStaleFlagLimit     = 200
)

// ListStaleFlags answers one page of the project's stale flags.
func (a *api) ListStaleFlags(ctx context.Context, req ListStaleFlagsRequestObject) (ListStaleFlagsResponseObject, error) {
	page := appflags.StaleFlagPage{Limit: defaultStaleFlagLimit}
	for _, r := range valueOr(req.Params.Reason, nil) {
		reason := flags.StaleReason(r)
		if !slices.Contains(flags.StaleReasons, reason) {
			return nil, flaghttp.InvalidRequest(
				"reason must be one of lifetime_exceeded, not_evaluated, owner_inactive, finished_rollout and experiment_overdue")
		}
		page.Reasons = append(page.Reasons, reason)
	}
	if l := req.Params.Limit; l != nil {
		if *l < 1 || *l > maxStaleFlagLimit {
			return nil, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxStaleFlagLimit))
		}
		page.Limit = *l
	}
	if c := req.Params.Cursor; c != nil {
		after, err := decodeKeyCursor(*c)
		if err != nil {
			return nil, err
		}
		page.After = after
	}
	list, next, err := a.h.ListStaleFlags.Handle(ctx, projectID(ctx), page)
	if err != nil {
		return nil, err
	}
	out := ListStaleFlags200JSONResponse{Items: each(list, staleFlagOut)}
	if next != nil {
		cursor := encodeKeyCursor(*next)
		out.NextCursor = &cursor
	}
	return out, nil
}

func staleFlagOut(f appflags.StaleFlag) StaleFlag {
	d := flagDefinitionOut(f.Flag)
	out := StaleFlag{
		Key: d.Key, Name: d.Name, Owner: d.Owner, Lifetime: d.Lifetime, ExpectedRemovalAt: d.ExpectedRemovalAt,
		Reasons:       each(f.Reasons, func(r flags.StaleReason) StaleReason { return StaleReason(r) }),
		LastEvaluated: lastEvaluatedOut(f.FlagView),
	}
	if e := f.Experiment; e != nil {
		out.Experiment = &StaleExperiment{Key: e.Key, ExpectedEndAt: openapi_types.Date{Time: e.ExpectedEndAt}}
	}
	return out
}

// SendStaleFlagDigest queues the project's digest for the caller and answers
// the stale flags it lists.
func (a *api) SendStaleFlagDigest(ctx context.Context, _ SendStaleFlagDigestRequestObject) (SendStaleFlagDigestResponseObject, error) {
	items, err := a.h.SendStaleFlagDigest.Handle(ctx, appgovernance.SendStaleFlagDigestCommand{
		Project: projectID(ctx), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return SendStaleFlagDigest202JSONResponse{Items: each(items, staleItemOut)}, nil
}

func staleItemOut(i governance.StaleFlagItem) StaleFlag {
	owner, _ := uuid.Parse(i.Owner)
	out := StaleFlag{
		Key: i.Key, Name: i.Name, Owner: owner, Lifetime: Lifetime(i.Lifetime),
		ExpectedRemovalAt: i.ExpectedRemovalAt,
		Reasons:           each(i.Reasons, func(r string) StaleReason { return StaleReason(r) }),
		LastEvaluated: each(i.LastEvaluated, func(e governance.LastEvaluated) FlagLastEvaluated {
			return FlagLastEvaluated{Environment: e.Environment, LastEvaluatedAt: e.LastEvaluatedAt}
		}),
	}
	if e := i.Experiment; e != nil {
		day, _ := time.Parse(time.DateOnly, e.ExpectedEndAt)
		out.Experiment = &StaleExperiment{Key: e.Key, ExpectedEndAt: openapi_types.Date{Time: day}}
	}
	return out
}
