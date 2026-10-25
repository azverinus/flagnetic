package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// GetFlag reads a flag with its configuration in every environment.
type GetFlag struct {
	snapshots   Snapshotter
	store       FlagReader
	evaluations LastEvaluations
}

// NewGetFlag returns the handler.
func NewGetFlag(snapshots Snapshotter, store FlagReader, evaluations LastEvaluations) *GetFlag {
	return &GetFlag{snapshots: snapshots, store: store, evaluations: evaluations}
}

// Handle returns project's flag with key, or not_found. The definition and
// the configurations are read in one snapshot, so they are never those of a
// flag deleted and created again in between.
func (h *GetFlag) Handle(ctx context.Context, project workspace.ProjectID, key string) (FlagView, error) {
	var v FlagView
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		f, err := h.store.FlagByKey(ctx, project, key)
		if err != nil {
			return flagNotFound(err)
		}
		configs, err := h.store.Configurations(ctx, f.ID)
		if err != nil {
			return err
		}
		v = FlagView{Flag: f, Configurations: configs}
		v.LastEvaluated, err = lastEvaluated(ctx, h.evaluations, f.ID)
		return err
	})
	return v, err
}

// FlagSort is the order flags are listed in.
type FlagSort string

// The orders flags are listed in: by key or by name in byte order, or by
// the last change to the definition, the most recent first; ties by key.
const (
	SortByKey     FlagSort = "key"
	SortByName    FlagSort = "name"
	SortByUpdated FlagSort = "updated"
)

// StateFilter is a flag state flags are listed by within one environment.
type StateFilter string

// The flag states flags are listed by: on, off, with rules whether on or
// off, with a rule serving a rollout, with a rule serving an experiment.
const (
	StateOn      StateFilter = "on"
	StateOff     StateFilter = "off"
	StateRules   StateFilter = "rules"
	StateRollout StateFilter = "rollout"
	// StateExperiment lists the flags with a rule serving an experiment.
	StateExperiment StateFilter = "experiment"
)

// FlagCursor is where a page of flags starts: after the flag with these
// key, name and last change, in the page's order.
type FlagCursor struct {
	Key       string
	Name      string
	UpdatedAt time.Time
}

// cursorAfter returns the cursor after f.
func cursorAfter(f flags.Flag) FlagCursor {
	return FlagCursor{Key: f.Key, Name: f.Name, UpdatedAt: f.UpdatedAt}
}

// FlagPage asks for one page of a project's flags.
type FlagPage struct {
	// Text is contained in the key or the name of every flag listed,
	// ignoring case; "" for any.
	Text string
	// Type, Tag and Owner, when set, are every listed flag's.
	Type  flags.Type
	Tag   string
	Owner *access.MemberID
	// Archived lists the archived flags instead of the others.
	Archived bool
	// Environment is the key of the environment State is judged in; ""
	// for none. State is "" for any.
	Environment string
	State       StateFilter
	// DiffersBetween are the keys of two environments every listed flag's
	// configurations differ between in what their rulesets carry; nil for
	// no comparison.
	DiffersBetween *[2]string
	Sort           FlagSort
	// After is where the page starts, nil for the first.
	After *FlagCursor
	Limit int
}

// FlagState is what a flag's configuration in one environment serves at
// a glance.
type FlagState struct {
	Environment string
	On          bool
	HasRules    bool
	HasRollout  bool
	// HasExperiment is true when a rule serves an experiment.
	HasExperiment bool
	// LastEvaluatedAt is when an SDK last evaluated the flag in the
	// environment, nil when none ever did.
	LastEvaluatedAt *time.Time
}

// ListedFlag is a flag's definition with its flag state in every
// environment, by the environments' order.
type ListedFlag struct {
	Flag   flags.Flag
	States []FlagState
}

// FlagQuery is what the store lists: FlagPage with its environments
// found.
type FlagQuery struct {
	Project  workspace.ProjectID
	Text     string
	Type     flags.Type
	Tag      string
	Owner    *access.MemberID
	Archived bool
	// In is the environment State is judged in, nil for no state filter.
	In    *workspace.EnvironmentID
	State StateFilter
	// Compared are the two environments whose configurations a comparison
	// reads, nil for none.
	Compared *[2]workspace.EnvironmentID
	Sort     FlagSort
	After    *FlagCursor
	// Limit is how many flags the store answers at most, 0 for every one.
	Limit int
}

// QueriedFlag is a flag the store lists, with its configurations in the
// two environments the query compares when it compares two.
type QueriedFlag struct {
	ListedFlag
	Compared [2]flags.Configuration
}

// FlagLister lists a project's flags.
type FlagLister interface {
	// Flags calls each with the flags q lists, in q's order, until each
	// answers false or the flags run out, all in one query.
	Flags(ctx context.Context, q FlagQuery, each func(QueriedFlag) bool) error
}

// ListFlags lists a project's flags a page at a time.
type ListFlags struct {
	snapshots    Snapshotter
	environments Environments
	store        FlagLister
	evaluations  LastEvaluations
}

// NewListFlags returns the handler, which finds the environments a page
// names in environments.
func NewListFlags(snapshots Snapshotter, environments Environments, store FlagLister, evaluations LastEvaluations) *ListFlags {
	return &ListFlags{snapshots: snapshots, environments: environments, store: store, evaluations: evaluations}
}

// Handle returns one page of project's flags and the cursor of the next,
// nil on the last. Paging by the sort's own values, a flag created
// meanwhile never shifts a page. The environments are found and the flags
// read in one snapshot, at most two queries whatever the page's size; a
// comparison reads past the page's end in the same query, skipping the
// flags whose configurations carry the same ruleset in both environments,
// since only the domain judges that. When each flag was last evaluated is
// read in one more query.
func (h *ListFlags) Handle(ctx context.Context, project workspace.ProjectID, p FlagPage) ([]ListedFlag, *FlagCursor, error) {
	q := FlagQuery{
		Project: project, Text: p.Text, Type: p.Type, Tag: p.Tag, Owner: p.Owner, Archived: p.Archived,
		State: p.State, Sort: p.Sort, After: p.After, Limit: p.Limit + 1,
	}
	var page []ListedFlag
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		if p.State != "" || p.Environment != "" || p.DiffersBetween != nil {
			if err := h.findEnvironments(ctx, project, p, &q); err != nil {
				return err
			}
		}
		if q.Compared != nil {
			q.Limit = 0
		}
		err := h.store.Flags(ctx, q, func(f QueriedFlag) bool {
			if q.Compared == nil || !f.Compared[0].SameRuleset(f.Compared[1]) {
				page = append(page, f.ListedFlag)
			}
			return len(page) <= p.Limit
		})
		if err != nil {
			return err
		}
		return h.addLastEvaluated(ctx, page[:min(len(page), p.Limit)])
	})
	if err != nil || len(page) <= p.Limit {
		return page, nil, err
	}
	page = page[:p.Limit]
	next := cursorAfter(page[len(page)-1].Flag)
	return page, &next, nil
}

// addLastEvaluated sets in the states of page when each flag was last
// evaluated.
func (h *ListFlags) addLastEvaluated(ctx context.Context, page []ListedFlag) error {
	ids := make([]flags.FlagID, len(page))
	for i, f := range page {
		ids[i] = f.Flag.ID
	}
	last, err := h.evaluations.LastEvaluated(ctx, ids)
	if err != nil {
		return err
	}
	for _, f := range page {
		for i, s := range f.States {
			if at, ok := last[f.Flag.ID][s.Environment]; ok {
				f.States[i].LastEvaluatedAt = &at
			}
		}
	}
	return nil
}

// findEnvironments sets in q the environments p names by key, or answers
// not_found for one project does not have.
func (h *ListFlags) findEnvironments(ctx context.Context, project workspace.ProjectID, p FlagPage, q *FlagQuery) error {
	envs, err := h.environments.Environments(ctx, project)
	if err != nil {
		return err
	}
	find := func(key string) (workspace.EnvironmentID, error) {
		for _, e := range envs {
			if e.Key == key {
				return e.ID, nil
			}
		}
		return workspace.EnvironmentID{}, errEnvironmentNotFound
	}
	if p.Environment != "" {
		var in workspace.EnvironmentID
		if in, err = find(p.Environment); err != nil {
			return err
		}
		if p.State != "" {
			q.In = &in
		}
	}
	if p.DiffersBetween != nil {
		var compared [2]workspace.EnvironmentID
		for i, key := range p.DiffersBetween {
			if compared[i], err = find(key); err != nil {
				return err
			}
		}
		q.Compared = &compared
	}
	return nil
}
