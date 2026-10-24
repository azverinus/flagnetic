// Package events holds the use cases of the events context: ingest of the
// events SDKs post. The SDK edge in the delivery context authenticates the
// key and speaks HTTP, then hands the batch here.
package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// DefaultEventsRetention is how long raw events are kept when the
// configuration does not say: the window within which an event ID is
// recognised as a retry and an event's time is accepted.
const DefaultEventsRetention = 30 * 24 * time.Hour

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// EventStore stores the events SDKs post.
type EventStore interface {
	// DeclaredUnitTypes returns which of keys project declares as unit
	// types.
	DeclaredUnitTypes(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error)
	// KnownFlags returns which of keys name a flag of project, archived
	// ones included.
	KnownFlags(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error)
	// Experiments returns the experiments of project keyed by keys, of any
	// status, by key; a key naming none is absent. It holds each experiment
	// against deletion until the unit of work ends, so that the count of a
	// refusal written meanwhile finds its experiment.
	Experiments(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]events.ExperimentFacts, error)
	// ClaimEvents records the IDs of events of typ of project as received
	// at at, and returns those not received before: the events to store.
	ClaimEvents(ctx context.Context, project workspace.ProjectID, typ events.EventType, ids []delivery.EventID,
		at time.Time) (map[delivery.EventID]bool, error)
	SaveSummaries(ctx context.Context, rows []delivery.SummaryRecord) error
	SaveAliases(ctx context.Context, aliases []events.AliasRecord) error
	SaveExposures(ctx context.Context, exposures []events.ExposureRecord) error
	// CountRejections adds counts to those of exposures refused in
	// environment, as of at.
	CountRejections(ctx context.Context, environment workspace.EnvironmentID, counts []events.RejectionCount, at time.Time) error
	// EnsureEventPartitions makes sure the raw event tables can take rows
	// received at at, outside any unit of work ctx carries.
	EnsureEventPartitions(ctx context.Context, at time.Time) error
}

// IngestEvents stores the events an SDK posts in a batch.
type IngestEvents struct {
	tx        Transactor
	store     EventStore
	clock     clock.Clock
	retention time.Duration
}

// NewIngestEvents returns the handler, accepting events within retention of
// the clock's time.
func NewIngestEvents(tx Transactor, store EventStore, c clock.Clock, retention time.Duration) *IngestEvents {
	return &IngestEvents{tx: tx, store: store, clock: c, retention: retention}
}

// EventBatch is a batch of events as an SDK posts it, each event as sent.
type EventBatch struct {
	SDK delivery.SDKIdentity
	// SentAt is when the SDK says it sent the batch, by its own clock.
	SentAt time.Time
	Events []json.RawMessage
}

// EventRejection is one event of a batch refused.
type EventRejection struct {
	// Index is the event's position in the batch.
	Index int
	// EventID is the event_id as sent, "" when it was not a string.
	EventID string
	events.Rejection
}

// BatchResult is how a batch was judged.
type BatchResult struct {
	// Accepted counts the events stored, by type.
	Accepted map[events.EventType]int
	// Duplicates counts the events received before, in this batch or an
	// earlier one.
	Duplicates int
	// DroppedRows counts the summary rows naming a flag the project does
	// not have.
	DroppedRows int
	// Rejected are the events refused, by index.
	Rejected []EventRejection
}

// AcceptedTotal is how many events were stored, whatever their type.
func (r BatchResult) AcceptedTotal() int {
	n := 0
	for _, c := range r.Accepted {
		n += c
	}
	return n
}

// Handle judges each event of b on its own and stores those accepted in one
// unit of work, so that once it returns every accepted event is durable and
// the SDK may drop the batch. An event whose ID was received before, in this
// batch or within the retention window, is a duplicate and stored once. A
// summary row naming a flag the project does not have is dropped: nobody
// could read it. An exposure is checked against the experiment it names;
// the details of its refusal are specific to a server key and generic to a
// client key.
//
// When the SDK's clock and the server's differ by more than a minute, every
// time in the batch is shifted by the difference before it is judged, so a
// device with a wrong clock keeps its events.
func (h *IngestEvents) Handle(ctx context.Context, key delivery.SDKKey, b EventBatch) (BatchResult, error) {
	now := h.clock.Now()
	skew := events.ClockSkew(b.SentAt, now)
	judged := make([]events.JudgedEvent, len(b.Events))
	for i, raw := range b.Events {
		judged[i] = events.JudgeEvent(raw, now, h.retention, skew)
	}
	// The day's partitions are made sure of before the unit of work begins:
	// creating one takes a connection of its own, and doing it while the
	// unit of work holds another could leave concurrent batches each holding
	// one connection and waiting for a second the pool no longer has.
	if err := h.store.EnsureEventPartitions(ctx, now); err != nil {
		return BatchResult{}, err
	}
	var res BatchResult
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		var err error
		res, err = h.write(ctx, key, b.SDK, judged, now)
		return err
	})
	return res, err
}

// write stores the events judged in the unit of work ctx carries.
func (h *IngestEvents) write(ctx context.Context, key delivery.SDKKey, sdk delivery.SDKIdentity, judged []events.JudgedEvent,
	now time.Time,
) (BatchResult, error) {
	judged = slices.Clone(judged)
	var unitTypes, flags, experimentKeys []string
	for _, e := range judged {
		switch {
		case e.Rejection != nil:
		case e.Alias != nil:
			unitTypes = append(unitTypes, e.Alias.FromUnitType, e.Alias.ToUnitType)
		case e.Summary != nil:
			for _, r := range e.Summary.Rows {
				flags = append(flags, r.Flag)
			}
		case e.Exposure != nil:
			experimentKeys = append(experimentKeys, e.Exposure.ExperimentKey)
		}
	}
	declared, err := h.store.DeclaredUnitTypes(ctx, key.Project, unique(unitTypes))
	if err != nil {
		return BatchResult{}, err
	}
	known, err := h.store.KnownFlags(ctx, key.Project, unique(flags))
	if err != nil {
		return BatchResult{}, err
	}
	named, err := h.store.Experiments(ctx, key.Project, unique(experimentKeys))
	if err != nil {
		return BatchResult{}, err
	}
	// An event is a duplicate within the batch only after an earlier copy
	// that was not refused: a copy of a refused event is refused alike
	// rather than counted as received.
	ids := map[events.EventType][]delivery.EventID{}
	duplicate := make([]bool, len(judged))
	seen := map[delivery.EventID]bool{}
	refused := map[rejectionKey]int64{}
	detailed := key.Kind == delivery.SDKKeyServer
	for i, e := range judged {
		if e.Rejection != nil {
			continue
		}
		if a := e.Alias; a != nil {
			switch {
			case !declared[a.FromUnitType]:
				judged[i] = e.Reject(events.RejectUndeclaredUnitType,
					fmt.Sprintf("from_unit_type %q is not a unit type of this project", a.FromUnitType))
				continue
			case !declared[a.ToUnitType]:
				judged[i] = e.Reject(events.RejectUndeclaredUnitType,
					fmt.Sprintf("to_unit_type %q is not a unit type of this project", a.ToUnitType))
				continue
			}
		}
		if x := e.Exposure; x != nil {
			facts, exists := named[x.ExperimentKey]
			var experiment *events.ExperimentFacts
			if exists {
				experiment = &facts
			}
			if rj := events.CheckExposure(*x, experiment, detailed); rj != nil {
				judged[i] = e.Reject(rj.Code, rj.Detail)
				// Only a refusal for an experiment that exists is counted: a
				// key nobody declared would let whoever holds a client key
				// grow the table with keys of their own making.
				if exists {
					refused[rejectionKey{facts.ID, rj.Code}]++
				}
				continue
			}
		}
		if seen[e.ID] {
			duplicate[i] = true
			continue
		}
		seen[e.ID] = true
		ids[e.Type] = append(ids[e.Type], e.ID)
	}
	// Claimed in a fixed order of types and IDs, so two replicas storing
	// overlapping batches at once wait on each other instead of deadlocking.
	fresh := map[delivery.EventID]bool{}
	for _, typ := range []events.EventType{events.EventEvaluationSummary, events.EventAlias, events.EventExposure} {
		if len(ids[typ]) == 0 {
			continue
		}
		slices.SortFunc(ids[typ], func(a, b delivery.EventID) int {
			x, y := a.Bytes(), b.Bytes()
			return bytes.Compare(x[:], y[:])
		})
		claimed, err := h.store.ClaimEvents(ctx, key.Project, typ, ids[typ], now)
		if err != nil {
			return BatchResult{}, err
		}
		for id := range claimed {
			fresh[id] = true
		}
	}
	res := BatchResult{Accepted: map[events.EventType]int{}}
	var (
		summaries []delivery.SummaryRecord
		aliases   []events.AliasRecord
		exposures []events.ExposureRecord
	)
	for i, e := range judged {
		switch {
		case e.Rejection != nil:
			res.Rejected = append(res.Rejected, EventRejection{Index: i, EventID: e.SentID, Rejection: *e.Rejection})
			continue
		case duplicate[i] || !fresh[e.ID]:
			res.Duplicates++
			continue
		}
		res.Accepted[e.Type]++
		if a := e.Alias; a != nil {
			aliases = append(aliases, events.AliasRecord{EventID: e.ID, Key: key, SDK: sdk, Alias: *a, ReceivedAt: now})
		}
		if x := e.Exposure; x != nil {
			exposures = append(exposures, events.ExposureRecord{
				EventID: e.ID, Key: key, SDK: sdk, Experiment: named[x.ExperimentKey].ID, Exposure: *x, ReceivedAt: now,
			})
		}
		if s := e.Summary; s != nil {
			for _, r := range s.Rows {
				if !known[r.Flag] {
					res.DroppedRows++
					continue
				}
				summaries = append(summaries, delivery.SummaryRecord{
					Source: delivery.SourceSDK, EventID: e.ID, Key: key, SDK: sdk, AppVersion: s.AppVersion,
					RulesetVersion: s.RulesetVersion, PeriodStart: s.IntervalStart, PeriodEnd: s.IntervalEnd, Row: r, ReceivedAt: now,
				})
			}
		}
	}
	if len(summaries) > 0 {
		if err := h.store.SaveSummaries(ctx, summaries); err != nil {
			return BatchResult{}, err
		}
	}
	if len(aliases) > 0 {
		if err := h.store.SaveAliases(ctx, aliases); err != nil {
			return BatchResult{}, err
		}
	}
	if len(exposures) > 0 {
		if err := h.store.SaveExposures(ctx, exposures); err != nil {
			return BatchResult{}, err
		}
	}
	if err := h.countRejections(ctx, key, refused, now); err != nil {
		return BatchResult{}, err
	}
	return res, nil
}

// rejectionKey is what exposures refused are counted by within a batch: the
// experiment and the reason.
type rejectionKey struct {
	experiment experiments.ExperimentID
	reason     events.RejectionCode
}

// countRejections adds the refusals of a batch to the counts of their
// experiments, in the order of experiment and reason, so two replicas
// counting refusals of the same experiments at once wait on each other
// instead of deadlocking.
func (h *IngestEvents) countRejections(ctx context.Context, key delivery.SDKKey, refused map[rejectionKey]int64, at time.Time) error {
	if len(refused) == 0 {
		return nil
	}
	counts := make([]events.RejectionCount, 0, len(refused))
	for k, n := range refused {
		counts = append(counts, events.RejectionCount{Experiment: k.experiment, Reason: k.reason, Count: n})
	}
	slices.SortFunc(counts, func(a, b events.RejectionCount) int {
		x, y := a.Experiment.Bytes(), b.Experiment.Bytes()
		if c := bytes.Compare(x[:], y[:]); c != 0 {
			return c
		}
		return strings.Compare(string(a.Reason), string(b.Reason))
	})
	return h.store.CountRejections(ctx, key.Environment, counts, at)
}

// unique returns the distinct strings of in, sorted.
func unique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
