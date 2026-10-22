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

// SDKInventory gathers the SDKs a replica sees making requests and writes
// them in rounds, so no request waits on recording its SDK and an SDK
// polling many times a minute costs one write.
type SDKInventory struct {
	store InventoryStore
	clock clock.Clock

	mu      sync.Mutex
	pending map[delivery.InventoryIdentity]delivery.InventorySighting
}

// NewSDKInventory returns the replica's gatherer of the SDK inventory.
func NewSDKInventory(store InventoryStore, c clock.Clock) *SDKInventory {
	return &SDKInventory{store: store, clock: c, pending: map[delivery.InventoryIdentity]delivery.InventorySighting{}}
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
		return
	}
	i.pending[s.InventoryIdentity] = s
}

// Flush writes the sightings noted since the last round. A sighting that
// cannot be written is dropped rather than retried: its SDK's next request
// is noted again. It returns the first failure.
func (i *SDKInventory) Flush(ctx context.Context) error {
	i.mu.Lock()
	pending := i.pending
	i.pending = map[delivery.InventoryIdentity]delivery.InventorySighting{}
	i.mu.Unlock()
	var first error
	for _, s := range pending {
		if err := i.store.RecordInventory(ctx, s); err != nil && first == nil {
			first = err
		}
	}
	return first
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
