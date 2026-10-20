package flagnetic

import (
	"context"
	"time"
)

// WithSleep replaces how the background loop waits between fetches, so a
// test drives polling and backoff without real time passing. sleep returns
// false when ctx is done first.
func WithSleep(sleep func(ctx context.Context, d time.Duration) bool) Option {
	return func(o *options) { o.sleep = sleep }
}

// WithJitter replaces the full jitter drawn under a backoff ceiling.
func WithJitter(jitter func(ceiling time.Duration) time.Duration) Option {
	return func(o *options) { o.jitter = jitter }
}

// WithSummaryTick replaces the reporting interval's ticker, so a test
// decides when a window of evaluation counts closes.
func WithSummaryTick(ticks <-chan time.Time) Option {
	return func(o *options) { o.summaryTicks = ticks }
}

// WithSummarySleep replaces how the reporter waits between the attempts at
// one ingest batch.
func WithSummarySleep(sleep func(ctx context.Context, d time.Duration) bool) Option {
	return func(o *options) { o.summarySleep = sleep }
}

// WithSummaryNow replaces the clock the reporter reads, so a test ages a
// window past the hour at which it is dropped.
func WithSummaryNow(now func() time.Time) Option {
	return func(o *options) { o.summaryNow = now }
}

// WithTimeDivisor divides every interval, timeout and backoff bound the
// client holds by n, so a scenario driven by real time — the change
// stream's, where two loops wait at once — runs in a fraction of it. The
// scenario divides its own durations and windows by the same factor.
func WithTimeDivisor(n int) Option {
	return func(o *options) { o.timeDivisor = n }
}

// WithExposureTick replaces the flush interval's ticker, so a test decides
// when the queue of exposures and aliases becomes ingest batches.
func WithExposureTick(ticks <-chan time.Time) Option {
	return func(o *options) { o.exposureTicks = ticks }
}

// WithExposureSleep replaces how the queue waits between the attempts at
// one ingest batch.
func WithExposureSleep(sleep func(ctx context.Context, d time.Duration) bool) Option {
	return func(o *options) { o.exposureSleep = sleep }
}

// WithExposureNow replaces the clock the queue reads, so a test ages the
// suppression set past its window.
func WithExposureNow(now func() time.Time) Option {
	return func(o *options) { o.exposureNow = now }
}

// WithQueueCapacity replaces how many exposures and aliases the queue
// holds before it drops the oldest, so a test reaches the bound without
// ten thousand events.
func WithQueueCapacity(n int) Option {
	return func(o *options) { o.queueCapacity = n }
}
