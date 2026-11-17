package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// InventoryStore stores the SDK inventory of each environment.
type InventoryStore interface {
	// LockSDKInventory holds, until the unit of work ends, the right to add
	// rows of key to the inventory.
	LockSDKInventory(ctx context.Context, key delivery.SDKKeyID) error
	// TrimInventory deletes the rows of key beyond the newest max by last
	// time seen, the oldest first, and returns how many.
	TrimInventory(ctx context.Context, key delivery.SDKKeyID, max int) (int, error)
	// RecordInventory records a sighting of an SDK: a new row, or the row's
	// last time and ruleset version moved on, unless the row was seen less
	// than delivery.InventoryInterval before with the same ruleset version,
	// so replicas writing the same row coalesce.
	RecordInventory(ctx context.Context, s delivery.InventorySighting) error
	// Inventory returns the rows of environment, most recently seen first.
	Inventory(ctx context.Context, environment workspace.EnvironmentID) ([]delivery.InventoryRow, error)
	// PurgeInventory deletes the rows last seen before before and returns
	// how many.
	PurgeInventory(ctx context.Context, before time.Time) (int64, error)
}

// maxPendingInventory bounds the rows a replica holds between two rounds,
// so a flood of distinct SDKs or app versions cannot grow its memory: a
// sighting of a row beyond it is dropped, and seen again on the SDK's next
// request.
const maxPendingInventory = 1024

// maxPendingInventoryPerKey bounds the rows of one key a replica holds
// between two rounds, a sixteenth of the queue: any caller of a client key
// sets its own User-Agent, so without a share of its own a handful of
// requests would take every place and the SDKs of every other key would go
// unrecorded.
const maxPendingInventoryPerKey = 64

// InventoryDrops counts the sightings a replica dropped since the last
// round, by why.
type InventoryDrops struct {
	// QueueFull counts sightings of a new row that found every place of the
	// replica's queue taken.
	QueueFull int
	// KeyFull counts sightings of a new row whose key held its share of the
	// replica's queue already.
	KeyFull int
	// WriteFailed counts sightings held but lost because writing their
	// key's failed.
	WriteFailed int
}

// Total is how many sightings were dropped, whatever why.
func (d InventoryDrops) Total() int { return d.QueueFull + d.KeyFull + d.WriteFailed }

// SDKInventory gathers the SDKs a replica sees making requests and writes
// them in rounds, so no request waits on recording its SDK and an SDK
// polling many times a minute costs one write.
type SDKInventory struct {
	tx    Transactor
	store InventoryStore
	clock clock.Clock

	mu      sync.Mutex
	pending map[delivery.InventoryIdentity]delivery.InventorySighting
	// perKey counts the rows of pending by key.
	perKey  map[delivery.SDKKeyID]int
	dropped InventoryDrops
}

// NewSDKInventory returns the replica's gatherer of the SDK inventory.
func NewSDKInventory(tx Transactor, store InventoryStore, c clock.Clock) *SDKInventory {
	return &SDKInventory{tx: tx, store: store, clock: c, pending: map[delivery.InventoryIdentity]delivery.InventorySighting{}, perKey: map[delivery.SDKKeyID]int{}}
}

// Note keeps a request of key's SDK, naming the app version its context
// carried ("" for none) and the ruleset version it fetched or was sent (0
// for none), for the next round.
func (i *SDKInventory) Note(key delivery.SDKKey, sdk delivery.SDKIdentity, appVersion string, rulesetVersion int64) {
	if i == nil {
		return
	}
	s := delivery.Sight(key, sdk, appVersion, rulesetVersion, i.clock.Now())
	i.mu.Lock()
	defer i.mu.Unlock()
	if held, ok := i.pending[s.InventoryIdentity]; ok {
		i.pending[s.InventoryIdentity] = held.Merge(s)
		return
	}
	if len(i.pending) >= maxPendingInventory {
		i.dropped.QueueFull++
		return
	}
	if i.perKey[s.Key] >= maxPendingInventoryPerKey {
		i.dropped.KeyFull++
		return
	}
	i.perKey[s.Key]++
	i.pending[s.InventoryIdentity] = s
}

// Dropped returns the sightings dropped since it was last asked, for the
// round's warning.
func (i *SDKInventory) Dropped() InventoryDrops {
	i.mu.Lock()
	defer i.mu.Unlock()
	n := i.dropped
	i.dropped = InventoryDrops{}
	return n
}

// Flush writes the sightings noted since the last round, one unit of work
// per key. A key's sightings that cannot be written are dropped rather than
// retried, and counted as dropped: its SDKs' next requests are noted again.
// It returns the first failure.
func (i *SDKInventory) Flush(ctx context.Context) error {
	i.mu.Lock()
	pending := i.pending
	i.pending = map[delivery.InventoryIdentity]delivery.InventorySighting{}
	i.perKey = map[delivery.SDKKeyID]int{}
	i.mu.Unlock()
	byKey := map[delivery.SDKKeyID][]delivery.InventorySighting{}
	for _, s := range pending {
		byKey[s.Key] = append(byKey[s.Key], s)
	}
	var first error
	for key, seen := range byKey {
		err := i.write(ctx, key, seen)
		if err != nil {
			i.mu.Lock()
			i.dropped.WriteFailed += len(seen)
			i.mu.Unlock()
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// write records the sightings of key, then drops the rows beyond the
// delivery.MaxInventoryRowsPerKey it may hold, the oldest seen first: the
// SDKs in use stay listed and the ones that stopped make room. The key is
// locked for the unit of work so that replicas writing at once never take
// it past its bound.
func (i *SDKInventory) write(ctx context.Context, key delivery.SDKKeyID, seen []delivery.InventorySighting) error {
	return i.tx.Within(ctx, func(ctx context.Context) error {
		if err := i.store.LockSDKInventory(ctx, key); err != nil {
			return err
		}
		for _, s := range seen {
			if err := i.store.RecordInventory(ctx, s); err != nil {
				return err
			}
		}
		_, err := i.store.TrimInventory(ctx, key, delivery.MaxInventoryRowsPerKey)
		return err
	})
}

// ListSDKInventory answers a member which SDKs use an environment.
type ListSDKInventory struct {
	environments RulesetEnvironments
	store        InventoryStore
}

// NewListSDKInventory returns the handler.
func NewListSDKInventory(environments RulesetEnvironments, store InventoryStore) *ListSDKInventory {
	return &ListSDKInventory{environments: environments, store: store}
}

// Handle returns the inventory of project's environment keyed environment,
// most recently seen first, or not_found for an environment the project does
// not have.
func (h *ListSDKInventory) Handle(ctx context.Context, project workspace.ProjectID, environment string) ([]delivery.InventoryRow, error) {
	envID, _, err := h.environments.RulesetEnvironment(ctx, project, environment)
	if errors.Is(err, ErrNotFound) {
		return nil, errEnvironmentNotFound
	}
	if err != nil {
		return nil, err
	}
	return h.store.Inventory(ctx, envID)
}

// PurgeSDKInventory forgets the SDKs not seen for the retention.
type PurgeSDKInventory struct {
	store     InventoryStore
	clock     clock.Clock
	retention time.Duration
}

// NewPurgeSDKInventory returns the handler, forgetting rows not seen for
// retention.
func NewPurgeSDKInventory(store InventoryStore, c clock.Clock, retention time.Duration) *PurgeSDKInventory {
	return &PurgeSDKInventory{store: store, clock: c, retention: retention}
}

// Handle deletes the rows last seen more than the retention ago.
func (h *PurgeSDKInventory) Handle(ctx context.Context) error {
	_, err := h.store.PurgeInventory(ctx, h.clock.Now().Add(-h.retention))
	return err
}
