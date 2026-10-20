package flagnetic

import (
	"sort"
	"sync"
)

// Recorder holds in memory what an offline client would report, so a test
// can assert on it: the running evaluation counts, every evaluation
// summary a flush or the reporting interval would have sent, and every
// exposure and alias the client recorded. It is safe for concurrent use,
// and its methods are safe on a nil Recorder.
type Recorder struct {
	mu        sync.Mutex
	counts    map[summaryKey]int64
	summaries []EvaluationSummary
	exposures []Exposure
	aliases   []Alias
}

// SummaryCount is one row of an evaluation summary: how often a flag served
// a variation for an evaluation reason. It carries no unit IDs and no
// values; Variation is empty for local_override and fallback.
type SummaryCount struct {
	Flag      string
	Variation string
	Reason    Reason
	Count     int64
}

type summaryKey struct {
	flag, variation string
	reason          Reason
}

func newRecorder() *Recorder {
	return &Recorder{counts: map[summaryKey]int64{}}
}

func (r *Recorder) count(flag string, d Detail) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[summaryKey{flag, d.Variation, d.Reason}]++
}

// Summary returns the evaluation counts since construction or the last
// Reset, ordered by flag, variation and reason.
func (r *Recorder) Summary() []SummaryCount {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out := make([]SummaryCount, 0, len(r.counts))
	for k, n := range r.counts {
		out = append(out, SummaryCount{Flag: k.flag, Variation: k.variation, Reason: k.reason, Count: n})
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Flag != b.Flag {
			return a.Flag < b.Flag
		}
		if a.Variation != b.Variation {
			return a.Variation < b.Variation
		}
		return a.Reason < b.Reason
	})
	return out
}

// record keeps an evaluation summary the client would have sent.
func (r *Recorder) record(s EvaluationSummary) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summaries = append(r.summaries, s)
}

// EvaluationSummaries returns the evaluation summaries the client would
// have sent since construction or the last Reset, in the order they were
// reported. A summary appears once Flush, Close or the reporting interval
// has closed its window; Summary is the running total, which closing a
// window does not touch.
func (r *Recorder) EvaluationSummaries() []EvaluationSummary {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]EvaluationSummary(nil), r.summaries...)
}

// recordEvent keeps an exposure or an alias the client would have sent.
// An offline client records it at the call itself, so a test asserts on it
// without flushing.
func (r *Recorder) recordEvent(e event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch v := e.(type) {
	case *exposureEvent:
		r.exposures = append(r.exposures, v.public())
	case *aliasEvent:
		r.aliases = append(r.aliases, v.public())
	}
}

// Exposures returns the exposures the client would have sent since
// construction or the last Reset, in the order they were recorded.
// Repeats per experiment, unit and variation are suppressed before they
// reach the recorder, exactly as they are online.
func (r *Recorder) Exposures() []Exposure {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Exposure(nil), r.exposures...)
}

// Aliases returns the aliases the client would have sent since
// construction or the last Reset, in the order they were recorded.
func (r *Recorder) Aliases() []Alias {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Alias(nil), r.aliases...)
}

// Reset forgets everything recorded.
func (r *Recorder) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.counts)
	r.summaries = nil
	r.exposures = nil
	r.aliases = nil
}
