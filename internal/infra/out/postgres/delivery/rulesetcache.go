package delivery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

const (
	// sharedLoadTimeout bounds one materialization, which outlives the
	// request that started it so the requests after it find it done.
	sharedLoadTimeout = 30 * time.Second
	// maxPendingVersions bounds the versions per environment whose
	// propagation is still to be observed: a version no SDK asks for is
	// eventually forgotten rather than held forever.
	maxPendingVersions = 16
)

// RulesetCache holds, per environment, the ruleset document this replica
// last materialized and the newest version it knows of, learned from the
// notification of each bump on RulesetsChannel and, for its own writes,
// before the write is answered. It serves a document only while its
// listener is up and the document is at least the newest version known;
// otherwise it materializes afresh, once per environment however many
// requests ask at the same moment. While the listener is down it keeps
// nothing, since a bump announced then may be missed.
//
// When the listener (re)starts, everything is dropped, the versions known
// included: what the database holds is then the newest there is, even when
// it is older than a version known before, as after a failover or a restore
// that lost recent writes, so the replica never waits for a version the
// database no longer has.
type RulesetCache struct {
	load appdelivery.Rulesets
	// committedLoad materializes the rulesets CommittedRuleset misses.
	committedLoad appdelivery.Rulesets
	clock         clock.Clock
	log           *slog.Logger
	fault         *RulesetLoadFault

	materializations prometheus.Counter
	propagation      *prometheus.HistogramVec

	ctx    context.Context
	cancel context.CancelFunc
	loads  sync.WaitGroup

	mu        sync.Mutex
	listening bool
	closed    bool
	// generation counts the listener going up or down. A load is kept only
	// when it did not change while the load was under way.
	generation uint64
	// started counts the materializations started, numbering each.
	started uint64
	entries map[workspace.EnvironmentID]*rulesetEntry
}

type rulesetEntry struct {
	// latest is the newest version known.
	latest int64
	doc    *appdelivery.Ruleset
	// loading is the materialization in flight, nil when none is.
	loading *rulesetLoad
	// pending are the versions announced whose propagation is still to be
	// observed, the oldest first.
	pending []pendingVersion
	// answered is the newest version an answer carried.
	answered int64
}

type pendingVersion struct {
	version  int64
	bumpedAt time.Time
}

type rulesetLoad struct {
	// seq numbers the load among those the cache started.
	seq  uint64
	done chan struct{}
	rs   appdelivery.Ruleset
	err  error
}

// RulesetCacheOptions are what a RulesetCache runs on.
type RulesetCacheOptions struct {
	// Load materializes a ruleset from the database.
	Load appdelivery.Rulesets
	// CommittedLoad materializes a ruleset from the database for
	// CommittedRuleset, on connections no write holds; Load when nil.
	CommittedLoad appdelivery.Rulesets
	Clock         clock.Clock
	// Registerer receives the cache's metrics.
	Registerer prometheus.Registerer
	Logger     *slog.Logger
	// Fault lets a test stall materializations; nil in production.
	Fault *RulesetLoadFault
}

// NewRulesetCache returns an empty cache, which reads through until its
// listener reports itself up, and registers its metrics.
func NewRulesetCache(o RulesetCacheOptions) *RulesetCache {
	c := &RulesetCache{
		load: o.Load, committedLoad: cmp.Or[appdelivery.Rulesets](o.CommittedLoad, o.Load), clock: o.Clock, log: o.Logger, fault: o.Fault,
		materializations: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "ruleset", Name: "materializations_total",
			Help: "Rulesets this replica materialized from the database.",
		}),
		propagation: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Subsystem: "ruleset", Name: "propagation_seconds",
			Help:    "Time from a ruleset version's bump to this replica's first answer carrying it, by environment.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"environment"}),
		entries: map[workspace.EnvironmentID]*rulesetEntry{},
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	o.Registerer.MustRegister(c.materializations, c.propagation)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

// Close cancels the materializations in flight, which nobody may wait for
// any more, and waits for them; every request after it is answered
// appdelivery.ErrRulesetUnavailable.
func (c *RulesetCache) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	c.loads.Wait()
}

// entry returns the entry of environment, made when there is none. c.mu is
// held.
func (c *RulesetCache) entry(environment workspace.EnvironmentID) *rulesetEntry {
	e, ok := c.entries[environment]
	if !ok {
		e = &rulesetEntry{}
		c.entries[environment] = e
	}
	return e
}

// Ruleset returns the ruleset of environment at the newest version known,
// from memory when it can be trusted, otherwise from a materialization it
// starts or joins. It waits for the materialization as long as ctx lasts,
// then answers appdelivery.ErrRulesetUnavailable; the materialization goes
// on for the requests after it. The answer counts towards the propagation
// of its version: it is an SDK's.
func (c *RulesetCache) Ruleset(ctx context.Context, environment workspace.EnvironmentID) (appdelivery.Ruleset, error) {
	return c.ruleset(ctx, environment, true)
}

// Unmeasured returns the cache as a source of rulesets whose answers do not
// count towards their version's propagation, for a member downloading one:
// the propagation measured is to SDKs, and a download answered first would
// hide the time the first SDK waited.
func (c *RulesetCache) Unmeasured() appdelivery.Rulesets { return unmeasuredRulesets{c} }

// CommittedRuleset returns the ruleset of environment at version, a
// version known to be committed and perhaps not yet announced to this
// replica: from memory when it holds that version, otherwise from a
// materialization through the options' CommittedLoad, which keeps it for
// the requests after. The protection guard reads what a write's
// environment serves before it through it. The guard's write holds a
// connection of the replica's pool while it waits, as may every write
// queued behind its project's row, so the load never takes a connection
// from that pool: it could wait for one the queued writes hold. A ruleset
// at another version is an error: the caller holds the project row every
// bump of environment takes, so the committed version cannot have moved.
// The answer does not count towards the propagation, which is to SDKs.
func (c *RulesetCache) CommittedRuleset(ctx context.Context, environment workspace.EnvironmentID, version int64) (appdelivery.Ruleset, error) {
	c.mu.Lock()
	e := c.entry(environment)
	e.latest = max(e.latest, version)
	if c.listening && e.doc != nil && e.doc.Version == version {
		rs := *e.doc
		c.mu.Unlock()
		return rs, nil
	}
	generation := c.generation
	c.mu.Unlock()
	// The caller waits inside its write's transaction; the load reads what
	// is committed, in a snapshot of its own.
	rs, err := c.committedLoad.Ruleset(pgxtx.Outside(ctx), environment)
	if err != nil {
		return appdelivery.Ruleset{}, err
	}
	if rs.Version != version {
		return appdelivery.Ruleset{}, fmt.Errorf("the committed ruleset of %s is at version %d, want %d", environment, rs.Version, version)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Kept on the terms a shared load is kept on (finish).
	if c.listening && c.generation == generation && c.entries[environment] == e && rs.Version >= e.latest &&
		(e.doc == nil || rs.Version >= e.doc.Version) {
		e.doc = &rs
	}
	return rs, nil
}

type unmeasuredRulesets struct{ c *RulesetCache }

func (u unmeasuredRulesets) Ruleset(ctx context.Context, environment workspace.EnvironmentID) (appdelivery.Ruleset, error) {
	return u.c.ruleset(ctx, environment, false)
}

func (c *RulesetCache) ruleset(ctx context.Context, environment workspace.EnvironmentID, measured bool) (appdelivery.Ruleset, error) {
	c.mu.Lock()
	arrived := c.started
	c.mu.Unlock()
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return appdelivery.Ruleset{}, appdelivery.ErrRulesetUnavailable
		}
		e := c.entry(environment)
		if c.listening && e.doc != nil && e.doc.Version >= e.latest {
			rs := *e.doc
			if measured {
				c.served(environment, e, rs.Version)
			}
			c.mu.Unlock()
			return rs, nil
		}
		l := e.loading
		// While the listener is down no notification tells of a write
		// another replica committed, so a load started before this request
		// may have read the database before that write: only a load started
		// since is joined.
		if l == nil || (!c.listening && l.seq <= arrived) {
			l = c.start(environment, e)
		}
		want := e.latest
		c.mu.Unlock()

		select {
		case <-l.done:
		case <-ctx.Done():
			return appdelivery.Ruleset{}, appdelivery.ErrRulesetUnavailable
		}
		if l.err != nil {
			if !errors.Is(l.err, appdelivery.ErrRulesetUnavailable) {
				c.log.WarnContext(ctx, "materializing a ruleset failed", "environment", environment.String(), "error", l.err.Error())
			}
			return appdelivery.Ruleset{}, appdelivery.ErrRulesetUnavailable
		}
		if l.rs.Version >= want {
			c.mu.Lock()
			if e, ok := c.entries[environment]; ok && measured {
				c.served(environment, e, l.rs.Version)
			}
			c.mu.Unlock()
			return l.rs, nil
		}
		// A newer version was announced while the load was under way:
		// load again rather than answer what is known to be old.
	}
}

// start begins the materialization of environment, shared by every request
// asking for it until it ends. c.mu is held.
func (c *RulesetCache) start(environment workspace.EnvironmentID, e *rulesetEntry) *rulesetLoad {
	c.started++
	l := &rulesetLoad{seq: c.started, done: make(chan struct{})}
	e.loading = l
	generation := c.generation
	c.loads.Add(1)
	safego.Go(c.ctx, "ruleset load", c.log, func(ctx context.Context) {
		defer c.loads.Done()
		l.err = errors.New("the materialization ended without a ruleset")
		defer func() {
			c.finish(environment, e, l, generation)
			close(l.done)
		}()
		ctx, cancel := context.WithTimeout(ctx, sharedLoadTimeout)
		defer cancel()
		if err := c.fault.wait(ctx); err != nil {
			l.err = err
			return
		}
		l.rs, l.err = c.load.Ruleset(ctx, environment)
		if l.err == nil {
			c.materializations.Inc()
		}
	})
	return l
}

// finish keeps what l loaded when it can still be trusted: the listener up
// and unchanged since the load began, the environment not forgotten, and
// the document at least as new as every version known.
func (c *RulesetCache) finish(environment workspace.EnvironmentID, e *rulesetEntry, l *rulesetLoad, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.loading == l {
		e.loading = nil
	}
	if l.err != nil || !c.listening || c.generation != generation || c.entries[environment] != e || l.rs.Version < e.latest {
		return
	}
	if e.doc == nil || l.rs.Version >= e.doc.Version {
		rs := l.rs
		e.doc = &rs
	}
}

// served observes the propagation of every pending version up to version,
// which this replica is about to answer for the first time. c.mu is held.
func (c *RulesetCache) served(environment workspace.EnvironmentID, e *rulesetEntry, version int64) {
	e.answered = max(e.answered, version)
	if len(e.pending) == 0 || e.pending[0].version > version {
		return
	}
	now := c.clock.Now()
	observer := c.propagation.WithLabelValues(environment.String())
	kept := e.pending[:0]
	for _, p := range e.pending {
		if p.version <= version {
			observer.Observe(max(now.Sub(p.bumpedAt), 0).Seconds())
		} else {
			kept = append(kept, p)
		}
	}
	e.pending = kept
}

// Committed is told a version this replica committed at bumpedAt, before
// the write is answered, so its next answer carries it without waiting for
// the notification, and that answer is the one its propagation is measured
// to.
func (c *RulesetCache) Committed(environment workspace.EnvironmentID, version int64, bumpedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.learn(c.entry(environment), version, bumpedAt)
}

// Listening is told when the listener starts or stops listening: either way
// a bump may have been missed, so everything held is dropped. The
// propagation series go too: the deletion of an environment may have been
// missed with the rest, and its series would otherwise stay forever.
func (c *RulesetCache) Listening(up bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listening = up
	c.generation++
	clear(c.entries)
	c.propagation.Reset()
}

// Received is told each bump, as "<environment>:<version>:<bumped at in
// Unix milliseconds>", and each deletion of an environment, as
// "<environment>".
func (c *RulesetCache) Received(payload string) {
	environment, version, bumpedAt, ok := parseRulesetNotification(payload)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !ok:
		// A payload this replica cannot read names an environment it cannot
		// tell apart: trusting nothing it holds is the only safe reading.
		c.log.Warn("unreadable ruleset notification; dropping every cached ruleset", "payload", payload)
		clear(c.entries)
		c.propagation.Reset()
	case version == 0:
		delete(c.entries, environment)
		c.propagation.DeleteLabelValues(environment.String())
	default:
		c.learn(c.entry(environment), version, bumpedAt)
	}
}

// learn records version, bumped at bumpedAt, as known, and as pending its
// propagation unless an answer already carried it or it is pending already,
// as the writing replica's own version is when its notification arrives.
// c.mu is held.
func (c *RulesetCache) learn(e *rulesetEntry, version int64, bumpedAt time.Time) {
	if version > e.latest {
		e.latest = version
	}
	if version <= e.answered || slices.ContainsFunc(e.pending, func(p pendingVersion) bool { return p.version == version }) {
		return
	}
	if len(e.pending) == maxPendingVersions {
		e.pending = e.pending[1:]
	}
	e.pending = append(e.pending, pendingVersion{version: version, bumpedAt: bumpedAt})
}

// parseRulesetNotification reads a payload of RulesetsChannel: a version 0
// is a deletion.
func parseRulesetNotification(payload string) (workspace.EnvironmentID, int64, time.Time, bool) {
	parts := strings.Split(payload, ":")
	environment, err := id.Parse[workspace.Environment](parts[0])
	if err != nil {
		return workspace.EnvironmentID{}, 0, time.Time{}, false
	}
	if len(parts) == 1 {
		return environment, 0, time.Time{}, true
	}
	if len(parts) != 3 {
		return workspace.EnvironmentID{}, 0, time.Time{}, false
	}
	version, err1 := strconv.ParseInt(parts[1], 10, 64)
	millis, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || version <= 0 {
		return workspace.EnvironmentID{}, 0, time.Time{}, false
	}
	return environment, version, time.UnixMilli(millis), true
}

// RulesetLoadFault stalls or fails a replica's materializations, so a test
// can watch it answer while it cannot load a ruleset.
type RulesetLoadFault struct {
	mu       sync.Mutex
	released chan struct{}
	stalled  int
	failing  bool
	failed   int
}

// errRulesetLoadFault is the failure of a materialization the fault fails.
var errRulesetLoadFault = errors.New("the ruleset load fault failed the materialization")

// Fail fails every materialization from now on until Release.
func (f *RulesetLoadFault) Fail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = true
}

// Failed counts the materializations failed so far.
func (f *RulesetLoadFault) Failed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failed
}

// Stall holds every materialization from now on until Release.
func (f *RulesetLoadFault) Stall() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released == nil {
		f.released = make(chan struct{})
	}
}

// Release lets the materializations held go on, and the next ones run.
func (f *RulesetLoadFault) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = false
	if f.released != nil {
		close(f.released)
		f.released = nil
	}
}

// Stalled counts the materializations held now.
func (f *RulesetLoadFault) Stalled() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stalled
}

func (f *RulesetLoadFault) wait(ctx context.Context) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.failing {
		f.failed++
		f.mu.Unlock()
		return errRulesetLoadFault
	}
	released := f.released
	if released == nil {
		f.mu.Unlock()
		return nil
	}
	f.stalled++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.stalled--
		f.mu.Unlock()
	}()
	select {
	case <-released:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
