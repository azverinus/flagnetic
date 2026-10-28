package compose

import (
	"context"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

// staleFlagReportPage is how many stale flags the digest reads at a time.
const staleFlagReportPage = 200

// staleFlagReport gives the governance context the stale flags report of the
// flags context, which it may not import.
type staleFlagReport struct{ list *appflags.ListStaleFlags }

var _ appgovernance.StaleFlagReport = staleFlagReport{}

// StaleFlags returns every stale flag of project. The report reads in a
// snapshot of its own, so it runs outside the unit of work the digest round
// holds its lock in.
func (r staleFlagReport) StaleFlags(ctx context.Context, project workspace.ProjectID) ([]governance.StaleFlagItem, error) {
	ctx = pgxtx.Outside(ctx)
	items := []governance.StaleFlagItem{}
	page := appflags.StaleFlagPage{Limit: staleFlagReportPage}
	for {
		stale, next, err := r.list.Handle(ctx, project, page)
		if err != nil {
			return nil, err
		}
		for _, f := range stale {
			items = append(items, staleFlagItem(f))
		}
		if next == nil {
			return items, nil
		}
		page.After = *next
	}
}

func staleFlagItem(f appflags.StaleFlag) governance.StaleFlagItem {
	item := governance.StaleFlagItem{
		Key: f.Flag.Key, Name: f.Flag.Name, Owner: f.Flag.Owner.String(), Lifetime: string(f.Flag.Lifetime),
		ExpectedRemovalAt: f.Flag.ExpectedRemovalAt,
		Reasons:           make([]string, len(f.Reasons)),
		LastEvaluated:     make([]governance.LastEvaluated, len(f.Configurations)),
	}
	for i, r := range f.Reasons {
		item.Reasons[i] = string(r)
	}
	for i, c := range f.Configurations {
		item.LastEvaluated[i] = governance.LastEvaluated{Environment: c.Environment}
		if at, ok := f.LastEvaluated[c.Environment]; ok {
			item.LastEvaluated[i].LastEvaluatedAt = &at
		}
	}
	if e := f.Experiment; e != nil {
		item.Experiment = &governance.StaleExperiment{Key: e.Key, ExpectedEndAt: e.ExpectedEndAt.Format("2006-01-02")}
	}
	return item
}
