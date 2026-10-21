package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// GetFlag reads a flag with its configuration in every environment.
type GetFlag struct {
	snapshots Snapshotter
	store     FlagReader
}

// NewGetFlag returns the handler.
func NewGetFlag(snapshots Snapshotter, store FlagReader) *GetFlag {
	return &GetFlag{snapshots: snapshots, store: store}
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
		v = FlagView{Flag: f, Configurations: configs}
		return err
	})
	return v, err
}

// FlagPage asks for one page of a project's flags, by key.
type FlagPage struct {
	// Archived lists the archived flags instead of the others.
	Archived bool
	// After is the key of the last flag of the previous page, "" for the
	// first.
	After string
	Limit int
}

// FlagLister lists a project's flags.
type FlagLister interface {
	// Flags returns at most limit of project's flags, archived or not as
	// archived says, with a key after after, by key in byte order.
	Flags(ctx context.Context, project workspace.ProjectID, archived bool, after string, limit int) ([]flags.Flag, error)
}

// ListFlags lists a project's flags a page at a time.
type ListFlags struct{ store FlagLister }

// NewListFlags returns the handler.
func NewListFlags(store FlagLister) *ListFlags { return &ListFlags{store: store} }

// Handle returns one page of project's flags and whether another follows.
// Paging by key, a flag created meanwhile never shifts a page.
func (h *ListFlags) Handle(ctx context.Context, project workspace.ProjectID, p FlagPage) ([]flags.Flag, bool, error) {
	fs, err := h.store.Flags(ctx, project, p.Archived, p.After, p.Limit+1)
	if err != nil || len(fs) <= p.Limit {
		return fs, false, err
	}
	return fs[:p.Limit], true, nil
}
