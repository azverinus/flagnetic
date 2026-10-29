package delivery

import (
	"context"
	"sync"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SDKKeyCache holds the SDK keys a replica has authenticated, by the hash of
// their value, so an SDK polling every few seconds costs no lookup. It
// trusts what it holds only while its listener on SDKKeysChannel is up: a
// revocation announced while it is down may be missed, so until it listens
// again every lookup goes to PostgreSQL and nothing is kept. A key that is
// not found is never kept, so a key created on another replica is accepted
// at once and guessing values fills no memory.
//
// It holds keys as they are stored; whether a key is still accepted at the
// time of a request (its expiry) is judged on every request, so a rotation
// overlap ends on time without any notification.
type SDKKeyCache struct {
	store *Store

	mu        sync.Mutex
	listening bool
	// generation counts what may have made a lookup stale: a notification,
	// or the listener going up or down. A lookup is kept only when no such
	// event happened while it was under way.
	generation uint64
	byHash     map[delivery.SDKKeyHash]delivery.SDKKey
	hashByID   map[delivery.SDKKeyID]delivery.SDKKeyHash
}

// NewSDKKeyCache returns a cache of the keys of store, which reads through
// until its listener reports itself up.
func NewSDKKeyCache(store *Store) *SDKKeyCache {
	return &SDKKeyCache{store: store, byHash: map[delivery.SDKKeyHash]delivery.SDKKey{}, hashByID: map[delivery.SDKKeyID]delivery.SDKKeyHash{}}
}

// SDKKeyByHash returns the key whose value hashes to hash, from what the
// cache holds while it can be trusted, or appdelivery.ErrNotFound.
func (c *SDKKeyCache) SDKKeyByHash(ctx context.Context, hash delivery.SDKKeyHash) (delivery.SDKKey, error) {
	c.mu.Lock()
	if k, ok := c.byHash[hash]; ok {
		c.mu.Unlock()
		return k, nil
	}
	generation := c.generation
	c.mu.Unlock()
	k, err := c.store.SDKKeyByHash(ctx, hash)
	if err != nil {
		return delivery.SDKKey{}, err
	}
	c.mu.Lock()
	if c.listening && c.generation == generation {
		c.byHash[hash] = k
		c.hashByID[k.ID] = hash
	}
	c.mu.Unlock()
	return k, nil
}

// HeldSDKKeyByHash returns the key whose value hashes to hash when the cache
// holds it, without reading PostgreSQL. It holds nothing while its listener
// is down, and never a hash that names no key.
func (c *SDKKeyCache) HeldSDKKeyByHash(hash delivery.SDKKeyHash) (delivery.SDKKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.byHash[hash]
	return k, ok
}

// Listening is told when the listener starts or stops listening: either way
// a notification may have been missed, so everything held is dropped.
func (c *SDKKeyCache) Listening(up bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listening = up
	c.generation++
	clear(c.byHash)
	clear(c.hashByID)
}

// Received is told the identifier of a key whose expiry, revocation or
// allowed origins changed, or which was deleted, and drops it.
func (c *SDKKeyCache) Received(payload string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	key, err := id.Parse[delivery.SDKKey](payload)
	if err != nil {
		// A payload this replica cannot read names a key it cannot tell
		// apart: dropping everything is the only safe reading.
		clear(c.byHash)
		clear(c.hashByID)
		return
	}
	if hash, ok := c.hashByID[key]; ok {
		delete(c.byHash, hash)
		delete(c.hashByID, key)
	}
}
