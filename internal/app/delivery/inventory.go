package delivery

import (
	"context"
	"errors"
	"slices"
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
	// InventoryOfKey returns the identities of the rows key holds.
	InventoryOfKey(ctx context.Context, key delivery.SDKKeyID) ([]delivery.InventoryIdentity, error)
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

// InventoryDrops counts the sightings a replica dropped since the last
// round, by why.
type InventoryDrops struct {
	// QueueFull counts sightings of a new row that found every place of the
	// replica's queue taken.
	QueueFull int
	// KeyFull counts sightings of a new row whose key held
	// delivery.MaxInventoryRowsPerKey rows already.
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
	dropped InventoryDrops
}

// NewSDKInventory returns the replica's gatherer of the SDK inventory.
func NewSDKInventory(tx Transactor, store InventoryStore, c clock.Clock) *SDKInventory {
	return &SDKInventory{tx: tx, store: store, clock: c, pending: map[delivery.InventoryIdentity]delivery.InventorySighting{}}
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
	i.mu.Unlock()
	byKey := map[delivery.SDKKeyID][]delivery.InventorySighting{}
	for _, s := range pending {
		byKey[s.Key] = append(byKey[s.Key], s)
	}
	var first error
	for key, seen := range byKey {
		full, err := i.write(ctx, key, seen)
		i.mu.Lock()
		if err != nil {
			i.dropped.WriteFailed += len(seen)
		} else {
			i.dropped.KeyFull += full
		}
		i.mu.Unlock()
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// write records the sightings of key: a row the key holds is updated, and a
// new row added while the key holds fewer than
// delivery.MaxInventoryRowsPerKey, in identity order so that which rows are
// kept does not depend on chance. The key is locked for the unit of work so
// that replicas writing at once never take it past its bound. It returns
// how many sightings were not recorded because the key was full.
func (i *SDKInventory) write(ctx context.Context, key delivery.SDKKeyID, seen []delivery.InventorySighting) (int, error) {
	slices.SortFunc(seen, func(a, b delivery.InventorySighting) int {
		return delivery.CompareInventoryIdentities(a.InventoryIdentity, b.InventoryIdentity)
	})
	var full int
	err := i.tx.Within(ctx, func(ctx context.Context) error {
		full = 0
		if err := i.store.LockSDKInventory(ctx, key); err != nil {
			return err
		}
		stored, err := i.store.InventoryOfKey(ctx, key)
		if err != nil {
			return err
		}
		known := make(map[delivery.InventoryIdentity]bool, len(stored))
		for _, id := range stored {
			known[id] = true
		}
		count := len(stored)
		for _, s := range seen {
			if !known[s.InventoryIdentity] {
				if count >= delivery.MaxInventoryRowsPerKey {
					full++
					continue
				}
				count++
			}
			if err := i.store.RecordInventory(ctx, s); err != nil {
				return err
			}
		}
		return nil
	})
	return full, err
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
