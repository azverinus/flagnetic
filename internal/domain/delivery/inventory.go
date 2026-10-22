package delivery

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// InventoryInterval is how often one row of the SDK inventory is written at
// most: often enough to tell an SDK that runs from one that stopped, rarely
// enough that an SDK polling every few seconds costs a write a minute.
const InventoryInterval = time.Minute

// DefaultInventoryRetention is how long a row of the SDK inventory is kept
// after its SDK was last seen when the configuration does not say.
const DefaultInventoryRetention = 90 * 24 * time.Hour

// InventoryIdentity is what one row of the SDK inventory is about: an SDK
// at one version, on one platform and app version, using one key of one
// environment.
type InventoryIdentity struct {
	Environment workspace.EnvironmentID
	Key         SDKKeyID
	SDK         SDKIdentity
	// AppVersion is "" when the requests carried none.
	AppVersion string
}

// InventorySighting is the requests of an SDK seen from First to At.
// RulesetVersion is the version the latest of them fetched or was sent, 0
// for none.
type InventorySighting struct {
	InventoryIdentity
	First          time.Time
	At             time.Time
	RulesetVersion int64
}

// Sight returns the sighting of a request of key's SDK at at.
func Sight(key SDKKey, sdk SDKIdentity, appVersion string, rulesetVersion int64, at time.Time) InventorySighting {
	return InventorySighting{
		InventoryIdentity: InventoryIdentity{Environment: key.Environment, Key: key.ID, SDK: sdk, AppVersion: cut(appVersion)},
		First:             at, At: at, RulesetVersion: rulesetVersion,
	}
}

// Merge returns s with a later sighting of the same row folded in: the
// earliest time, the latest time, and the latest ruleset version known.
func (s InventorySighting) Merge(later InventorySighting) InventorySighting {
	if later.First.Before(s.First) {
		s.First = later.First
	}
	if later.At.After(s.At) {
		s.At = later.At
	}
	if later.RulesetVersion != 0 {
		s.RulesetVersion = later.RulesetVersion
	}
	return s
}

// InventoryRow is one row of an environment's SDK inventory.
type InventoryRow struct {
	InventoryIdentity
	// KeyName, KeyID and KeyKind describe the key, for a member telling
	// which service holds it.
	KeyName   string
	KeyID     string
	KeyKind   SDKKeyKind
	FirstSeen time.Time
	LastSeen  time.Time
	// LastRulesetVersion is 0 when the SDK never fetched nor was sent one.
	LastRulesetVersion int64
}
