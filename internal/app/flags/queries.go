package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// GetFlag reads a flag with its configuration in every environment.
type GetFlag struct{ store FlagReader }

// NewGetFlag returns the handler.
func NewGetFlag(store FlagReader) *GetFlag { return &GetFlag{store: store} }

// Handle returns project's flag with key, or not_found.
func (h *GetFlag) Handle(ctx context.Context, project workspace.ProjectID, key string) (FlagView, error) {
	f, err := h.store.FlagByKey(ctx, project, key)
	if err != nil {
		return FlagView{}, flagNotFound(err)
	}
	configs, err := h.store.Configurations(ctx, f.ID)
	return FlagView{Flag: f, Configurations: configs}, err
}

// FlagPage asks for one page of a project's flags, by key.
type FlagPage struct {
	// After is the key of the last flag of the previous page, "" for the
	// first.
	After string
	Limit int
}

// FlagLister lists a project's flags.
type FlagLister interface {
	// Flags returns at most limit of project's flags with a key after
	// after, by key in byte order.
	Flags(ctx context.Context, project workspace.ProjectID, after string, limit int) ([]flags.Flag, error)
}

// ListFlags lists a project's flags a page at a time.
type ListFlags struct{ store FlagLister }

// NewListFlags returns the handler.
func NewListFlags(store FlagLister) *ListFlags { return &ListFlags{store: store} }

// Handle returns one page of project's flags and whether another follows.
// Paging by key, a flag created meanwhile never shifts a page.
func (h *ListFlags) Handle(ctx context.Context, project workspace.ProjectID, p FlagPage) ([]flags.Flag, bool, error) {
	fs, err := h.store.Flags(ctx, project, p.After, p.Limit+1)
	if err != nil || len(fs) <= p.Limit {
		return fs, false, err
	}
	return fs[:p.Limit], true, nil
}
