package flags

import (
	"context"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// StaleFlagPage asks for one page of a project's stale flags, by key in
// byte order.
type StaleFlagPage struct {
	// Reasons lists only the flags stale for one of them; empty for every
	// stale flag.
	Reasons []flags.StaleReason
	// After is the key the page starts after, "" for the first page.
	After string
	Limit int
}

// StaleFlag is a stale flag with its configurations, when SDKs last
// evaluated it, and why it is stale.
type StaleFlag struct {
	FlagView
	flags.Staleness
}

// UnarchivedFlagReader reads a project's flags that are not archived.
type UnarchivedFlagReader interface {
	// UnarchivedFlags returns up to limit of project's flags that are not
	// archived, each with its configuration in every environment, by key
	// in byte order, those whose key is greater than after when it is not
	// "".
	UnarchivedFlags(ctx context.Context, project workspace.ProjectID, after string, limit int) ([]FlagView, error)
}

// SummaryDays reads when a project last received an SDK's evaluation
// summary, which the events context counts.
type SummaryDays interface {
	// LastSummaryDay returns the latest UTC day project received an SDK's
	// evaluation summary, nil for never.
	LastSummaryDay(ctx context.Context, project workspace.ProjectID) (*time.Time, error)
}

// RunningExperiments reads a project's running experiments, which the
// experiments context holds.
type RunningExperiments interface {
	// RunningExperimentsOf returns project's running experiments.
	RunningExperimentsOf(ctx context.Context, project workspace.ProjectID) ([]experiments.Experiment, error)
}

// StaleDeps are what ListStaleFlags reads.
type StaleDeps struct {
	Snapshots   Snapshotter
	Store       UnarchivedFlagReader
	Evaluations LastEvaluations
	Summaries   SummaryDays
	Experiments RunningExperiments
	Owners      Owners
	Clock       clock.Clock
}

// ListStaleFlags lists a project's stale flags a page at a time.
type ListStaleFlags struct{ d StaleDeps }

// NewListStaleFlags returns the handler.
func NewListStaleFlags(d StaleDeps) *ListStaleFlags { return &ListStaleFlags{d: d} }

// staleBatch is how many flags ListStaleFlags judges per read: stale flags
// are a few of a project's, so a page reads past many fresh ones.
const staleBatch = 200

// Handle returns one page of project's stale flags, and the key to start the
// next page after, nil on the last page. Whether a flag is stale is judged
// by the domain from facts several contexts hold, so flags are read by key
// in batches and judged until the page is full, all in one snapshot.
func (h *ListStaleFlags) Handle(ctx context.Context, project workspace.ProjectID, p StaleFlagPage) ([]StaleFlag, *string, error) {
	now := h.d.Clock.Now().UTC()
	var page []StaleFlag
	err := h.d.Snapshots.Snapshot(ctx, func(ctx context.Context) error {
		j, err := h.judge(ctx, project, now)
		if err != nil {
			return err
		}
		for after := p.After; ; {
			batch, err := h.d.Store.UnarchivedFlags(ctx, project, after, staleBatch)
			if err != nil {
				return err
			}
			if err = j.addEvaluations(ctx, h.d.Evaluations, batch); err != nil {
				return err
			}
			for _, v := range batch {
				s, err := j.staleness(ctx, h.d.Owners, v, now)
				if err != nil {
					return err
				}
				if len(s.Reasons) > 0 && (len(p.Reasons) == 0 || slices.ContainsFunc(s.Reasons, func(r flags.StaleReason) bool {
					return slices.Contains(p.Reasons, r)
				})) {
					page = append(page, StaleFlag{FlagView: v, Staleness: s})
					if len(page) > p.Limit {
						return nil
					}
				}
			}
			if len(batch) < staleBatch {
				return nil
			}
			after = batch[len(batch)-1].Flag.Key
		}
	})
	if err != nil || len(page) <= p.Limit {
		return page, nil, err
	}
	page = page[:p.Limit]
	next := page[len(page)-1].Flag.Key
	return page, &next, nil
}

// staleJudge holds the facts a page of flags is judged from that are the
// project's rather than one flag's, and the owners judged so far.
type staleJudge struct {
	project        workspace.ProjectID
	lastSummaryDay *time.Time
	overdue        map[flags.FlagID][]flags.OverdueExperiment
	owners         map[access.MemberID]bool
}

// judge reads the project's facts at now.
func (h *ListStaleFlags) judge(ctx context.Context, project workspace.ProjectID, now time.Time) (*staleJudge, error) {
	j := &staleJudge{project: project, overdue: map[flags.FlagID][]flags.OverdueExperiment{}, owners: map[access.MemberID]bool{}}
	var err error
	if j.lastSummaryDay, err = h.d.Summaries.LastSummaryDay(ctx, project); err != nil {
		return nil, err
	}
	running, err := h.d.Experiments.RunningExperimentsOf(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, e := range running {
		if e.PastExpectedEnd(now) {
			j.overdue[e.Flag] = append(j.overdue[e.Flag], flags.OverdueExperiment{Key: e.Key, ExpectedEndAt: *e.ExpectedEndAt})
		}
	}
	return j, nil
}

// addEvaluations sets in batch when each flag was last evaluated.
func (j *staleJudge) addEvaluations(ctx context.Context, evaluations LastEvaluations, batch []FlagView) error {
	ids := make([]flags.FlagID, len(batch))
	for i, v := range batch {
		ids[i] = v.Flag.ID
	}
	last, err := evaluations.LastEvaluated(ctx, ids)
	if err != nil {
		return err
	}
	for i := range batch {
		batch[i].LastEvaluated = last[batch[i].Flag.ID]
	}
	return nil
}

// staleness judges v at now, asking owners about an owner not judged yet.
func (j *staleJudge) staleness(ctx context.Context, owners Owners, v FlagView, now time.Time) (flags.Staleness, error) {
	active, ok := j.owners[v.Flag.Owner]
	if !ok {
		var err error
		if active, err = owners.MayOwn(ctx, j.project, v.Flag.Owner); err != nil {
			return flags.Staleness{}, err
		}
		j.owners[v.Flag.Owner] = active
	}
	facts := flags.StaleFacts{
		Flag: v.Flag, LastSummaryDay: j.lastSummaryDay, OwnerActive: active, OverdueExperiments: j.overdue[v.Flag.ID],
		Configurations: make([]flags.Configuration, len(v.Configurations)),
	}
	for i, c := range v.Configurations {
		facts.Configurations[i] = c.Configuration
	}
	for _, at := range v.LastEvaluated {
		if facts.LastEvaluatedAt == nil || at.After(*facts.LastEvaluatedAt) {
			facts.LastEvaluatedAt = &at
		}
	}
	return flags.JudgeStale(facts, now), nil
}
