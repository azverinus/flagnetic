package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// SDKRetryAfter is how long an SDK is told to wait before retrying a request
// the server could not serve for now: long enough for a load or a lookup to
// finish, short enough that an SDK starting up is served within seconds.
const SDKRetryAfter = 2 * time.Second

// RulesetWait is how long a request waits for a ruleset its replica is
// loading before it is answered ErrRulesetUnavailable: an SDK holding a
// ruleset keeps it and retries, rather than wait on a load that may take up
// to its own bound. The load goes on for the requests after it.
const RulesetWait = 2 * time.Second

// ErrRulesetUnavailable answers a ruleset this replica cannot load for now.
// An SDK keeps the ruleset it holds rather than receive an empty one.
var ErrRulesetUnavailable = apperrors.Unavailable("ruleset_unavailable",
	"this replica cannot load the environment's ruleset yet; try again").WithRetryAfter(SDKRetryAfter)

// errSDKKeyLookupFailed answers a key that could not be checked because its
// store did not answer: the key may well be valid, so the SDK retries.
var errSDKKeyLookupFailed = apperrors.Unavailable("unavailable",
	"the SDK key could not be checked; try again").WithRetryAfter(SDKRetryAfter)

// SDKKeyLookup finds SDK keys by the hash of their value.
type SDKKeyLookup interface {
	// SDKKeyByHash returns the key whose value hashes to hash, or
	// ErrNotFound.
	SDKKeyByHash(ctx context.Context, hash delivery.SDKKeyHash) (delivery.SDKKey, error)
}

// AuthenticateSDKKey authenticates the SDK key a request to the SDK API
// presents.
type AuthenticateSDKKey struct {
	keys  SDKKeyLookup
	uses  *SDKKeyUses
	clock clock.Clock
}

// NewAuthenticateSDKKey returns the handler, noting each accepted key's use
// in uses.
func NewAuthenticateSDKKey(keys SDKKeyLookup, uses *SDKKeyUses, c clock.Clock) *AuthenticateSDKKey {
	return &AuthenticateSDKKey{keys: keys, uses: uses, clock: c}
}

// Handle returns the key whose value is presented: sdk_key_missing for no
// value; sdk_key_invalid for a malformed or unknown one, refused alike so a
// caller learns nothing about which keys exist; sdk_key_revoked or
// sdk_key_expired for a key that no longer works at the clock's time; and
// unavailable when the key cannot be looked up.
func (h *AuthenticateSDKKey) Handle(ctx context.Context, value string) (delivery.SDKKey, error) {
	if value == "" {
		return delivery.SDKKey{}, delivery.ErrSDKKeyMissing
	}
	_, hash, err := delivery.ParseSDKKeyValue(value)
	if err != nil {
		return delivery.SDKKey{}, err
	}
	k, err := h.keys.SDKKeyByHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return delivery.SDKKey{}, delivery.ErrSDKKeyInvalid
	}
	if err != nil {
		return delivery.SDKKey{}, errSDKKeyLookupFailed.WithCause(err)
	}
	now := h.clock.Now()
	if err := k.AcceptAt(now); err != nil {
		return delivery.SDKKey{}, err
	}
	h.uses.Note(k, now)
	return k, nil
}

// SDKKeyUseStore records when SDK keys were last used.
type SDKKeyUseStore interface {
	// RecordSDKKeyUse records that key was used at at, unless a use less
	// than delivery.SDKKeyUseInterval before at is recorded already, so
	// replicas writing the same key's use coalesce.
	RecordSDKKeyUse(ctx context.Context, key delivery.SDKKeyID, at time.Time) error
}

// maxPendingSDKKeyUses bounds the uses a replica holds between two rounds,
// so a flood of distinct keys cannot grow its memory: a use beyond it is
// dropped, and the key's next use is noted again.
const maxPendingSDKKeyUses = 1024

// SDKKeyUses gathers the uses of SDK keys a replica sees and writes them in
// rounds, so authenticating a request never waits on a write and a key
// presented many times a minute costs one.
type SDKKeyUses struct {
	store SDKKeyUseStore

	mu sync.Mutex
	// pending is the latest use of each key since the last round.
	pending map[delivery.SDKKeyID]time.Time
	// noted is when each key's use was last kept for writing, so a key
	// whose cached copy still shows an old last use is not noted again
	// within the interval.
	noted map[delivery.SDKKeyID]time.Time
}

// NewSDKKeyUses returns the replica's gatherer of SDK key uses.
func NewSDKKeyUses(store SDKKeyUseStore) *SDKKeyUses {
	return &SDKKeyUses{store: store, pending: map[delivery.SDKKeyID]time.Time{}, noted: map[delivery.SDKKeyID]time.Time{}}
}

// Note keeps k's use at at for the next round when it is one worth
// recording and there is room for it.
func (u *SDKKeyUses) Note(k delivery.SDKKey, at time.Time) {
	if !k.UseToRecord(at) {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if last, ok := u.noted[k.ID]; ok && at.Before(last.Add(delivery.SDKKeyUseInterval)) {
		return
	}
	if _, ok := u.pending[k.ID]; !ok && len(u.pending) >= maxPendingSDKKeyUses {
		return
	}
	u.pending[k.ID] = at
	u.noted[k.ID] = at
}

// Flush writes the uses noted since the last round. A use that cannot be
// written is dropped rather than retried: the key's next use after the
// interval is noted again, and nothing depends on the record but a
// member's view of their keys. It returns the first failure, for the
// round's log.
func (u *SDKKeyUses) Flush(ctx context.Context) error {
	u.mu.Lock()
	pending := u.pending
	u.pending = map[delivery.SDKKeyID]time.Time{}
	// Only the keys written in this round are remembered until the next:
	// rounds are an interval apart, so a key noted earlier may be noted
	// again, and the store still coalesces it.
	for k := range u.noted {
		if _, kept := pending[k]; !kept {
			delete(u.noted, k)
		}
	}
	u.mu.Unlock()
	var first error
	for key, at := range pending {
		if err := u.store.RecordSDKKeyUse(ctx, key, at); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Ruleset is an environment's ruleset document at one version.
type Ruleset struct {
	// Version is the ruleset version the document carries.
	Version int64
	// Document is the server-scope ruleset document, as SDKs receive it.
	Document []byte
}

// Rulesets answers the current ruleset of an environment.
type Rulesets interface {
	// Ruleset returns the ruleset of environment at its newest version
	// this replica knows, or ErrRulesetUnavailable.
	Ruleset(ctx context.Context, environment workspace.EnvironmentID) (Ruleset, error)
}

// ServeRuleset answers a server SDK its environment's ruleset.
type ServeRuleset struct{ rulesets Rulesets }

// NewServeRuleset returns the handler.
func NewServeRuleset(rulesets Rulesets) *ServeRuleset { return &ServeRuleset{rulesets: rulesets} }

// Handle returns the ruleset of key's environment. Only a server key
// receives one (key_kind_not_allowed otherwise): the document carries
// private segments, which an app shipping a client key must never hold.
func (h *ServeRuleset) Handle(ctx context.Context, key delivery.SDKKey) (Ruleset, error) {
	if err := key.CheckKind(delivery.SDKKeyServer); err != nil {
		return Ruleset{}, err
	}
	return h.rulesets.Ruleset(ctx, key.Environment)
}

// RulesetEnvironmentsByID reads the environment a ruleset is materialized
// for by its identifier.
type RulesetEnvironmentsByID interface {
	// RulesetEnvironmentByID returns the project, key and ruleset version
	// of environment, or ErrNotFound.
	RulesetEnvironmentByID(ctx context.Context, environment workspace.EnvironmentID) (workspace.ProjectID, string, int64, error)
}

// MaterializeRulesetDeps are what MaterializeRuleset runs on.
type MaterializeRulesetDeps struct {
	Snapshots    Snapshotter
	Environments RulesetEnvironmentsByID
	Declarations Declarations
	Flags        ConfiguredFlags
}

// MaterializeRuleset materializes an environment's ruleset from what is
// stored.
type MaterializeRuleset struct{ d MaterializeRulesetDeps }

// NewMaterializeRuleset returns the handler.
func NewMaterializeRuleset(d MaterializeRulesetDeps) *MaterializeRuleset {
	return &MaterializeRuleset{d: d}
}

// Ruleset materializes the ruleset of environment from one snapshot, so the
// document and its version never mix the states before and after a write.
// An environment that no longer exists has no ruleset to serve:
// ErrRulesetUnavailable.
func (h *MaterializeRuleset) Ruleset(ctx context.Context, environment workspace.EnvironmentID) (Ruleset, error) {
	var m delivery.Materials
	err := h.d.Snapshots.Snapshot(ctx, func(ctx context.Context) error {
		project, key, version, err := h.d.Environments.RulesetEnvironmentByID(ctx, environment)
		if err != nil {
			return err
		}
		m = delivery.Materials{Environment: key, Version: version}
		return readMaterials(ctx, h.d.Declarations, h.d.Flags, project, environment, &m)
	})
	if errors.Is(err, ErrNotFound) {
		return Ruleset{}, ErrRulesetUnavailable
	}
	if err != nil {
		return Ruleset{}, err
	}
	doc, err := delivery.Materialize(m)
	if err != nil {
		return Ruleset{}, err
	}
	return Ruleset{Version: m.Version, Document: doc}, nil
}

// readMaterials reads into m the declarations, segments and flags of
// project and the flags' configurations in environment, in the snapshot ctx
// carries.
func readMaterials(ctx context.Context, declarations Declarations, configured ConfiguredFlags, project workspace.ProjectID,
	environment workspace.EnvironmentID, m *delivery.Materials,
) error {
	var err error
	if m.UnitTypes, err = declarations.UnitTypes(ctx, project); err != nil {
		return err
	}
	if m.Attributes, err = declarations.Attributes(ctx, project); err != nil {
		return err
	}
	if m.Segments, err = configured.Segments(ctx, project); err != nil {
		return err
	}
	if m.Flags, err = configured.ProjectFlags(ctx, project); err != nil {
		return err
	}
	m.Configurations, err = configured.EnvironmentConfigurations(ctx, environment)
	return err
}

// ReadRuleset answers a member the ruleset of one of a project's
// environments, as its server SDKs receive it.
type ReadRuleset struct {
	environments RulesetEnvironments
	rulesets     Rulesets
}

// NewReadRuleset returns the handler.
func NewReadRuleset(environments RulesetEnvironments, rulesets Rulesets) *ReadRuleset {
	return &ReadRuleset{environments: environments, rulesets: rulesets}
}

// Handle returns the ruleset of project's environment keyed environment,
// the same document a server key receives, or not_found for an environment
// the project does not have.
func (h *ReadRuleset) Handle(ctx context.Context, project workspace.ProjectID, environment string) (Ruleset, error) {
	envID, _, err := h.environments.RulesetEnvironment(ctx, project, environment)
	if errors.Is(err, ErrNotFound) {
		return Ruleset{}, errEnvironmentNotFound
	}
	if err != nil {
		return Ruleset{}, err
	}
	return h.rulesets.Ruleset(ctx, envID)
}
