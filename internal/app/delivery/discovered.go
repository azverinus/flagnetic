package delivery

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// DiscoveredAttributeStore stores the attributes contexts carried that
// their project does not declare.
type DiscoveredAttributeStore interface {
	// LockDiscoveredAttributes holds, until the unit of work ends, the
	// right to add discovered attributes to project.
	LockDiscoveredAttributes(ctx context.Context, project workspace.ProjectID) error
	// DeclaredKeys returns which of keys project declares, as an attribute
	// or a unit type.
	DeclaredKeys(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error)
	// DiscoveredAttributes returns the stored attributes of project keyed
	// keys that it has not declared since, dismissed ones included.
	DiscoveredAttributes(ctx context.Context, project workspace.ProjectID, keys []string) ([]delivery.DiscoveredAttribute, error)
	// CountDiscoveredAttributes counts the stored attributes of project
	// that it has not declared since, dismissed ones included.
	CountDiscoveredAttributes(ctx context.Context, project workspace.ProjectID) (int, error)
	SaveDiscoveredAttribute(ctx context.Context, d delivery.DiscoveredAttribute) error
}

// Bounds of what a replica gathers between two writes of discovered
// attributes, so that contexts carrying ever new keys cannot grow its
// memory: what is beyond is dropped, and seen again in a later context.
const (
	// maxDiscoveryProjects is how many projects' sightings are held.
	maxDiscoveryProjects = 1024
	// maxDiscoveryPending is how many keys of one project are held.
	maxDiscoveryPending = 1000
)

// Discoveries gathers the undeclared attributes evaluated contexts carry
// and writes them in rounds, so an evaluation never waits on a write and a
// key seen a thousand times a second costs one.
type Discoveries struct {
	tx    Transactor
	store DiscoveredAttributeStore

	mu      sync.Mutex
	pending map[workspace.ProjectID]map[string]*delivery.Sighting
	dropped int
}

// NewDiscoveries returns the replica's gatherer of discovered attributes.
func NewDiscoveries(tx Transactor, store DiscoveredAttributeStore) *Discoveries {
	return &Discoveries{tx: tx, store: store, pending: map[workspace.ProjectID]map[string]*delivery.Sighting{}}
}

// Note keeps the attributes a context of project carried at at for the
// next round, as far as the bounds allow.
func (d *Discoveries) Note(project workspace.ProjectID, at time.Time, observed []delivery.ObservedAttribute) {
	if d == nil || len(observed) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	seen, ok := d.pending[project]
	if !ok {
		if len(d.pending) >= maxDiscoveryProjects {
			d.dropped += len(observed)
			return
		}
		seen = map[string]*delivery.Sighting{}
		d.pending[project] = seen
	}
	for _, o := range observed {
		s, ok := seen[o.Key]
		if !ok {
			if len(seen) >= maxDiscoveryPending {
				d.dropped++
				continue
			}
			s = &delivery.Sighting{Key: o.Key, First: at}
			seen[o.Key] = s
		}
		if !slices.Contains(s.Types, o.Type) {
			s.Types = append(s.Types, o.Type)
		}
		s.Count++
		if at.After(s.Last) {
			s.Last = at
		}
		if at.Before(s.First) {
			s.First = at
		}
	}
}

// Dropped returns how many sightings were dropped since it was last asked,
// for the round's warning.
func (d *Discoveries) Dropped() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.dropped
	d.dropped = 0
	return n
}

// Flush writes what was gathered since the last round, one unit of work per
// project. A project's sightings that cannot be written are dropped rather
// than retried: the next contexts carrying them are seen again. It returns
// the first failure.
func (d *Discoveries) Flush(ctx context.Context) error {
	d.mu.Lock()
	pending := d.pending
	d.pending = map[workspace.ProjectID]map[string]*delivery.Sighting{}
	d.mu.Unlock()
	var first error
	for project, seen := range pending {
		if err := d.write(ctx, project, seen); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// write merges the sightings of project into what is stored: a key the
// project declares meanwhile is skipped, a known key merged, and a new key
// kept while the project holds fewer than delivery.MaxDiscoveredAttributes,
// in key order so that which keys are kept does not depend on chance.
func (d *Discoveries) write(ctx context.Context, project workspace.ProjectID, seen map[string]*delivery.Sighting) error {
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return d.tx.Within(ctx, func(ctx context.Context) error {
		if err := d.store.LockDiscoveredAttributes(ctx, project); err != nil {
			return err
		}
		declared, err := d.store.DeclaredKeys(ctx, project, keys)
		if err != nil {
			return err
		}
		stored, err := d.store.DiscoveredAttributes(ctx, project, keys)
		if err != nil {
			return err
		}
		known := make(map[string]delivery.DiscoveredAttribute, len(stored))
		for _, a := range stored {
			known[a.Key] = a
		}
		count, err := d.store.CountDiscoveredAttributes(ctx, project)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if declared[k] {
				continue
			}
			s := *seen[k]
			a, ok := known[k]
			switch {
			case ok:
				a = a.Seen(s)
			case count < delivery.MaxDiscoveredAttributes:
				a = delivery.NewDiscoveredAttribute(project, s)
				count++
			default:
				continue
			}
			if err := d.store.SaveDiscoveredAttribute(ctx, a); err != nil {
				return err
			}
		}
		return nil
	})
}

// DiscoveredAttributeLister lists the discovered attributes a member sees.
type DiscoveredAttributeLister interface {
	// ListedDiscoveredAttributes returns the attributes of project neither
	// dismissed nor declared since, by key.
	ListedDiscoveredAttributes(ctx context.Context, project workspace.ProjectID) ([]delivery.DiscoveredAttribute, error)
}

// ListDiscoveredAttributes answers a project's discovered attributes.
type ListDiscoveredAttributes struct{ store DiscoveredAttributeLister }

// NewListDiscoveredAttributes returns the handler.
func NewListDiscoveredAttributes(store DiscoveredAttributeLister) *ListDiscoveredAttributes {
	return &ListDiscoveredAttributes{store: store}
}

// Handle returns the discovered attributes of project a member sees, by key.
func (h *ListDiscoveredAttributes) Handle(ctx context.Context, project workspace.ProjectID) ([]delivery.DiscoveredAttribute, error) {
	return h.store.ListedDiscoveredAttributes(ctx, project)
}

// DiscoveredAttributeEditor changes one discovered attribute.
type DiscoveredAttributeEditor interface {
	// DiscoveredAttributeForUpdate returns the attribute of project keyed
	// key, locked until the unit of work ends, or ErrNotFound when there is
	// none it has not declared since.
	DiscoveredAttributeForUpdate(ctx context.Context, project workspace.ProjectID, key string) (delivery.DiscoveredAttribute, error)
	SaveDiscoveredAttribute(ctx context.Context, d delivery.DiscoveredAttribute) error
	DeleteDiscoveredAttribute(ctx context.Context, project workspace.ProjectID, key string) error
}

// DismissDiscoveredAttribute hides a discovered attribute until it is seen
// with a new type.
type DismissDiscoveredAttribute struct {
	tx    Transactor
	store DiscoveredAttributeEditor
}

// NewDismissDiscoveredAttribute returns the handler.
func NewDismissDiscoveredAttribute(tx Transactor, store DiscoveredAttributeEditor) *DismissDiscoveredAttribute {
	return &DismissDiscoveredAttribute{tx: tx, store: store}
}

// Handle dismisses the discovered attribute of project keyed key, or
// answers not_found for one not listed.
func (h *DismissDiscoveredAttribute) Handle(ctx context.Context, project workspace.ProjectID, key string) error {
	return h.tx.Within(ctx, func(ctx context.Context) error {
		a, err := listedForUpdate(ctx, h.store, project, key)
		if err != nil {
			return err
		}
		if a, err = a.Dismiss(); err != nil {
			return err
		}
		return h.store.SaveDiscoveredAttribute(ctx, a)
	})
}

// listedForUpdate returns the discovered attribute of project keyed key a
// member sees, locked, or delivery.ErrDiscoveredAttributeNotFound.
func listedForUpdate(ctx context.Context, store DiscoveredAttributeEditor, project workspace.ProjectID, key string,
) (delivery.DiscoveredAttribute, error) {
	a, err := store.DiscoveredAttributeForUpdate(ctx, project, key)
	if errors.Is(err, ErrNotFound) || (err == nil && a.Dismissed) {
		return a, delivery.ErrDiscoveredAttributeNotFound
	}
	return a, err
}

// DeclareAttribute declares an attribute through the workspace context, as
// a member creating it would, audited as such.
type DeclareAttribute func(ctx context.Context, d AttributeDeclaration) (workspace.Attribute, error)

// AttributeDeclaration is an attribute to declare.
type AttributeDeclaration struct {
	Project                workspace.ProjectID
	Key, Name, Description string
	Type                   workspace.AttributeType
	Request                governance.Request
}

// DeclareDiscoveredAttribute declares a discovered attribute.
type DeclareDiscoveredAttribute struct {
	tx      Transactor
	store   DiscoveredAttributeEditor
	declare DeclareAttribute
}

// NewDeclareDiscoveredAttribute returns the handler.
func NewDeclareDiscoveredAttribute(tx Transactor, store DiscoveredAttributeEditor, declare DeclareAttribute) *DeclareDiscoveredAttribute {
	return &DeclareDiscoveredAttribute{tx: tx, store: store, declare: declare}
}

// Handle declares the discovered attribute of d's project keyed d.Key with
// d's type, named after its key unless d names it, and removes it from the
// discovered ones, in one unit of work; an attribute not listed is
// not_found.
func (h *DeclareDiscoveredAttribute) Handle(ctx context.Context, d AttributeDeclaration) (workspace.Attribute, error) {
	if d.Name == "" {
		d.Name = d.Key
	}
	var attr workspace.Attribute
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := listedForUpdate(ctx, h.store, d.Project, d.Key); err != nil {
			return err
		}
		var err error
		if attr, err = h.declare(ctx, d); err != nil {
			return err
		}
		return h.store.DeleteDiscoveredAttribute(ctx, d.Project, d.Key)
	})
	return attr, err
}
