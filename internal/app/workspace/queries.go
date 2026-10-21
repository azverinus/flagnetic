package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// FindProject finds a project by the key a request names.
type FindProject struct {
	store ProjectFinder
}

// NewFindProject returns the handler.
func NewFindProject(store ProjectFinder) *FindProject { return &FindProject{store: store} }

// Handle returns the project with key, or ErrNotFound.
func (h *FindProject) Handle(ctx context.Context, key string) (workspace.Project, error) {
	return h.store.ProjectByKey(ctx, key)
}

// ProjectLister lists projects.
type ProjectLister interface {
	// Projects returns every project when all is set, else those of ids,
	// ordered by key.
	Projects(ctx context.Context, all bool, ids []workspace.ProjectID) ([]workspace.Project, error)
}

// ListProjects lists the projects a caller works in.
type ListProjects struct {
	store ProjectLister
}

// NewListProjects returns the handler.
func NewListProjects(store ProjectLister) *ListProjects { return &ListProjects{store: store} }

// ListProjectsQuery says which projects to list: every one, or those of
// Only.
type ListProjectsQuery struct {
	All  bool
	Only []workspace.ProjectID
}

// Handle returns the projects, ordered by key.
func (h *ListProjects) Handle(ctx context.Context, q ListProjectsQuery) ([]workspace.Project, error) {
	return h.store.Projects(ctx, q.All, q.Only)
}

// List lists what a project holds of one kind, such as its environments.
type List[T any] struct {
	list func(ctx context.Context, project workspace.ProjectID) ([]T, error)
}

// Handle returns what project holds, in the kind's order.
func (h *List[T]) Handle(ctx context.Context, project workspace.ProjectID) ([]T, error) {
	return h.list(ctx, project)
}

// Used is a unit type or attribute with how many flags and segments name
// it.
type Used[T any] struct {
	Declaration T
	UsageCount  int
}

// ListUsed lists a project's unit types or attributes with how many flags
// and segments name each.
type ListUsed[T any] struct {
	snapshots Snapshotter
	list      func(ctx context.Context, project workspace.ProjectID) ([]T, error)
	key       func(T) string
	usages    DeclarationUsages
}

// Handle returns project's declarations of the kind by key, each with its
// usage count. The declarations and the counts are read in one snapshot, so
// a count is never that of a declaration deleted and created again in
// between, and the counts of the whole list cost one query.
func (h *ListUsed[T]) Handle(ctx context.Context, project workspace.ProjectID) ([]Used[T], error) {
	var out []Used[T]
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		ds, err := h.list(ctx, project)
		if err != nil {
			return err
		}
		counts, err := h.usages.DeclarationUsageCounts(ctx, project)
		if err != nil {
			return err
		}
		out = make([]Used[T], len(ds))
		for i, d := range ds {
			out[i] = Used[T]{Declaration: d, UsageCount: counts[h.key(d)]}
		}
		return nil
	})
	return out, err
}

// Get finds one thing a project holds by its key, such as an environment.
type Get[T any] struct {
	get  func(ctx context.Context, project workspace.ProjectID, key string, lock bool) (T, error)
	what string
}

// Handle returns project's thing with key, or not_found.
func (h *Get[T]) Handle(ctx context.Context, project workspace.ProjectID, key string) (T, error) {
	v, err := h.get(ctx, project, key, false)
	return v, notFound(err, h.what)
}

// EnvironmentReader reads a project's environments.
type EnvironmentReader interface {
	// Environments returns the project's environments by sort order.
	Environments(ctx context.Context, project workspace.ProjectID) ([]workspace.Environment, error)
	EnvironmentByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Environment, error)
}

// NewListEnvironments returns the query listing a project's environments.
func NewListEnvironments(store EnvironmentReader) *List[workspace.Environment] {
	return &List[workspace.Environment]{list: store.Environments}
}

// NewGetEnvironment returns the query finding an environment by key.
func NewGetEnvironment(store EnvironmentReader) *Get[workspace.Environment] {
	return &Get[workspace.Environment]{get: store.EnvironmentByKey, what: "environment"}
}

// UnitTypeReader reads a project's unit types.
type UnitTypeReader interface {
	// UnitTypes returns the project's unit types by key.
	UnitTypes(ctx context.Context, project workspace.ProjectID) ([]workspace.UnitType, error)
	UnitTypeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.UnitType, error)
}

// NewListUnitTypes returns the query listing a project's unit types, each
// with how many flags and segments usages counts naming it.
func NewListUnitTypes(snapshots Snapshotter, store UnitTypeReader, usages DeclarationUsages) *ListUsed[workspace.UnitType] {
	return &ListUsed[workspace.UnitType]{
		snapshots: snapshots, list: store.UnitTypes, key: func(u workspace.UnitType) string { return u.Key }, usages: usages,
	}
}

// NewGetUnitType returns the query finding a unit type by key.
func NewGetUnitType(store UnitTypeReader) *Get[workspace.UnitType] {
	return &Get[workspace.UnitType]{get: store.UnitTypeByKey, what: "unit type"}
}

// AttributeReader reads a project's attributes.
type AttributeReader interface {
	// Attributes returns the project's attributes by key.
	Attributes(ctx context.Context, project workspace.ProjectID) ([]workspace.Attribute, error)
	AttributeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Attribute, error)
}

// NewListAttributes returns the query listing a project's attributes, each
// with how many flags and segments usages counts naming it.
func NewListAttributes(snapshots Snapshotter, store AttributeReader, usages DeclarationUsages) *ListUsed[workspace.Attribute] {
	return &ListUsed[workspace.Attribute]{
		snapshots: snapshots, list: store.Attributes, key: func(a workspace.Attribute) string { return a.Key }, usages: usages,
	}
}

// NewGetAttribute returns the query finding an attribute by key.
func NewGetAttribute(store AttributeReader) *Get[workspace.Attribute] {
	return &Get[workspace.Attribute]{get: store.AttributeByKey, what: "attribute"}
}
