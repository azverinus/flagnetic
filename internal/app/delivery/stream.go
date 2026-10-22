package delivery

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// RulesetSubscription is one stream's view of the ruleset versions of its
// environment this replica has loaded.
type RulesetSubscription interface {
	// Changed is sent on when a version above every one offered before is
	// offered. It holds one signal at most, so a subscriber slow to read
	// it misses none: it reads Version when it gets to it.
	Changed() <-chan struct{}
	// Version returns the newest version offered.
	Version() int64
	// Close ends the subscription.
	Close()
}

// RulesetStreams offers each new ruleset version of an environment to the
// streams of that environment once this replica has loaded it.
type RulesetStreams interface {
	// Subscribe returns a subscription to the versions of environment.
	Subscribe(environment workspace.EnvironmentID) RulesetSubscription
	// Latest returns the newest version of environment this replica has
	// heard of while it has streams, 0 when it has heard of none.
	Latest(environment workspace.EnvironmentID) int64
}

// WatchRuleset follows the ruleset versions of a key's environment for the
// ruleset stream, which carries versions only: never a document, so either
// kind of key may hold it.
type WatchRuleset struct {
	streams  RulesetStreams
	rulesets Rulesets
}

// NewWatchRuleset returns the handler. rulesets is read for the current
// version, and must not count its answers towards a version's propagation:
// a stream carries no ruleset.
func NewWatchRuleset(streams RulesetStreams, rulesets Rulesets) *WatchRuleset {
	return &WatchRuleset{streams: streams, rulesets: rulesets}
}

// Open subscribes to the versions of key's environment, then reads its
// current version: in that order, so a version loaded between the two is
// offered rather than missed. ErrRulesetUnavailable when the replica
// cannot load the ruleset in ctx's time.
func (h *WatchRuleset) Open(ctx context.Context, key delivery.SDKKey) (RulesetSubscription, int64, error) {
	sub := h.streams.Subscribe(key.Environment)
	rs, err := h.rulesets.Ruleset(ctx, key.Environment)
	if err != nil {
		sub.Close()
		return nil, 0, err
	}
	return sub, rs.Version, nil
}

// CatchUp returns the current version of key's environment when the
// replica has heard of a version above sent, which a failed reload may
// have left unoffered; otherwise sent.
func (h *WatchRuleset) CatchUp(ctx context.Context, key delivery.SDKKey, sent int64) (int64, error) {
	if h.streams.Latest(key.Environment) <= sent {
		return sent, nil
	}
	rs, err := h.rulesets.Ruleset(ctx, key.Environment)
	if err != nil {
		return sent, err
	}
	return max(rs.Version, sent), nil
}
