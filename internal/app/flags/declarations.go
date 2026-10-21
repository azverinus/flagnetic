package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// DeclarationReferenceFinder finds what names a unit type or attribute.
type DeclarationReferenceFinder interface {
	// DeclarationReferences returns the segments whose conditions name the
	// declaration key, then one reference per flag and environment whose
	// rules, rollouts or overrides name it, archived flags included.
	DeclarationReferences(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error)
}

// DeclarationGuard keeps every rule, rollout, override and segment naming a
// declared unit type or attribute: the workspace context asks it before
// deleting one.
type DeclarationGuard struct{ store DeclarationReferenceFinder }

// NewDeclarationGuard returns the handler.
func NewDeclarationGuard(store DeclarationReferenceFinder) *DeclarationGuard {
	return &DeclarationGuard{store: store}
}

// RefuseReferenced answers in_use, listing what names it, when a segment
// or a flag's configuration of project names the unit type or attribute
// keyed key, archived flags included, since a restored flag would come back
// naming something undeclared; nil when nothing does. The caller holds the
// project's lock, which every write naming a declaration takes too, so the
// answer holds until its unit of work ends.
func (g *DeclarationGuard) RefuseReferenced(ctx context.Context, project workspace.ProjectID, key string) error {
	refs, err := g.store.DeclarationReferences(ctx, project, key)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return flags.ErrInUse(refs)
	}
	return nil
}

// DeclarationUsageCounter counts what names each unit type and attribute.
type DeclarationUsageCounter interface {
	// DeclarationUsageCounts returns, by declaration key, how many flags
	// and segments of project name it; a key nothing names is absent.
	DeclarationUsageCounts(ctx context.Context, project workspace.ProjectID) (map[string]int, error)
}

// DeclarationUsages tells the workspace context how many flags and segments
// name each of a project's unit types and attributes, for its lists.
type DeclarationUsages struct{ store DeclarationUsageCounter }

// NewDeclarationUsages returns the handler.
func NewDeclarationUsages(store DeclarationUsageCounter) *DeclarationUsages {
	return &DeclarationUsages{store: store}
}

// DeclarationUsageCounts returns, by declaration key, how many flags,
// archived ones included, and segments of project name it, each counted
// once; a key nothing names is absent. It reads in the unit of work or
// snapshot ctx carries.
func (h *DeclarationUsages) DeclarationUsageCounts(ctx context.Context, project workspace.ProjectID) (map[string]int, error) {
	return h.store.DeclarationUsageCounts(ctx, project)
}
