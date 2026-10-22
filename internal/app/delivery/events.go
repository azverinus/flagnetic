package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// DefaultEventsRetention is how long raw events are kept when the
// configuration does not say: the window within which an event ID is
// recognised as a retry and an event's time is accepted.
const DefaultEventsRetention = 30 * 24 * time.Hour

// EventStore stores the events SDKs post.
type EventStore interface {
	// DeclaredUnitTypes returns which of keys project declares as unit
	// types.
	DeclaredUnitTypes(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error)
	// KnownFlags returns which of keys name a flag of project, archived
	// ones included.
	KnownFlags(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error)
	// ClaimEvents records the IDs of events of typ of project as received
	// at at, and returns those not received before: the events to store.
	ClaimEvents(ctx context.Context, project workspace.ProjectID, typ delivery.EventType, ids []delivery.EventID,
		at time.Time) (map[delivery.EventID]bool, error)
	SaveSummaries(ctx context.Context, rows []delivery.SummaryRecord) error
	SaveAliases(ctx context.Context, aliases []delivery.AliasRecord) error
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
	SDK    delivery.SDKIdentity
	Events []json.RawMessage
}

// EventRejection is one event of a batch refused.
type EventRejection struct {
	// Index is the event's position in the batch.
	Index int
	// EventID is the event_id as sent, "" when it was not a string.
	EventID string
	delivery.Rejection
}

// BatchResult is how a batch was judged.
type BatchResult struct {
	// Accepted counts the events stored, by type.
	Accepted map[delivery.EventType]int
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
// could read it. Every exposure is refused unknown_experiment, whatever its
// form, since no experiment runs yet that an exposure could belong to.
func (h *IngestEvents) Handle(ctx context.Context, key delivery.SDKKey, b EventBatch) (BatchResult, error) {
	now := h.clock.Now()
	judged := make([]delivery.JudgedEvent, len(b.Events))
	for i, raw := range b.Events {
		judged[i] = delivery.JudgeEvent(raw, now, h.retention)
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
func (h *IngestEvents) write(ctx context.Context, key delivery.SDKKey, sdk delivery.SDKIdentity, judged []delivery.JudgedEvent,
	now time.Time,
) (BatchResult, error) {
	judged = slices.Clone(judged)
	var unitTypes, flags []string
	for _, e := range judged {
		switch {
		case e.Rejection != nil:
		case e.Alias != nil:
			unitTypes = append(unitTypes, e.Alias.FromUnitType, e.Alias.ToUnitType)
		case e.Summary != nil:
			for _, r := range e.Summary.Rows {
				flags = append(flags, r.Flag)
			}
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
	// An event is a duplicate within the batch only after an earlier copy
	// that was not refused: a copy of a refused event is refused alike
	// rather than counted as received.
	ids := map[delivery.EventType][]delivery.EventID{}
	duplicate := make([]bool, len(judged))
	seen := map[delivery.EventID]bool{}
	for i, e := range judged {
		if e.Rejection != nil {
			continue
		}
		if a := e.Alias; a != nil {
			switch {
			case !declared[a.FromUnitType]:
				judged[i] = e.Reject(delivery.RejectUndeclaredUnitType,
					fmt.Sprintf("from_unit_type %q is not a unit type of this project", a.FromUnitType))
				continue
			case !declared[a.ToUnitType]:
				judged[i] = e.Reject(delivery.RejectUndeclaredUnitType,
					fmt.Sprintf("to_unit_type %q is not a unit type of this project", a.ToUnitType))
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
	for _, typ := range []delivery.EventType{delivery.EventEvaluationSummary, delivery.EventAlias} {
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
	res := BatchResult{Accepted: map[delivery.EventType]int{}}
	var summaries []delivery.SummaryRecord
	var aliases []delivery.AliasRecord
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
			aliases = append(aliases, delivery.AliasRecord{EventID: e.ID, Key: key, SDK: sdk, Alias: *a, ReceivedAt: now})
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
	return res, nil
}

// unique returns the distinct strings of in, sorted.
func unique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
