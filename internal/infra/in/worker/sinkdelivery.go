package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// SinkDeliveryName names the worker delivering sink streams in logs, metrics
// and Tick.
const SinkDeliveryName = "sink_delivery"

// sinksAtOnce is how many sinks one replica attempts at once. A sink's
// streams are attempted one after another, so a stop takes effect before the
// next of them and a sink's failing is told once; different sinks run side by
// side, so one whose target hangs until its timeout delays only itself.
const sinksAtOnce = 16

// sinkRoundFor is how long a round goes on claiming; the attempts it started
// then finish within their own timeout.
const sinkRoundFor = time.Minute

// SinkDeliverer is the use case attempting the sink streams that are due.
type SinkDeliverer interface {
	Claim(ctx context.Context, busy []sinks.ID) (appsinks.Claim, error)
	AttemptSink(ctx context.Context, token appsinks.LeaseToken, c appsinks.ClaimedSink) (map[sinks.EventKind]appsinks.Result, error)
}

// AtRiskEvaluator judges whether sinks are about to lose events to retention.
type AtRiskEvaluator interface {
	Handle(ctx context.Context) error
}

// SinkMetrics are the series the delivery of sinks reports, and what the use
// case tells its observer through.
type SinkMetrics struct {
	batches *prometheus.CounterVec
	lag     *prometheus.GaugeVec
	skipped *prometheus.CounterVec
}

var _ appsinks.Observer = (*SinkMetrics)(nil)

// PendingDeadLetters counts the dead letters waiting, per sink, those with
// none included.
type PendingDeadLetters interface {
	PendingDeadLetters(ctx context.Context) (map[sinks.ID]int, error)
}

// SinkRisk says which sinks are marked at risk.
type SinkRisk interface {
	AtRiskStates(ctx context.Context) (map[sinks.ID]bool, error)
}

// NewSinkMetrics registers the series on reg:
// flagnetic_sink_batches_total by sink, event kind and result,
// flagnetic_sink_lag_seconds by sink and event kind,
// flagnetic_sink_rows_skipped_total by reason, and
// flagnetic_sink_dead_letters by sink and flagnetic_sink_at_risk by sink,
// which are read from pending and risk whenever they are scraped, so that
// every replica answers the same and a sink that is gone leaves no series
// behind.
func NewSinkMetrics(reg prometheus.Registerer, pending PendingDeadLetters, risk SinkRisk) *SinkMetrics {
	m := &SinkMetrics{
		batches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "sink", Name: "batches_total",
			Help: "Batches attempted by sink and event kind, by result: ack, transient, rejected, dead_lettered, redelivered or redelivery_failed.",
		}, []string{"sink", "event_kind", "result"}),
		lag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metrics.Namespace, Subsystem: "sink", Name: "lag_seconds",
			Help: "Age of the oldest event a sink's stream has not delivered, in seconds; zero when it is caught up.",
		}, []string{"sink", "event_kind"}),
		skipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "sink", Name: "rows_skipped_total",
			Help: "Rows a sink passed without exporting them, by reason: environment_deleted.",
		}, []string{"reason"}),
	}
	reg.MustRegister(m.batches, m.lag, m.skipped, deadLetterGauge{pending}, atRiskGauge{risk})
	return m
}

// deadLetterScrapeTimeout bounds how long a scrape waits for the counts.
const deadLetterScrapeTimeout = 5 * time.Second

// deadLetterGauge reports the dead letters waiting, per sink, as they are
// when it is scraped.
type deadLetterGauge struct{ pending PendingDeadLetters }

var deadLetterDesc = prometheus.NewDesc(
	prometheus.BuildFQName(metrics.Namespace, "sink", "dead_letters"),
	"Dead letters waiting to be redelivered or discarded, by sink: neither redelivered, discarded nor expired.",
	[]string{"sink"}, nil)

func (deadLetterGauge) Describe(ch chan<- *prometheus.Desc) { ch <- deadLetterDesc }

func (g deadLetterGauge) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), deadLetterScrapeTimeout)
	defer cancel()
	counts, err := g.pending.PendingDeadLetters(ctx)
	if err != nil {
		// A scrape that cannot count reports nothing, rather than zero.
		return
	}
	for sink, n := range counts {
		ch <- prometheus.MustNewConstMetric(deadLetterDesc, prometheus.GaugeValue, float64(n), sink.String())
	}
}

var atRiskDesc = prometheus.NewDesc(
	prometheus.BuildFQName(metrics.Namespace, "sink", "at_risk"),
	"Whether a sink is at risk of losing events to retention, by sink: 1 while its oldest undelivered event is within the loss warning of the edge of retention, otherwise 0.",
	[]string{"sink"}, nil)

// atRiskGauge reports which sinks are at risk, as they are when it is
// scraped.
type atRiskGauge struct{ risk SinkRisk }

func (atRiskGauge) Describe(ch chan<- *prometheus.Desc) { ch <- atRiskDesc }

func (g atRiskGauge) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), deadLetterScrapeTimeout)
	defer cancel()
	states, err := g.risk.AtRiskStates(ctx)
	if err != nil {
		// A scrape that cannot read reports nothing, rather than zero.
		return
	}
	for sink, atRisk := range states {
		v := 0.0
		if atRisk {
			v = 1
		}
		ch <- prometheus.MustNewConstMetric(atRiskDesc, prometheus.GaugeValue, v, sink.String())
	}
}

// Batch counts one batch attempted.
func (m *SinkMetrics) Batch(sink sinks.ID, kind sinks.EventKind, result string) {
	m.batches.WithLabelValues(sink.String(), string(kind), result).Inc()
}

// Lag sets how far behind a stream is.
func (m *SinkMetrics) Lag(sink sinks.ID, kind sinks.EventKind, lag time.Duration) {
	m.lag.WithLabelValues(sink.String(), string(kind)).Set(lag.Seconds())
}

// RowsSkipped counts rows passed without being exported.
func (m *SinkMetrics) RowsSkipped(reason string, n int) {
	m.skipped.WithLabelValues(reason).Add(float64(n))
}

// SinkDelivery returns the worker delivering the sink streams that are due,
// every interval. A round claims the streams due and starts each claimed
// sink's attempts, its streams one after another, beside the other sinks', and
// goes on claiming while any is under way, so a sink whose target hangs holds
// its own streams' leases and one slot and delays no other; it returns once
// nothing is due and no attempt is under way, so a round driven by Tick leaves
// every attempt it started recorded. A stream whose batch was full is due again
// at once, and is attempted again in the same round.
//
// Each round also judges, once it has delivered, the sinks that are due to be
// judged for being at risk: a sink that cannot be delivered to, or is disabled
// or stopped, is judged all the same.
func SinkDelivery(d SinkDeliverer, risk AtRiskEvaluator, interval time.Duration, log *slog.Logger) Worker {
	return Worker{
		Name: SinkDeliveryName, Interval: interval,
		// The claiming stops after sinkRoundFor; the attempts started by then
		// finish within the longest call a target allows.
		Timeout: sinkRoundFor + 2*sinks.ObjectStorageCallTimeout,
		Round: func(ctx context.Context) error {
			r := &sinkRound{d: d, log: log, slots: make(chan struct{}, sinksAtOnce), busy: map[sinks.ID]bool{}, woken: make(chan struct{}, 1)}
			err := r.run(ctx, interval)
			r.attempts.Wait()
			// A sink is judged after the attempts of the round, so that one
			// that just caught up is not reported at risk for a minute more.
			// A failure to judge is logged and fails nothing: delivery does
			// not depend on it.
			if riskErr := risk.Handle(ctx); riskErr != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "judging which sinks are at risk failed", "error", riskErr.Error())
			}
			return err
		},
	}
}

// sinkRound is one round of the worker: the sinks it is attempting now and the
// slots they hold.
type sinkRound struct {
	d     SinkDeliverer
	log   *slog.Logger
	slots chan struct{}
	woken chan struct{}

	attempts sync.WaitGroup
	mu       sync.Mutex
	busy     map[sinks.ID]bool
}

func (r *sinkRound) run(ctx context.Context, interval time.Duration) error {
	until := time.Now().Add(sinkRoundFor)
	for ctx.Err() == nil && time.Now().Before(until) {
		// Whether nothing was under way when the claim looked: a sink that
		// finished meanwhile may have a stream due that the claim passed
		// over because it was still busy, so only a claim made with every
		// sink finished shows there is nothing left.
		idle := r.idle()
		started, err := r.claim(ctx)
		if err != nil {
			return err
		}
		if started > 0 {
			continue
		}
		if idle {
			return nil
		}
		// Nothing is due, but a sink is still being attempted: wait for it to
		// finish, or for another stream to fall due, whichever comes first.
		select {
		case <-r.woken:
		case <-time.After(interval):
		case <-ctx.Done():
		}
	}
	return ctx.Err()
}

func (r *sinkRound) idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.busy) == 0
}

// claim claims the streams due of the sinks not being attempted and starts
// each sink's attempts; it returns how many sinks it started.
func (r *sinkRound) claim(ctx context.Context) (int, error) {
	r.mu.Lock()
	busy := make([]sinks.ID, 0, len(r.busy))
	for id := range r.busy {
		busy = append(busy, id)
	}
	r.mu.Unlock()
	c, err := r.d.Claim(ctx, busy)
	if err != nil {
		return 0, err
	}
	for _, s := range c.Sinks {
		select {
		case r.slots <- struct{}{}:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		r.mu.Lock()
		r.busy[s.Sink.ID] = true
		r.mu.Unlock()
		r.attempts.Go(func() {
			defer func() {
				r.mu.Lock()
				delete(r.busy, s.Sink.ID)
				r.mu.Unlock()
				<-r.slots
				select {
				case r.woken <- struct{}{}:
				default:
				}
			}()
			_ = safego.Call(ctx, SinkDeliveryName, r.log, func(ctx context.Context) error {
				r.attempt(ctx, c.Token, s)
				return nil
			})
		})
	}
	return len(c.Sinks), nil
}

// attempt attempts one sink's streams and logs what did not go as planned.
func (r *sinkRound) attempt(ctx context.Context, token appsinks.LeaseToken, s appsinks.ClaimedSink) {
	results, err := r.d.AttemptSink(ctx, token, s)
	for kind, res := range results {
		if res == appsinks.LeaseLost {
			// Another replica took the stream while this one waited on the
			// target; its outcome stands, and the target deduplicates on the
			// delivery ID.
			r.log.InfoContext(ctx, "sink attempt not recorded: the lease was lost", "sink", s.Sink.ID.String(), "event_kind", string(kind))
		}
	}
	if err != nil && ctx.Err() == nil {
		r.log.ErrorContext(ctx, "sink delivery failed", "sink", s.Sink.ID.String(), "error", err.Error())
	}
}
