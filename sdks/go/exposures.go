package flagnetic

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults for the exposure and alias queue.
const (
	// defaultFlushInterval is how often the queue becomes ingest batches.
	defaultFlushInterval = 2 * time.Second
	// defaultQueueCapacity is how many events the queue holds before it
	// drops the oldest.
	defaultQueueCapacity = 10000
	// suppressionCapacity bounds one generation of the suppression set;
	// two generations are read, so at most twice that many keys are held.
	suppressionCapacity = 10000
	// detailCapacity bounds one generation of the details an exposure call
	// looks up; two generations are held, as for the suppression set.
	detailCapacity = 10000
)

// Event types of the envelope of the SDK API.
const (
	exposureEventType = "exposure"
	aliasEventType    = "alias"
)

// eventQueueKind names this queue in the kind field of its log events. One
// batch may carry exposures and aliases alike, so the kind names the queue
// rather than an event type, and an alert tells it from the evaluation
// summary reporter's own.
const eventQueueKind = "exposure_or_alias"

// The predeclared attributes an exposure carries from the context.
const (
	attributePlatform   = "platform"
	attributeAppVersion = "app_version"
)

// exposureEvent is one exposure as the events endpoint receives it: a unit
// encountered behaviour that depends on its assignment. The SDK's name and
// version are the envelope's, never the event's.
type exposureEvent struct {
	Type          string `json:"type"`
	EventID       string `json:"event_id"`
	ExperimentKey string `json:"experiment_key"`
	FlagKey       string `json:"flag_key"`
	VariationKey  string `json:"variation_key"`
	UnitType      string `json:"unit_type"`
	UnitID        string `json:"unit_id"`
	// Platform and AppVersion are the context's predeclared attributes,
	// null when it names neither.
	Platform   *string `json:"platform"`
	AppVersion *string `json:"app_version"`
	// EvaluationReason is experiment, or override when an override decided
	// the variation.
	EvaluationReason Reason `json:"evaluation_reason"`
	// Override is the experiment block's own field: every exposure
	// carrying it is excluded from first-exposure analysis.
	Override       bool      `json:"override"`
	RulesetVersion int64     `json:"ruleset_version"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (e *exposureEvent) id() string { return e.EventID }

// aliasEvent links the unit ID in use before sign-up to the one after, so
// that analysis joins anonymous-keyed exposures to user-keyed outcomes.
type aliasEvent struct {
	Type         string    `json:"type"`
	EventID      string    `json:"event_id"`
	FromUnitType string    `json:"from_unit_type"`
	FromUnitID   string    `json:"from_unit_id"`
	ToUnitType   string    `json:"to_unit_type"`
	ToUnitID     string    `json:"to_unit_id"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (e *aliasEvent) id() string { return e.EventID }

// Exposure is one exposure an offline client would have sent, held by the
// Recorder for a test to assert on.
type Exposure struct {
	// EventID is the exposure's UUIDv7, chosen when it was recorded and
	// unchanged across every retry.
	EventID       string
	ExperimentKey string
	FlagKey       string
	VariationKey  string
	UnitType      string
	UnitID        string
	// Platform and AppVersion are the context's predeclared attributes,
	// empty when it names neither.
	Platform   string
	AppVersion string
	// Reason is experiment or override.
	Reason Reason
	// Override marks an exposure a platform override influenced; analysis
	// counts it separately.
	Override       bool
	RulesetVersion int64
	OccurredAt     time.Time
}

func (e *exposureEvent) public() Exposure {
	return Exposure{
		EventID:        e.EventID,
		ExperimentKey:  e.ExperimentKey,
		FlagKey:        e.FlagKey,
		VariationKey:   e.VariationKey,
		UnitType:       e.UnitType,
		UnitID:         e.UnitID,
		Platform:       valueOf(e.Platform),
		AppVersion:     valueOf(e.AppVersion),
		Reason:         e.EvaluationReason,
		Override:       e.Override,
		RulesetVersion: e.RulesetVersion,
		OccurredAt:     e.OccurredAt,
	}
}

// Alias is one alias an offline client would have sent, held by the
// Recorder for a test to assert on.
type Alias struct {
	EventID      string
	FromUnitType string
	FromUnitID   string
	ToUnitType   string
	ToUnitID     string
	OccurredAt   time.Time
}

func (e *aliasEvent) public() Alias {
	return Alias{
		EventID:      e.EventID,
		FromUnitType: e.FromUnitType,
		FromUnitID:   e.FromUnitID,
		ToUnitType:   e.ToUnitType,
		ToUnitID:     e.ToUnitID,
		OccurredAt:   e.OccurredAt,
	}
}

func valueOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// exposureKey is what a repeat is suppressed by: the same unit seeing the
// same variation of the same experiment again.
type exposureKey struct {
	experiment, unitType, unitID, variation string
}

// detailKey names one flag evaluated for one context.
type detailKey struct {
	flag, context string
}

// exposureRecord is what an exposure needs from the most recent evaluation
// of a flag for a context: the experiment block, the evaluation reason and
// the ruleset version the evaluation used. A later ruleset does not
// invalidate it — a cached assignment is a valid assignment, and reporting
// it is the point of the explicit call — but an evaluation that no longer
// carries an experiment block does.
type exposureRecord struct {
	experiment     Experiment
	reason         Reason
	rulesetVersion int64
}

// exposures holds the exposure and alias queue: the explicit exposure
// call, automatic exposure, the alias call, the suppression of repeats and
// the flush loop that posts batches through the ingest client. Everything
// is off with WithExposures(false); in offline mode the events go to the
// Recorder at once and no loop runs.
type exposures struct {
	interval time.Duration
	capacity int
	wait     intervals
	now      func() time.Time
	sleep    func(context.Context, time.Duration) bool
	jitter   func(time.Duration) time.Duration
	// ingest posts the batches; nil in offline mode.
	ingest *ingest
	// record takes an event an offline client would have sent; nil online.
	record func(event)
	log    func(slog.Level, Event, map[string]any)

	// ticks closes the current batch on the flush interval; a test
	// replaces it. Offline there is neither ticker nor loop.
	ticks  <-chan time.Time
	ticker *time.Ticker

	// queueMu guards the queue and the suppression set, both touched when
	// an exposure is recorded. suppressedSince is when the suppression
	// set last expired a generation, so that an entry lives for at least
	// one flush interval.
	queueMu         sync.Mutex
	queue           []event
	suppressed      *generations[exposureKey, struct{}]
	suppressedSince time.Time

	// detailsMu guards the details an exposure call looks up.
	detailsMu sync.Mutex
	details   *generations[detailKey, exposureRecord]
	// experimentFlags holds the keys of the flags whose evaluation has
	// carried an experiment block in this process, as a lock-free read. A
	// flag never in it — the great majority — costs one such read per
	// evaluation and touches nothing else, which is what keeps evaluation
	// off these locks; a flag in it has every detail remembered, so that a
	// ruleset dropping its experiment rule stops the exposures rather than
	// leaving the last assignment to be reported forever.
	experimentFlags sync.Map

	// failures counts the attempts since the last batch the platform
	// answered and notBefore is when the queue may post again, so that a
	// backoff grows across flushes rather than restarting at every one;
	// the flush goroutine alone reads and writes them.
	failures  int
	notBefore time.Time

	// full wakes the loop when the batch size is reached.
	full    chan struct{}
	flushes chan chan error
	closed  chan struct{}
}

// newExposures builds the queue. c.exposures stays nil when exposures are
// off, and every method below is safe on a nil receiver.
func newExposures(c *Client, o options) *exposures {
	now := o.exposureNow
	if now == nil {
		now = time.Now
	}
	sleepFor := o.exposureSleep
	if sleepFor == nil {
		sleepFor = sleep
	}
	jitter := o.jitter
	if jitter == nil {
		jitter = fullJitter
	}
	interval := o.flushInterval
	if interval <= 0 {
		interval = defaultFlushInterval
	}
	capacity := o.queueCapacity
	if capacity <= 0 {
		capacity = defaultQueueCapacity
	}
	q := &exposures{
		interval: interval,
		capacity: capacity,
		wait:     c.wait,
		now:      now,
		sleep:    sleepFor,
		jitter:   jitter,
		log:      c.log,
		// Repeats are suppressed for at least one flush interval: a
		// generation is read while the one after it fills.
		suppressed:      newGenerations[exposureKey, struct{}](suppressionCapacity),
		suppressedSince: now(),
		// The details an exposure call looks up expire by capacity alone:
		// a call may come long after the evaluation it reports, and a
		// quiet process must still find what it served.
		details: newGenerations[detailKey, exposureRecord](detailCapacity),
		full:    make(chan struct{}, 1),
		flushes: make(chan chan error),
		closed:  make(chan struct{}),
	}
	if c.source != nil {
		// Offline: no network, so no queue and no loop either. The events
		// go to the recorder a test reads.
		q.record = c.recorder.recordEvent
		return q
	}
	q.ingest = newIngest(c, now)
	q.ticks = o.exposureTicks
	if q.ticks == nil {
		q.ticker = time.NewTicker(interval)
		q.ticks = q.ticker.C
	}
	return q
}

// offline reports whether the events go to the Recorder rather than to the
// events endpoint. An offline queue runs no loop of its own.
func (q *exposures) offline() bool { return q.record != nil }

// Exposure records that the unit described by fc encountered the behaviour
// the flag key serves. It looks up the most recent detail of that flag for
// that flag context and enqueues an exposure only when the detail carries
// an experiment block; otherwise nothing is sent and a debug log names the
// reason. Evaluation itself never records one, so being assigned is never
// mistaken for being exposed, except for a flag whose automatic_exposure is
// set. ctx comes first as on every getter, for tracing.
//
// A repeat for the same experiment, unit and variation is suppressed for
// at least the flush interval; the platform's own deduplication is what
// analysis relies on.
func (c *Client) Exposure(_ context.Context, fc Context, key string) {
	if c == nil {
		return
	}
	c.exposures.exposure(fc, key)
}

// Alias records that two unit IDs belong to the same person, typically an
// anonymous ID and a user ID after sign-up. It changes no context: an
// experiment that crosses registration keeps hashing on the unit type it
// declared, and the alias is what lets analysis join the two. ctx comes
// first as on every getter, for tracing.
func (c *Client) Alias(_ context.Context, fromUnitType, fromUnitID, toUnitType, toUnitID string) {
	if c == nil {
		return
	}
	c.exposures.alias(fromUnitType, fromUnitID, toUnitType, toUnitID)
}

func (q *exposures) exposure(fc Context, key string) {
	if q == nil {
		return
	}
	record, ok := q.recall(detailKey{key, fingerprint(fc)})
	if !ok {
		q.skipped(key, q.cause(key))
		return
	}
	q.emit(fc, key, record)
}

func (q *exposures) alias(fromUnitType, fromUnitID, toUnitType, toUnitID string) {
	if q == nil {
		return
	}
	now := q.now()
	q.enqueue(&aliasEvent{
		Type:         aliasEventType,
		EventID:      newEventID(now),
		FromUnitType: fromUnitType,
		FromUnitID:   fromUnitID,
		ToUnitType:   toUnitType,
		ToUnitID:     toUnitID,
		OccurredAt:   now.UTC(),
	})
}

// evaluated is called after every evaluation. It remembers the detail an
// exposure call looks up, and, for a flag with automatic_exposure, records
// the exposure at once. An evaluation of a flag that has never carried an
// experiment block costs one lock-free read and nothing more; one that
// stops carrying it forgets what was remembered, so the exposure call
// sends nothing rather than the assignment of an experiment the ruleset no
// longer serves.
func (q *exposures) evaluated(fc Context, key string, d Detail, automatic bool) {
	if q == nil {
		return
	}
	if d.Experiment == nil {
		if _, ever := q.experimentFlags.Load(key); ever {
			q.forget(detailKey{key, fingerprint(fc)}, d.RulesetVersion)
		}
		return
	}
	q.experimentFlags.Store(key, struct{}{})
	record := exposureRecord{experiment: *d.Experiment, reason: d.Reason, rulesetVersion: d.RulesetVersion}
	q.remember(detailKey{key, fingerprint(fc)}, record)
	if automatic {
		q.emit(fc, key, record)
	}
}

// emit builds the exposure and enqueues it, unless the same unit has
// already been recorded seeing this variation of this experiment.
func (q *exposures) emit(fc Context, key string, r exposureRecord) {
	now := q.now()
	repeat := q.suppress(exposureKey{
		experiment: r.experiment.Key,
		unitType:   r.experiment.UnitType,
		unitID:     r.experiment.UnitID,
		variation:  r.experiment.Variation,
	}, now)
	if repeat {
		q.skipped(key, "repeat")
		return
	}
	q.enqueue(&exposureEvent{
		Type:             exposureEventType,
		EventID:          newEventID(now),
		ExperimentKey:    r.experiment.Key,
		FlagKey:          key,
		VariationKey:     r.experiment.Variation,
		UnitType:         r.experiment.UnitType,
		UnitID:           r.experiment.UnitID,
		Platform:         predeclared(fc, attributePlatform),
		AppVersion:       predeclared(fc, attributeAppVersion),
		EvaluationReason: r.reason,
		Override:         r.experiment.Override,
		RulesetVersion:   r.rulesetVersion,
		OccurredAt:       now.UTC(),
	})
}

// skipped logs, at debug, an exposure call that sent nothing and why.
func (q *exposures) skipped(key, cause string) {
	q.log(slog.LevelDebug, EventExposureSkipped, map[string]any{"flag": key, "cause": cause})
}

// cause tells the two mistakes behind an exposure call with nothing to
// report apart, as far as the SDK can: a flag no evaluation of which has
// ever carried an experiment block — the flag has no running experiment,
// or the call names the wrong key — and a flag that has, but not for this
// context, which is the call passing a context the flag was not evaluated
// with, or an evaluation the ruleset has since stopped serving an
// experiment for.
func (q *exposures) cause(key string) string {
	if _, ever := q.experimentFlags.Load(key); ever {
		return "no_detail"
	}
	return "no_experiment"
}

// suppress reports whether this exposure key was already recorded within
// the suppression window, and records it otherwise.
func (q *exposures) suppress(k exposureKey, now time.Time) bool {
	q.queueMu.Lock()
	defer q.queueMu.Unlock()
	if now.Sub(q.suppressedSince) >= q.interval {
		q.suppressed.expire()
		q.suppressedSince = now
	}
	if _, seen := q.suppressed.load(k); seen {
		return true
	}
	q.suppressed.store(k, struct{}{})
	return false
}

// remember keeps the detail of an evaluation, unless one of a later
// ruleset is already held: two goroutines evaluating the same flag for the
// same context across a ruleset swap may finish in either order, and the
// later ruleset is the one to report from.
func (q *exposures) remember(k detailKey, r exposureRecord) {
	q.detailsMu.Lock()
	defer q.detailsMu.Unlock()
	if held, ok := q.details.load(k); ok && held.rulesetVersion > r.rulesetVersion {
		return
	}
	q.details.store(k, r)
}

// forget drops the detail of a flag that no longer carries an experiment
// block, unless what is held came from a later ruleset than the evaluation
// that found none.
func (q *exposures) forget(k detailKey, rulesetVersion int64) {
	q.detailsMu.Lock()
	defer q.detailsMu.Unlock()
	if held, ok := q.details.load(k); ok && held.rulesetVersion > rulesetVersion {
		return
	}
	q.details.delete(k)
}

func (q *exposures) recall(k detailKey) (exposureRecord, bool) {
	q.detailsMu.Lock()
	defer q.detailsMu.Unlock()
	return q.details.load(k)
}

// enqueue puts one event in the queue, dropping the oldest when the queue
// is full, and wakes the loop once the batch size is reached. An offline
// queue hands the event to the recorder instead.
func (q *exposures) enqueue(e event) {
	if q.offline() {
		q.record(e)
		return
	}
	q.queueMu.Lock()
	q.queue = append(q.queue, e)
	dropped := q.trim()
	waiting := len(q.queue)
	q.queueMu.Unlock()
	q.reportDropped(dropped)
	if waiting >= maxEventsPerBatch {
		select {
		case q.full <- struct{}{}:
		default:
		}
	}
}

// trim drops the oldest events over the capacity and returns how many.
// The caller holds queueMu.
func (q *exposures) trim() int {
	if len(q.queue) <= q.capacity {
		return 0
	}
	dropped := len(q.queue) - q.capacity
	copy(q.queue, q.queue[dropped:])
	clear(q.queue[q.capacity:])
	q.queue = q.queue[:q.capacity]
	return dropped
}

func (q *exposures) reportDropped(dropped int) {
	if dropped == 0 {
		return
	}
	q.log(slog.LevelWarn, EventQueueDropped, map[string]any{
		"kind":     eventQueueKind,
		"cause":    "queue_full",
		"events":   dropped,
		"capacity": q.capacity,
	})
}

// take empties the queue.
func (q *exposures) take() []event {
	q.queueMu.Lock()
	defer q.queueMu.Unlock()
	events := q.queue
	q.queue = nil
	return events
}

// putBack returns events the platform did not answer to the front of the
// queue, so the next flush sends them again with the same event IDs. They
// are the oldest, so they are the first dropped when the queue is full.
func (q *exposures) putBack(events []event) {
	if len(events) == 0 {
		return
	}
	q.queueMu.Lock()
	next := make([]event, 0, len(events)+len(q.queue))
	next = append(next, events...)
	next = append(next, q.queue...)
	q.queue = next
	dropped := q.trim()
	q.queueMu.Unlock()
	q.reportDropped(dropped)
}

// run flushes the queue on every tick, whenever a batch has filled and on
// every flush, until ctx is done. An offline queue has no loop: New never
// starts one.
func (q *exposures) run(ctx context.Context) {
	if q == nil || q.offline() {
		return
	}
	defer close(q.closed)
	if q.ticker != nil {
		defer q.ticker.Stop()
	}
	for {
		select {
		case <-q.ticks:
			_ = q.send(ctx)
		case <-q.full:
			_ = q.send(ctx)
		case reply := <-q.flushes:
			reply <- q.send(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// flush sends what the queue holds and waits for the platform's answer, or
// until ctx is done. A send cut short by ctx keeps running on the flush
// goroutine, so Close can bound its wait without abandoning the events.
func (q *exposures) flush(ctx context.Context) error {
	if q == nil || q.offline() {
		// An offline client recorded every event at the call itself.
		return nil
	}
	reply := make(chan error, 1)
	select {
	case q.flushes <- reply:
	case <-q.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// send posts what the queue holds. Nothing queued, nothing sent.
func (q *exposures) send(ctx context.Context) error {
	events := q.take()
	if len(events) == 0 {
		return nil
	}
	if now := q.now(); now.Before(q.notBefore) {
		// The last attempt failed and its backoff has not run out: the
		// events wait in the queue rather than being posted into the same
		// failure on every flush interval.
		q.putBack(events)
		return nil
	}
	batches := q.ingest.pack(events)
	for i, group := range batches {
		res := q.attempt(ctx, group)
		switch res.outcome {
		case sendAccepted:
			q.failures, q.notBefore = 0, time.Time{}
			q.accepted(group, res.result)
		case sendDropped:
			// Any other 4xx: sending it again would fail the same way, so
			// the batch goes and the ones after it are still tried.
			q.dropped(group, res)
		case sendUnauthorized:
			// A refused key ends when an operator restores it, so the
			// queue waits at a backoff growing to the unauthorized cap
			// rather than to the ordinary one.
			q.hold(group, res, q.wait.unauthorizedCap)
			q.putBack(events[indexOf(batches, i):])
			return res.err
		default:
			// Retriable and out of this flush's time: the events stay
			// queued, with their event IDs, and the backoff attempt set
			// carries over, so a platform that keeps failing is asked ever
			// less often instead of every flush interval.
			q.putBack(events[indexOf(batches, i):])
			return res.err
		}
	}
	return nil
}

// attempt sends one batch, retrying it whole — the same events, so the
// same event IDs — on 429, 5xx and network errors, until the platform
// answers it, the flush interval is up or the client closes. The SDK backs
// off on its own rather than honouring Retry-After, so a rate limit whose
// header names minutes does not hold exposures back for minutes; the
// scenarios of conformance/protocol allow either.
//
// The attempt count is the queue's, not this call's, so a flush that runs
// out of time hands the next one a longer wait rather than starting from
// the base again.
func (q *exposures) attempt(ctx context.Context, group []event) sendResult {
	deadline := q.now().Add(q.interval)
	for {
		res := q.ingest.post(ctx, group)
		if res.outcome != sendRetry {
			return res
		}
		wait := q.hold(group, res, q.wait.retryCap)
		if ctx.Err() != nil || q.now().Add(wait).After(deadline) || !q.sleep(ctx, wait) {
			return res
		}
	}
}

// hold counts one failed attempt, logs it, and holds the queue back until
// its backoff is up, returning the wait. The limit the backoff grows to is
// the caller's: an ordinary failure takes the retry cap, a refused key the
// unauthorized one, where only an operator ends it.
func (q *exposures) hold(group []event, res sendResult, limit time.Duration) time.Duration {
	q.failures++
	wait := q.jitter(backoffCeiling(q.failures, q.wait.retryBase, limit))
	q.notBefore = q.now().Add(wait)
	q.failed(group, res, wait)
	return wait
}

// failed logs one attempt that will be made again, with the same event
// IDs, after wait.
func (q *exposures) failed(group []event, res sendResult, wait time.Duration) {
	logIngestFailed(q.log, eventQueueKind, group, res, wait)
}

// accepted logs the events of an answered batch the platform rejected,
// which are never sent again.
func (q *exposures) accepted(group []event, res ingestResult) {
	if len(res.Rejected) == 0 {
		return
	}
	codes := make([]string, 0, len(res.Rejected))
	for _, r := range res.Rejected {
		codes = append(codes, r.Code)
	}
	q.log(slog.LevelWarn, EventQueueDropped, map[string]any{
		"kind": eventQueueKind, "cause": "refused_by_the_platform",
		"events": eventIDs(group), "accepted": res.Accepted,
		"duplicates": res.Duplicates, "rejected": codes,
	})
}

// dropped logs a batch the platform refused for good.
func (q *exposures) dropped(group []event, res sendResult) {
	logBatchDropped(q.log, eventQueueKind, group, res)
}

// predeclared reads one predeclared attribute of a context as a string; a
// context naming none, or naming it as another type, leaves the event's
// field null.
func predeclared(fc Context, name string) *string {
	v, ok := fc.Attributes[name]
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

// fingerprint is a canonical encoding of a context, so that an exposure
// call finds the detail of the very context the flag was evaluated for.
// Every part is written with its length in front, so no unit ID and no
// attribute value can forge a separator.
func fingerprint(fc Context) string {
	var b strings.Builder
	for _, k := range sortedKeys(fc.Units) {
		writeSized(&b, k)
		writeSized(&b, fc.Units[k])
	}
	b.WriteByte('|')
	for _, k := range sortedKeys(fc.Attributes) {
		writeSized(&b, k)
		// encoding/json orders a map's keys, so the same attribute encodes
		// the same way every time.
		encoded, err := json.Marshal(fc.Attributes[k])
		if err != nil {
			encoded = []byte("null")
		}
		writeSized(&b, string(encoded))
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeSized(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
}
