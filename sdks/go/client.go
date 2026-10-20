package flagnetic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azverinus/flagnetic/eval"
)

// Client evaluates flags against the ruleset it holds. Construct it with
// New; it is safe for concurrent use.
type Client struct {
	logger         Logger
	allowOverrides bool
	source         func() ([]byte, error)
	recorder       *Recorder
	// summaries counts every evaluation and reports each interval's
	// window; nil when reporting is off.
	summaries *summaries
	// exposures queues the exposures and aliases; nil when they are off.
	exposures *exposures

	// ruleset is nil at stage built_in.
	ruleset atomic.Pointer[loadedRuleset]
	stage   atomic.Value // Stage
	// stageMu orders stage transitions and their reports.
	stageMu   sync.Mutex
	ready     chan struct{}
	readyOnce sync.Once

	// Online mode only.
	sdkKey       string
	rulesetURL   string
	streamURL    string
	eventsURL    string
	http         *http.Client
	streamHTTP   *http.Client
	ownsHTTP     bool
	storePath    string
	sleep        func(context.Context, time.Duration) bool
	jitter       func(time.Duration) time.Duration
	cancel       context.CancelFunc
	done         chan struct{}
	schemaLogged atomic.Bool
	// staleSince is when the first failure since the last success
	// happened, in Unix nanoseconds; 0 while unset.
	staleSince atomic.Int64
	// revoked marks the key refused, so key_revoked is logged once however
	// many of the fetch loop and the change stream see it.
	revoked atomic.Bool

	wait intervals

	// Change stream. streamReady is closed once the first connection
	// attempt has resolved, so the first fetch follows it; wake asks the
	// fetch loop for a ruleset; target is the highest version an event has
	// announced.
	streamReady     chan struct{}
	streamReadyOnce sync.Once
	streamConnected atomic.Bool
	wake            chan struct{}
	target          atomic.Int64
	// firstConnect and retryHint belong to the stream goroutine alone.
	firstConnect bool
	retryHint    time.Duration

	// overrides is replaced whole under overridesMu, so evaluations read it
	// without locking.
	overrides   atomic.Pointer[map[string]localOverride]
	overridesMu sync.Mutex

	reloadMu       sync.Mutex
	listenersMu    sync.Mutex
	listeners      map[int]func([]string)
	stageListeners map[int]func(Stage)
	nextID         int

	// mismatched holds the flag keys already logged as type_mismatch.
	mismatched sync.Map
}

// loadedRuleset is a decoded ruleset and when it was received.
type loadedRuleset struct {
	rs         *eval.Ruleset
	receivedAt time.Time
}

// Option configures a Client.
type Option func(*options)

type options struct {
	source         func() ([]byte, error)
	logger         Logger
	allowOverrides bool
	httpClient     *http.Client
	requestTimeout time.Duration
	pollInterval   time.Duration
	changeStream   *bool
	storePath      string
	sleep          func(context.Context, time.Duration) bool
	jitter         func(time.Duration) time.Duration
	timeDivisor    int

	// Evaluation summaries.
	sendSummaries   *bool
	summaryInterval time.Duration
	summaryTicks    <-chan time.Time
	summarySleep    func(context.Context, time.Duration) bool
	summaryNow      func() time.Time

	// Exposures and aliases.
	sendExposures *bool
	flushInterval time.Duration
	queueCapacity int
	exposureTicks <-chan time.Time
	exposureSleep func(context.Context, time.Duration) bool
	exposureNow   func() time.Time
}

// WithOffline makes the client offline, reading document: a ruleset
// document exactly as the ruleset endpoint serves it. No network call is
// ever made.
func WithOffline(document []byte) Option {
	doc := bytes.Clone(document)
	return WithOfflineSource(func() ([]byte, error) { return doc, nil })
}

// WithOfflineFile makes the client offline, reading the ruleset document at
// path at construction and on every Reload.
func WithOfflineFile(path string) Option {
	return WithOfflineSource(func() ([]byte, error) { return os.ReadFile(path) }) // #nosec G304 -- the caller names its own document
}

// WithOfflineSource makes the client offline, calling read for the ruleset
// document at construction and on every Reload.
func WithOfflineSource(read func() ([]byte, error)) Option {
	return func(o *options) { o.source = read }
}

// WithLogger replaces the default logger, SlogLogger(nil, slog.LevelWarn).
func WithLogger(l Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithLocalOverrides enables Client.LocalOverrides. Without it every Set fails
// with ErrLocalOverridesNotAllowed. Construction logs local_overrides_allowed as a
// warning, so a build shipped with the option is noticed.
func WithLocalOverrides() Option {
	return func(o *options) { o.allowOverrides = true }
}

// WithStore persists every accepted ruleset to the file at path, readable
// by its owner only, and loads it at construction as last_good before any
// network call. A ruleset the store cannot load or save logs store_failed;
// a corrupt file leaves the client at built_in.
func WithStore(path string) Option {
	return func(o *options) { o.storePath = path }
}

// WithPollInterval sets how often the ruleset is fetched with
// If-None-Match while the change stream is connected; default 5 minutes,
// at least 30 seconds. While the stream is down or disabled the safety
// poll runs every 30 seconds instead, so a kill switch is never further
// away than that.
func WithPollInterval(d time.Duration) Option {
	return func(o *options) { o.pollInterval = d }
}

// WithChangeStream turns the change stream on or off; it is on by
// default. The stream carries nothing but the new ruleset version, so a
// kill switch reaches the client within two seconds; with it off the
// safety poll every 30 seconds is the only notice, which is what a proxy
// that blocks text/event-stream leaves.
func WithChangeStream(enabled bool) Option {
	return func(o *options) { o.changeStream = &enabled }
}

// WithRequestTimeout bounds each request; default 10 seconds.
func WithRequestTimeout(d time.Duration) Option {
	return func(o *options) { o.requestTimeout = d }
}

// WithHTTPClient sends requests through hc, for a proxy or custom TLS.
// Its own Timeout, if any, applies besides WithRequestTimeout. The change
// stream is long-lived, so it runs through a copy of hc without that
// Timeout; everything else about hc, transport included, is shared.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithSummaries turns evaluation summaries on or off; they are on by
// default. With them off the client counts nothing and posts nothing, so
// stale flags and this SDK's version in the wild stop being visible.
// Offline mode implies off: the counts go to the Recorder instead.
func WithSummaries(enabled bool) Option {
	return func(o *options) { o.sendSummaries = &enabled }
}

// WithSummaryInterval sets how often a window of evaluation counts becomes
// one report; default 60 seconds. It is also how long a report may be
// retried before its window merges into the next one.
func WithSummaryInterval(d time.Duration) Option {
	return func(o *options) { o.summaryInterval = d }
}

// WithExposures turns exposures and aliases on or off; they are on by
// default. With them off Exposure and Alias record nothing and no event
// leaves the process, so running experiments receive no data; construction
// logs exposures_disabled as a warning, so a build shipped with the option
// is noticed. Offline mode keeps recording them in the Recorder instead of
// sending them.
func WithExposures(enabled bool) Option {
	return func(o *options) { o.sendExposures = &enabled }
}

// WithFlushInterval sets how often the queue of exposures and aliases
// becomes ingest batches; default 2 seconds. It is also how long one batch
// may be retried before its events go back into the queue, and how long a
// repeat per experiment, unit and variation is suppressed for.
func WithFlushInterval(d time.Duration) Option {
	return func(o *options) { o.flushInterval = d }
}

// New constructs a client for a server SDK key and the base URL of a
// Flagnetic instance, and starts fetching the ruleset in the background.
// It returns at once at stage built_in, or last_good when WithStore holds a
// ruleset; call Ready to wait for the first fetch. New fails only for a
// client SDK key: network trouble is never a construction error.
//
// With WithOffline, WithOfflineFile or WithOfflineSource the client is
// offline instead: sdkKey and baseURL are ignored, nothing touches the
// network, and New fails when the document cannot be read or decoded.
func New(sdkKey, baseURL string, opts ...Option) (*Client, error) {
	o := options{logger: SlogLogger(nil, slog.LevelWarn)}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	c := &Client{
		logger:         o.logger,
		allowOverrides: o.allowOverrides,
		source:         o.source,
		ready:          make(chan struct{}),
		listeners:      map[int]func([]string){},
		stageListeners: map[int]func(Stage){},
	}
	c.stage.Store(StageBuiltIn)
	if o.source == nil && strings.HasPrefix(sdkKey, clientKeyPrefix) {
		return nil, errors.New("flagnetic: a client SDK key cannot fetch the ruleset; the Go SDK needs a server key")
	}
	if c.allowOverrides {
		c.log(slog.LevelWarn, EventLocalOverridesAllowed, nil)
	}
	if o.source != nil {
		c.recorder = newRecorder()
		if _, err := c.load(); err != nil {
			return nil, err
		}
		// Offline mode makes no network call, so the reports go to the
		// recorder a test reads.
		c.startLoops(o, nil)
		return c, nil
	}

	c.sdkKey = sdkKey
	base := strings.TrimRight(baseURL, "/")
	c.rulesetURL = base + apiPath + "/ruleset"
	c.streamURL = base + apiPath + "/stream"
	c.eventsURL = base + apiPath + "/events"
	c.http = o.httpClient
	if c.http == nil {
		c.http = &http.Client{}
		c.ownsHTTP = true
	}
	// The change stream stays open for an hour at a time, so no Timeout
	// of the caller's client may cut it short.
	streamHTTP := *c.http
	streamHTTP.Timeout = 0
	c.streamHTTP = &streamHTTP
	c.wait = newIntervals(o)
	c.sleep = o.sleep
	if c.sleep == nil {
		c.sleep = sleep
	}
	c.jitter = o.jitter
	if c.jitter == nil {
		c.jitter = fullJitter
	}
	c.storePath = o.storePath
	if c.storePath != "" {
		c.loadStore()
	}
	c.streamReady = make(chan struct{})
	c.wake = make(chan struct{}, 1)
	c.firstConnect = true
	c.startLoops(o, func(ctx context.Context, loops *sync.WaitGroup) {
		if o.changeStream == nil || *o.changeStream {
			loops.Add(1)
			go func() {
				defer loops.Done()
				c.streamLoop(ctx)
			}()
		} else {
			// Nothing will open a stream: the first fetch need not wait.
			c.firstAttemptDone()
		}
		loops.Add(1)
		go func() {
			defer loops.Done()
			c.run(ctx)
		}()
	})
	return c, nil
}

// startLoops builds the summary reporter, unless reporting is off, and
// starts it beside whatever loops the mode needs. Close cancels them all
// and waits for c.done.
func (c *Client) startLoops(o options, online func(context.Context, *sync.WaitGroup)) {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	var loops sync.WaitGroup
	if o.sendSummaries == nil || *o.sendSummaries {
		c.summaries = newSummaries(c, o)
		if !c.summaries.offline() {
			loops.Add(1)
			go func() {
				defer loops.Done()
				c.summaries.run(ctx)
			}()
		}
	}
	if o.sendExposures == nil || *o.sendExposures {
		c.exposures = newExposures(c, o)
		if !c.exposures.offline() {
			loops.Add(1)
			go func() {
				defer loops.Done()
				c.exposures.run(ctx)
			}()
		}
	} else {
		c.log(slog.LevelWarn, EventExposuresDisabled, nil)
	}
	if online != nil {
		online(ctx, &loops)
	}
	go func() {
		loops.Wait()
		close(c.done)
	}()
}

// intervals are the SDK's default waits: what the two
// background loops wait for, and the bounds their backoffs grow to.
type intervals struct {
	// requestTimeout bounds one ruleset request. The change stream is
	// long-lived and is bounded by heartbeat instead.
	requestTimeout time.Duration
	// poll is the safety poll while the change stream is connected,
	// downPoll while it is down or disabled.
	poll     time.Duration
	downPoll time.Duration
	// retryBase, retryCap and unauthorizedCap bound the fetch backoff;
	// streamRetryCap bounds the reconnect backoff, closeJitterCap the wait
	// after a server-side close, and stableFor is how long a connection
	// must hold for that backoff to start from retryBase again.
	retryBase       time.Duration
	retryCap        time.Duration
	unauthorizedCap time.Duration
	streamRetryCap  time.Duration
	closeJitterCap  time.Duration
	stableFor       time.Duration
	// heartbeat is how long the change stream may stay silent.
	heartbeat time.Duration
	// catchUp are the waits before re-fetching while a replica is behind
	// the version an event announced.
	catchUp []time.Duration
}

// newIntervals derives every wait from the options. A test divides them
// all by one factor, so that a scenario on real time runs in a fraction of
// it; the scenario divides its own durations and windows by the same one.
func newIntervals(o options) intervals {
	divisor := time.Duration(max(o.timeDivisor, 1))
	w := intervals{
		requestTimeout:  o.requestTimeout,
		poll:            o.pollInterval,
		downPoll:        defaultDownPollInterval / divisor,
		retryBase:       defaultRetryBase / divisor,
		retryCap:        defaultRetryCap / divisor,
		unauthorizedCap: defaultUnauthorizedCap / divisor,
		streamRetryCap:  streamRetryCap / divisor,
		closeJitterCap:  closeJitterCap / divisor,
		stableFor:       stableFor / divisor,
		heartbeat:       heartbeatTimeout / divisor,
		catchUp:         make([]time.Duration, len(catchUpDelays)),
	}
	if w.requestTimeout <= 0 {
		w.requestTimeout = defaultRequestTimeout
	}
	if w.poll == 0 {
		w.poll = defaultPollInterval / divisor
	}
	w.poll = max(w.poll, minPollInterval/divisor)
	for i, d := range catchUpDelays {
		w.catchUp[i] = d / divisor
	}
	return w
}

// load reads the offline source and swaps the ruleset in, returning the
// keys of the flags whose configuration changed. On failure the held
// ruleset stays.
func (c *Client) load() ([]string, error) {
	doc, err := c.read()
	if err != nil {
		return nil, fmt.Errorf("flagnetic: read offline ruleset: %w", err)
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		return nil, fmt.Errorf("flagnetic: offline ruleset: %w", err)
	}
	changed := c.swap(rs, "offline", time.Now())
	c.setStage(StageFresh)
	c.markReady()
	return changed, nil
}

// swap makes rs the held ruleset and logs ruleset_loaded, returning the
// keys of the flags whose evaluation may have changed, local overrides the
// ruleset drops included.
func (c *Client) swap(rs *eval.Ruleset, source string, receivedAt time.Time) []string {
	prev := c.ruleset.Swap(&loadedRuleset{rs: rs, receivedAt: receivedAt})
	c.log(slog.LevelInfo, EventRulesetLoaded, map[string]any{
		"source":          source,
		"ruleset_version": rs.Version,
		"flags":           len(rs.Flags),
	})
	dropped := c.refuseLocalOverrides(rs)
	before := &eval.Ruleset{}
	if prev != nil {
		before = prev.rs
	}
	return mergeKeys(changedFlags(before, rs), dropped)
}

// setStage moves the fallback chain to stage and reports a transition
// three ways besides Stage and Detail: the stage_changed log event and the
// OnStage listeners.
func (c *Client) setStage(stage Stage) {
	c.stageMu.Lock()
	from := c.Stage()
	if from == stage {
		c.stageMu.Unlock()
		return
	}
	c.stage.Store(stage)
	level := slog.LevelWarn
	if stage == StageFresh {
		level = slog.LevelInfo
	}
	c.log(level, EventStageChanged, map[string]any{"from": string(from), "to": string(stage)})
	c.listenersMu.Lock()
	listeners := inOrder(c.stageListeners)
	c.listenersMu.Unlock()
	for _, l := range listeners {
		c.callStageListener(l, stage)
	}
	c.stageMu.Unlock()
}

func (c *Client) callStageListener(l func(Stage), stage Stage) {
	defer func() {
		if r := recover(); r != nil {
			c.log(slog.LevelError, EventListenerFailed, map[string]any{"cause": fmt.Sprint(r)})
		}
	}()
	l(stage)
}

func (c *Client) markReady() {
	c.readyOnce.Do(func() { close(c.ready) })
}

// read calls the offline source; a panicking source is an error, so that
// no public code path panics.
func (c *Client) read() (doc []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("source panicked: %v", r)
		}
	}()
	return c.source()
}

// changedFlags lists, sorted, the keys of flags whose evaluation may
// differ between two rulesets: flags added, removed or reconfigured, and
// flags that reach, through their conditions, the conditions of the
// segments those name, their overrides, rollouts and prerequisites, a
// segment, a declared attribute or unit type, or a prerequisite flag that
// changed, transitively. A flag's
// own definition names its segments and prerequisites but not their
// contents, so comparing definitions alone misses those.
func changedFlags(before, after *eval.Ruleset) []string {
	flagKey := func(f eval.Flag) string { return f.Key }
	segments := changedKeys(before.Segments, after.Segments, func(s eval.Segment) string { return s.Key })
	attributes := changedKeys(before.Attributes, after.Attributes, func(a eval.Attribute) string { return a.Key })
	unitTypes := changedKeys(before.UnitTypes, after.UnitTypes, func(u string) string { return u })
	changed := changedKeys(before.Flags, after.Flags, flagKey)

	// What each flag reaches, in either ruleset.
	type reach struct{ segments, names, flags map[string]bool }
	reaches := map[string]*reach{}
	for _, rs := range []*eval.Ruleset{before, after} {
		bySegment := make(map[string]eval.Segment, len(rs.Segments))
		for _, s := range rs.Segments {
			bySegment[s.Key] = s
		}
		for _, f := range rs.Flags {
			r := reaches[f.Key]
			if r == nil {
				r = &reach{map[string]bool{}, map[string]bool{}, map[string]bool{}}
				reaches[f.Key] = r
			}
			var walk func(cs []eval.Condition)
			walk = func(cs []eval.Condition) {
				for _, c := range cs {
					switch c.Kind {
					case eval.ConditionInSegment, eval.ConditionNotInSegment:
						if !r.segments[c.Segment] {
							r.segments[c.Segment] = true
							walk(bySegment[c.Segment].Conditions)
						}
					case eval.ConditionPrerequisite:
						r.flags[c.Flag] = true
					default:
						// An attribute, or a unit type read as one.
						r.names[c.Attribute] = true
					}
				}
			}
			for _, p := range f.Prerequisites {
				r.flags[p.Flag] = true
			}
			for _, o := range f.Overrides {
				r.names[o.UnitType] = true
			}
			for _, rule := range f.Rules {
				walk(rule.Conditions)
				r.names[rule.Serve.UnitType] = true
			}
		}
	}
	// A flag is affected by what it reaches directly, and by any
	// prerequisite affected in turn; repeat until nothing more is.
	for grew := true; grew; {
		grew = false
		for k, r := range reaches {
			if changed[k] {
				continue
			}
			if anyOf(r.segments, segments) || anyOf(r.names, attributes) || anyOf(r.names, unitTypes) || anyOf(r.flags, changed) {
				changed[k] = true
				grew = true
			}
		}
	}
	out := make([]string, 0, len(changed))
	for k := range changed {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// changedKeys returns the keys of the items added, removed or changed
// between two lists, comparing items by their JSON encoding.
func changedKeys[T any](before, after []T, key func(T) string) map[string]bool {
	encode := func(items []T) map[string]string {
		m := make(map[string]string, len(items))
		for _, it := range items {
			b, err := json.Marshal(it)
			if err != nil {
				b = nil // unreachable: a decoded item re-encodes
			}
			m[key(it)] = string(b)
		}
		return m
	}
	a, b := encode(before), encode(after)
	changed := map[string]bool{}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			changed[k] = true
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			changed[k] = true
		}
	}
	return changed
}

// anyOf reports whether some key of keys is in set.
func anyOf(keys, set map[string]bool) bool {
	for k := range keys {
		if set[k] {
			return true
		}
	}
	return false
}

// mergeKeys returns the sorted union of two key lists.
func mergeKeys(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range append(a, b...) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Reload re-reads the offline source and swaps the ruleset in, firing
// OnChange listeners with the keys of the flags whose evaluation may have
// changed, through a segment, a declared attribute or unit type or a
// prerequisite as well as their own configuration. On failure it returns the error and keeps the held ruleset.
func (c *Client) Reload() error {
	if c == nil {
		return errors.New("flagnetic: Reload on a nil client")
	}
	if c.source == nil {
		return errors.New("flagnetic: Reload needs an offline client")
	}
	c.reloadMu.Lock()
	changed, err := c.load()
	c.reloadMu.Unlock()
	if err != nil {
		return err
	}
	c.fireChange(changed)
	return nil
}

// Status is the state of the fallback chain: the stage and, since the
// most recent attempt failed, when the failures began.
type Status struct {
	Stage Stage
	// StaleSince is when the first fetch failure since the last success
	// happened; zero after a success and before any attempt.
	StaleSince time.Time
}

// Status returns the stage and the staleness of the held ruleset.
func (c *Client) Status() Status {
	if c == nil {
		return Status{Stage: StageBuiltIn}
	}
	s := Status{Stage: c.Stage()}
	if n := c.staleSince.Load(); n != 0 {
		s.StaleSince = time.Unix(0, n)
	}
	return s
}

// Stage returns the current stage of the fallback chain.
func (c *Client) Stage() Stage {
	if c == nil {
		return StageBuiltIn
	}
	return c.stage.Load().(Stage)
}

// Ready waits for the result of the first fetch, success or failure, or
// until ctx is done, and returns the stage reached: fresh, last_good from
// the store, or built_in. It never fails, and returns at once after Close.
// An offline client is ready at once. Evaluation before Ready is legal and
// follows the fallback chain.
func (c *Client) Ready(ctx context.Context) Stage {
	if c == nil {
		return StageBuiltIn
	}
	select {
	case <-c.ready:
	case <-ctx.Done():
	}
	return c.Stage()
}

// Recorder returns the in-memory recorder of an offline client, or nil
// for an online one.
func (c *Client) Recorder() *Recorder {
	if c == nil {
		return nil
	}
	return c.recorder
}

// OnChange registers listener, called with the sorted keys of the flags
// whose value may have changed: on a new ruleset and on every local
// override change. Listeners run synchronously on the goroutine that caused
// the change, for a fetched ruleset the background loop, so a listener must
// not call Close; a panicking listener is logged as listener_failed. The
// returned function unregisters the listener.
func (c *Client) OnChange(listener func(changed []string)) (remove func()) {
	if c == nil || listener == nil {
		return func() {}
	}
	c.listenersMu.Lock()
	defer c.listenersMu.Unlock()
	id := c.nextID
	c.nextID++
	c.listeners[id] = listener
	return func() {
		c.listenersMu.Lock()
		defer c.listenersMu.Unlock()
		delete(c.listeners, id)
	}
}

func (c *Client) fireChange(changed []string) {
	if len(changed) == 0 {
		return
	}
	c.listenersMu.Lock()
	listeners := inOrder(c.listeners)
	c.listenersMu.Unlock()
	for _, l := range listeners {
		c.callListener(l, append([]string(nil), changed...))
	}
}

// OnStage registers listener, called with the new stage on every
// transition of the fallback chain, in order, on the goroutine that caused
// it; it sees transitions after it is registered, so read Stage after
// registering for the current one. A panicking listener is logged as
// listener_failed. A listener must not call Close. The returned function
// unregisters the listener.
func (c *Client) OnStage(listener func(Stage)) (remove func()) {
	if c == nil || listener == nil {
		return func() {}
	}
	c.listenersMu.Lock()
	defer c.listenersMu.Unlock()
	id := c.nextID
	c.nextID++
	c.stageListeners[id] = listener
	return func() {
		c.listenersMu.Lock()
		defer c.listenersMu.Unlock()
		delete(c.stageListeners, id)
	}
}

// inOrder returns the listeners in registration order. The caller holds
// listenersMu.
func inOrder[L any](m map[int]L) []L {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]L, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}

func (c *Client) callListener(l func([]string), changed []string) {
	defer func() {
		if r := recover(); r != nil {
			c.log(slog.LevelError, EventListenerFailed, map[string]any{"cause": fmt.Sprint(r)})
		}
	}()
	l(changed)
}

// Flush sends the queued exposures and aliases, then closes the window of
// evaluation counts and sends it as one evaluation summary, waiting for
// the platform's answer or until ctx is done. Nothing queued and nothing
// evaluated is nothing sent. An offline client records both in its
// Recorder instead, at the call itself. Flush returns the error of the
// last attempt, so a caller may ignore it: events that failed stay queued
// and a window that failed merges into the next one.
func (c *Client) Flush(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.flushAll(ctx)
}

// flushAll sends the queue of exposures and aliases and the window of
// evaluation counts at once, so that one deadline bounds both rather than
// a slow queue eating the window's share of it, and returns the first
// error either reported.
func (c *Client) flushAll(ctx context.Context) error {
	queued := make(chan error, 1)
	go func() { queued <- c.exposures.flush(ctx) }()
	err := c.summaries.flush(ctx)
	if queueErr := <-queued; err == nil {
		err = queueErr
	}
	return err
}

// Close flushes the queued exposures and aliases and the current window of
// evaluation counts, within five seconds or ctx, then stops fetching and
// polling and returns once the background loops have ended, even during a
// backoff or a request, or when ctx is done. Getters keep serving the held
// ruleset. Close is idempotent.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.cancel == nil {
		return nil
	}
	flushCtx, stopFlush := context.WithTimeout(ctx, closeFlushTimeout)
	_ = c.flushAll(flushCtx)
	stopFlush()
	c.cancel()
	c.markReady()
	select {
	case <-c.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if c.ownsHTTP {
		c.http.CloseIdleConnections()
	}
	return nil
}
