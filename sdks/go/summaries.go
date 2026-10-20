package flagnetic

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Defaults for the evaluation summary.
const (
	defaultSummaryInterval = time.Minute
	// maxWindowAge is how old the interval of a window whose report failed
	// may be before the window is dropped rather than merged forward: an
	// hour, so a long outage does not eventually deliver counts nobody can
	// place. The interval's start is what ages, because a window that
	// merges forward is measured from where its counting began.
	maxWindowAge = time.Hour
	// closeFlushTimeout bounds the flush Close performs.
	closeFlushTimeout = 5 * time.Second
)

// summaryEventType is the event type of an evaluation summary in the
// envelope.
const summaryEventType = "evaluation_summary"

// summaryEvent is one evaluation summary as the events endpoint receives
// it: counts per flag, variation and evaluation reason over an interval,
// and nothing else. No unit ID, attribute or flag value is in it, by
// construction.
type summaryEvent struct {
	Type           string       `json:"type"`
	EventID        string       `json:"event_id"`
	IntervalStart  time.Time    `json:"interval_start"`
	IntervalEnd    time.Time    `json:"interval_end"`
	RulesetVersion int64        `json:"ruleset_version"`
	Counts         []summaryRow `json:"counts"`
}

func (e *summaryEvent) id() string { return e.EventID }

// summaryRow is one row of an evaluation summary. Variation is null when
// no variation was served, as on the reasons fallback, error and
// local_override.
type summaryRow struct {
	Flag      string  `json:"flag"`
	Variation *string `json:"variation"`
	Reason    Reason  `json:"reason"`
	Count     int64   `json:"count"`
}

// EvaluationSummary is one evaluation summary an offline client would have
// sent, held by the Recorder for a test to assert on.
type EvaluationSummary struct {
	// EventID is the report's deduplication key, a lowercase UUIDv7.
	EventID        string
	IntervalStart  time.Time
	IntervalEnd    time.Time
	RulesetVersion int64
	Counts         []SummaryCount
}

// public is the summary in the shape the Recorder exposes.
func (e *summaryEvent) public() EvaluationSummary {
	out := EvaluationSummary{
		EventID:        e.EventID,
		IntervalStart:  e.IntervalStart,
		IntervalEnd:    e.IntervalEnd,
		RulesetVersion: e.RulesetVersion,
		Counts:         make([]SummaryCount, 0, len(e.Counts)),
	}
	for _, r := range e.Counts {
		out.Counts = append(out.Counts, SummaryCount{
			Flag: r.Flag, Variation: variationOf(r), Reason: r.Reason, Count: r.Count,
		})
	}
	return out
}

// summaryWindow is what was evaluated over one interval.
type summaryWindow struct {
	start, end time.Time
	counts     map[summaryKey]int64
}

// summaries counts every evaluation and reports each interval's window
// through the ingest client. Reporting is off with WithSummaries(false);
// in offline mode the window goes to the Recorder instead, on the
// goroutine that asked for it, so an offline client holds none of its own.
type summaries struct {
	interval time.Duration
	wait     intervals
	now      func() time.Time
	sleep    func(context.Context, time.Duration) bool
	jitter   func(time.Duration) time.Duration
	// version is the ruleset version held at the interval's end.
	version func() int64
	// ingest posts the batches; nil in offline mode.
	ingest *ingest
	// record takes the summary an offline client would have sent; nil
	// online.
	record func(EvaluationSummary)
	log    func(slog.Level, Event, map[string]any)

	// ticks closes a window on the reporting interval; a test replaces it.
	// Offline there is neither ticker nor loop.
	ticks  <-chan time.Time
	ticker *time.Ticker

	// mu guards the window being counted into.
	mu      sync.Mutex
	start   time.Time
	current map[summaryKey]int64
	// kept is a window whose report failed, merged into the next one.
	kept *summaryWindow

	// reportMu serializes the reports of an offline client, which has no
	// loop to serialize them for it.
	reportMu sync.Mutex

	// refusals counts the consecutive refusals of the SDK key and
	// notBefore is when a refused reporter may post again; the reporting
	// goroutine alone reads and writes them.
	refusals  int
	notBefore time.Time

	flushes chan chan error
	closed  chan struct{}
}

// newSummaries builds the reporter. c.summaries stays nil when reporting
// is off, and every method below is safe on a nil receiver.
func newSummaries(c *Client, o options) *summaries {
	now := o.summaryNow
	if now == nil {
		now = time.Now
	}
	sleepFor := o.summarySleep
	if sleepFor == nil {
		sleepFor = sleep
	}
	jitter := o.jitter
	if jitter == nil {
		jitter = fullJitter
	}
	interval := o.summaryInterval
	if interval <= 0 {
		interval = defaultSummaryInterval
	}
	s := &summaries{
		interval: interval,
		wait:     c.wait,
		now:      now,
		sleep:    sleepFor,
		jitter:   jitter,
		version:  c.heldVersion,
		log:      c.log,
		start:    now(),
		current:  map[summaryKey]int64{},
		flushes:  make(chan chan error),
		closed:   make(chan struct{}),
	}
	if c.source != nil {
		// Offline: no network, so no interval either. Flush and Close hand
		// the window to the recorder a test reads.
		s.record = c.recorder.record
		return s
	}
	s.ingest = newIngest(c, now)
	s.ticks = o.summaryTicks
	if s.ticks == nil {
		s.ticker = time.NewTicker(interval)
		s.ticks = s.ticker.C
	}
	return s
}

// offline reports whether the window goes to the Recorder rather than to
// the events endpoint. An offline reporter runs no loop of its own.
func (s *summaries) offline() bool { return s.record != nil }

// count records one served value: the flag, the variation and the
// evaluation reason, never the value and never the context.
func (s *summaries) count(flag string, d Detail) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current[summaryKey{flag, d.Variation, d.Reason}]++
}

// run closes a window on every tick and on every flush, until ctx is done.
// An offline reporter has no loop: New never starts one.
func (s *summaries) run(ctx context.Context) {
	if s == nil || s.offline() {
		return
	}
	defer close(s.closed)
	if s.ticker != nil {
		defer s.ticker.Stop()
	}
	for {
		select {
		case <-s.ticks:
			_ = s.report(ctx)
		case reply := <-s.flushes:
			reply <- s.report(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// flush closes the current window and waits for its report, or until ctx
// is done. A report cut short by ctx keeps running on the reporting
// goroutine, so Close can bound its wait without abandoning the window.
func (s *summaries) flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.offline() {
		s.reportMu.Lock()
		defer s.reportMu.Unlock()
		return s.report(ctx)
	}
	reply := make(chan error, 1)
	select {
	case s.flushes <- reply:
	case <-s.closed:
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

// report turns the window into summary events and delivers them. Nothing
// evaluated, nothing sent.
func (s *summaries) report(ctx context.Context) error {
	w := s.take()
	if w == nil {
		return nil
	}
	events := s.events(w)
	if s.offline() {
		for _, e := range events {
			s.record(e.(*summaryEvent).public())
		}
		return nil
	}
	if now := s.now(); now.Before(s.notBefore) {
		// The key was refused and its backoff has not run out: the rows
		// wait in the window rather than being posted into a 401.
		s.keep(w)
		return nil
	}
	return s.deliver(ctx, events)
}

// take closes the current window, merging in the one a failed report kept
// unless its interval began over an hour ago, and returns nil when
// nothing was evaluated. Dropping an aged window never drops this
// interval's own counts with it.
func (s *summaries) take() *summaryWindow {
	now := s.now()
	s.mu.Lock()
	w := &summaryWindow{start: s.start, end: now, counts: s.current}
	kept := s.kept
	s.start, s.current, s.kept = now, map[summaryKey]int64{}, nil
	s.mu.Unlock()
	if kept != nil {
		if age := now.Sub(kept.start); age > maxWindowAge {
			s.log(slog.LevelWarn, EventQueueDropped, map[string]any{
				"kind":  summaryEventType,
				"rows":  len(kept.counts),
				"cause": "window_older_than_one_hour",
				"age":   age.String(),
			})
		} else {
			// A failed window merges into the next: the interval begins
			// where the failed one did and the rows are added together.
			if kept.start.Before(w.start) {
				w.start = kept.start
			}
			for k, n := range kept.counts {
				w.counts[k] += n
			}
		}
	}
	if len(w.counts) == 0 {
		return nil
	}
	return w
}

// keep puts a window back, to merge into the next one.
func (s *summaries) keep(w *summaryWindow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kept == nil {
		s.kept = w
		return
	}
	if w.start.Before(s.kept.start) {
		s.kept.start = w.start
	}
	for k, n := range w.counts {
		s.kept.counts[k] += n
	}
}

// events turns a window into summary events: the rows ordered, at most
// 1000 to an event, each with its own UUIDv7 and all naming the window's
// interval and the ruleset version held at its end.
func (s *summaries) events(w *summaryWindow) []event {
	rows := make([]summaryRow, 0, len(w.counts))
	for k, n := range w.counts {
		row := summaryRow{Flag: k.flag, Reason: k.reason, Count: n}
		if k.variation != "" {
			variation := k.variation
			row.Variation = &variation
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Flag != b.Flag {
			return a.Flag < b.Flag
		}
		if av, bv := variationOf(a), variationOf(b); av != bv {
			return av < bv
		}
		return a.Reason < b.Reason
	})
	version := s.version()
	var events []event
	for len(rows) > 0 {
		n := min(len(rows), maxSummaryRows)
		events = append(events, &summaryEvent{
			Type:           summaryEventType,
			EventID:        newEventID(s.now()),
			IntervalStart:  w.start.UTC(),
			IntervalEnd:    w.end.UTC(),
			RulesetVersion: version,
			// Capped, so that appending to one event's rows can never
			// reach into the next one's.
			Counts: rows[:n:n],
		})
		rows = rows[n:]
	}
	return events
}

func variationOf(r summaryRow) string {
	if r.Variation == nil {
		return ""
	}
	return *r.Variation
}

// deliver sends the batches of one window. A batch the platform answered
// is done, rejections included; a batch the client gave up on takes
// itself and every batch after it back into the next window.
func (s *summaries) deliver(ctx context.Context, events []event) error {
	batches := s.ingest.pack(events)
	for i, group := range batches {
		res := s.attempt(ctx, group)
		switch res.outcome {
		case sendAccepted:
			s.refusals, s.notBefore = 0, time.Time{}
			s.accepted(group, res.result)
		case sendDropped:
			s.dropped(group, res)
		case sendUnauthorized:
			// A refused key ends when an operator restores it, so the
			// reporter waits — indefinitely, at a backoff growing to the
			// unauthorized cap — rather than posting every interval.
			s.refusals++
			wait := s.jitter(backoffCeiling(s.refusals, s.wait.retryBase, s.wait.unauthorizedCap))
			s.notBefore = s.now().Add(wait)
			s.failed(group, res, wait)
			s.keep(windowOf(events[indexOf(batches, i):]))
			return res.err
		default:
			// Retriable and out of time: the rows merge into the next
			// window rather than being sent with these event IDs forever.
			s.keep(windowOf(events[indexOf(batches, i):]))
			return res.err
		}
	}
	return nil
}

// indexOf is where the ith batch begins in the events it was packed from.
func indexOf(batches [][]event, i int) int {
	at := 0
	for _, group := range batches[:i] {
		at += len(group)
	}
	return at
}

// attempt sends one batch, retrying it whole — the same events, so the
// same event IDs — on 429 (honouring Retry-After), 5xx and network errors,
// until the platform answers it, the reporting interval is up or the
// client closes. A refused key is not retried here: its backoff outlasts
// any one interval, so deliver schedules it instead.
func (s *summaries) attempt(ctx context.Context, group []event) sendResult {
	deadline := s.now().Add(s.interval)
	for failures := 1; ; failures++ {
		res := s.ingest.post(ctx, group)
		if res.outcome != sendRetry {
			return res
		}
		wait := s.jitter(backoffCeiling(failures, s.wait.retryBase, s.wait.retryCap))
		if res.retryAfter > 0 {
			wait = res.retryAfter
		}
		s.failed(group, res, wait)
		if ctx.Err() != nil || s.now().Add(wait).After(deadline) {
			return res
		}
		if !s.sleep(ctx, wait) {
			return res
		}
	}
}

// failed logs one attempt that will be made again, with the same event
// IDs, after wait.
func (s *summaries) failed(group []event, res sendResult, wait time.Duration) {
	logIngestFailed(s.log, summaryEventType, group, res, wait)
}

// accepted logs what the platform did with a batch it answered: rows it
// dropped because it does not know the flag, and events it rejected, which
// are never sent again.
func (s *summaries) accepted(group []event, res ingestResult) {
	if res.DroppedRows == 0 && len(res.Rejected) == 0 {
		return
	}
	fields := map[string]any{"kind": summaryEventType, "cause": "refused_by_the_platform",
		"events": eventIDs(group), "accepted": res.Accepted,
		"duplicates": res.Duplicates, "dropped_rows": res.DroppedRows}
	if len(res.Rejected) > 0 {
		codes := make([]string, 0, len(res.Rejected))
		for _, r := range res.Rejected {
			codes = append(codes, r.Code)
		}
		fields["rejected"] = codes
	}
	s.log(slog.LevelWarn, EventQueueDropped, fields)
}

// dropped logs a batch the platform refused for good: a 4xx other than
// 429 means sending it again would fail the same way.
func (s *summaries) dropped(group []event, res sendResult) {
	logBatchDropped(s.log, summaryEventType, group, res)
}

// eventIDs are the event IDs of a batch, so a log names the reports that
// went.
func eventIDs(group []event) []string {
	out := make([]string, 0, len(group))
	for _, e := range group {
		out = append(out, e.id())
	}
	return out
}

// windowOf rebuilds a window from the summary events that were not
// delivered, so their rows merge into the next one.
func windowOf(events []event) *summaryWindow {
	w := &summaryWindow{counts: map[summaryKey]int64{}}
	for _, e := range events {
		se, ok := e.(*summaryEvent)
		if !ok {
			continue
		}
		if w.start.IsZero() || se.IntervalStart.Before(w.start) {
			w.start = se.IntervalStart
		}
		if se.IntervalEnd.After(w.end) {
			w.end = se.IntervalEnd
		}
		for _, r := range se.Counts {
			w.counts[summaryKey{r.Flag, variationOf(r), r.Reason}] += r.Count
		}
	}
	return w
}
