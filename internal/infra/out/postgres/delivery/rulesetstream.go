package delivery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// fanOutRetry is how long a reload that failed waits before its one retry:
// long enough for a passing failure to pass, short enough that the streams
// still hear the version well within their heartbeat, which catches up
// whatever the retry misses.
const fanOutRetry = time.Second

// RulesetStream offers each new ruleset version to the streams of its
// environment once this replica has loaded it, so an SDK told of a version
// finds it here when it asks. It stands in front of the RulesetCache on
// RulesetsChannel: each notification reaches the cache first, then wakes
// the environment's fan-out.
//
// One fan-out runs per environment with streams: it reloads the ruleset,
// then offers its version to every subscription. Notifications arriving
// during a reload make one more cycle after it, however many they are. A
// reload that fails is retried once after fanOutRetry; then the streams'
// heartbeats catch up.
type RulesetStream struct {
	cache    *RulesetCache
	rulesets appdelivery.Rulesets
	log      *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	runs   sync.WaitGroup

	mu     sync.Mutex
	closed bool
	topics map[workspace.EnvironmentID]*topic
}

// topic is the streams of one environment.
type topic struct {
	subs map[*subscription]struct{}
	// running is set while the fan-out runs, dirty when a notification
	// arrived since its cycle began.
	running, dirty bool
	// latest is the newest version heard of.
	latest int64
}

// NewRulesetStream returns the fan-out in front of cache, reloading
// through it without counting towards a version's propagation: a stream
// carries no ruleset.
func NewRulesetStream(cache *RulesetCache, log *slog.Logger) *RulesetStream {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &RulesetStream{cache: cache, rulesets: cache.Unmeasured(), log: log, topics: map[workspace.EnvironmentID]*topic{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s
}

// Close ends the fan-outs and waits for them; nothing is offered after it.
func (s *RulesetStream) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.runs.Wait()
}

// Listening tells the cache, then wakes every streamed environment when
// the listener is up again: a version may have been missed while it was
// down.
func (s *RulesetStream) Listening(up bool) {
	s.cache.Listening(up)
	if !up {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for environment, t := range s.topics {
		s.wake(environment, t)
	}
}

// Received tells the cache, then wakes the environment a bump names. The
// deletion of an environment drops its topic; its streams end on their
// next heartbeat, their key gone with it. A payload that cannot be read
// wakes every environment, since it may have named any.
func (s *RulesetStream) Received(payload string) {
	s.cache.Received(payload)
	environment, version, _, ok := parseRulesetNotification(payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !ok:
		for environment, t := range s.topics {
			s.wake(environment, t)
		}
	case version == 0:
		delete(s.topics, environment)
	default:
		if t, ok := s.topics[environment]; ok {
			t.latest = max(t.latest, version)
			s.wake(environment, t)
		}
	}
}

// Latest returns the newest version of environment heard of while it has
// streams.
func (s *RulesetStream) Latest(environment workspace.EnvironmentID) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.topics[environment]; ok {
		return t.latest
	}
	return 0
}

// Subscribe returns a subscription to the versions of environment.
func (s *RulesetStream) Subscribe(environment workspace.EnvironmentID) appdelivery.RulesetSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.topics[environment]
	if !ok {
		t = &topic{subs: map[*subscription]struct{}{}}
		s.topics[environment] = t
	}
	sub := &subscription{stream: s, environment: environment, topic: t, changed: make(chan struct{}, 1)}
	t.subs[sub] = struct{}{}
	return sub
}

// wake runs the fan-out of environment, or marks the one running dirty.
// s.mu is held.
func (s *RulesetStream) wake(environment workspace.EnvironmentID, t *topic) {
	if t.running {
		t.dirty = true
		return
	}
	if s.closed {
		return
	}
	t.running = true
	s.runs.Add(1)
	safego.Go(s.ctx, "ruleset fan-out", s.log, func(ctx context.Context) {
		defer s.runs.Done()
		ended := false
		defer func() {
			if !ended {
				// A fan-out that panicked must not leave its topic
				// waiting for it forever.
				s.mu.Lock()
				t.running = false
				s.mu.Unlock()
			}
		}()
		s.fanOut(ctx, environment, t)
		ended = true
	})
}

// fanOut reloads the ruleset of environment and offers its version to t's
// subscriptions, for as long as notifications keep arriving during a
// cycle.
func (s *RulesetStream) fanOut(ctx context.Context, environment workspace.EnvironmentID, t *topic) {
	retried := false
	for {
		s.mu.Lock()
		t.dirty = false
		s.mu.Unlock()

		loadCtx, cancel := context.WithTimeout(ctx, sharedLoadTimeout)
		rs, err := s.rulesets.Ruleset(loadCtx, environment)
		cancel()
		switch {
		case err == nil:
			retried = false
			s.offer(t, rs.Version)
		case !retried && ctx.Err() == nil:
			retried = true
			select {
			case <-time.After(fanOutRetry):
			case <-ctx.Done():
			}
			continue
		default:
			s.log.Warn("a ruleset version could not be offered to its streams; their heartbeats catch it up",
				"environment", environment.String(), "error", err.Error())
		}

		s.mu.Lock()
		if !t.dirty || ctx.Err() != nil {
			t.running = false
			// The streams that closed while it ran left their topic for it.
			if len(t.subs) == 0 && s.topics[environment] == t {
				delete(s.topics, environment)
			}
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// offer offers version to every subscription of t.
func (s *RulesetStream) offer(t *topic, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.latest = max(t.latest, version)
	for sub := range t.subs {
		sub.offer(version)
	}
}

// subscription is one stream's subscription to its environment's
// versions.
type subscription struct {
	stream      *RulesetStream
	environment workspace.EnvironmentID
	topic       *topic
	changed     chan struct{}

	mu      sync.Mutex
	version int64
}

func (sub *subscription) Changed() <-chan struct{} { return sub.changed }

func (sub *subscription) Version() int64 {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return sub.version
}

// offer raises the subscription's version and signals it, unless a signal
// is pending already: the subscriber reads the newest version then.
func (sub *subscription) offer(version int64) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if version <= sub.version {
		return
	}
	sub.version = version
	select {
	case sub.changed <- struct{}{}:
	default:
	}
}

// Close removes the subscription, and its topic with the last one unless
// a fan-out still runs for it.
func (sub *subscription) Close() {
	s := sub.stream
	s.mu.Lock()
	defer s.mu.Unlock()
	t := sub.topic
	delete(t.subs, sub)
	if len(t.subs) == 0 && !t.running && s.topics[sub.environment] == t {
		delete(s.topics, sub.environment)
	}
}
