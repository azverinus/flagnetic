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
// configuration does not say: the window within which an event's time,
// shifted by the batch's clock skew, is accepted.
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
	// FlagVariations returns the variation keys of each flag of project
	// that keys name, archived ones included, by flag key; a key naming no
	// flag is absent.
	FlagVariations(ctx context.Context, project workspace.ProjectID, keys []string) (map[string][]string, error)
	// Experiments returns the experiments of project keyed by keys, of any
	// status, by key; a key naming none is absent. It holds each experiment
	// against deletion until the unit of work ends, so that the count of a
	// refusal written meanwhile finds its experiment.
	Experiments(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]events.ExperimentFacts, error)
	// ClaimEvents records the IDs of events of typ of project as received
	// at at, and returns those not received before: the events to store.
	ClaimEvents(ctx context.Context, project workspace.ProjectID, typ events.EventType, ids []delivery.EventID,
		at time.Time) (map[delivery.EventID]bool, error)
	// AliasLinkCount returns how many alias links environment holds.
	AliasLinkCount(ctx context.Context, environment workspace.EnvironmentID) (int64, error)
	// LinkedPairs returns which of pairs are already linked in environment.
	LinkedPairs(ctx context.Context, environment workspace.EnvironmentID, pairs []events.AliasPair) (map[events.AliasPair]bool, error)
	// UnitsFirstExposed returns how many units of each of experiments the
	// rollup has counted a first exposure of in environment.
	UnitsFirstExposed(ctx context.Context, environment workspace.EnvironmentID, experiments []experiments.ExperimentID,
	) (map[experiments.ExperimentID]int64, error)
	// ExposedUnits returns which of units have a first exposure stored.
	ExposedUnits(ctx context.Context, units []events.UnitKey) (map[events.UnitKey]bool, error)
	SaveSummaries(ctx context.Context, rows []delivery.SummaryRecord) error
	SaveAliases(ctx context.Context, aliases []events.AliasRecord) error
	// LinkAliases links the pair of each alias in its environment, once per
	// pair, keeping the alias that occurred earliest.
	LinkAliases(ctx context.Context, aliases []events.AliasRecord) error
	SaveExposures(ctx context.Context, exposures []events.ExposureRecord) error
	// ClaimRefusals records the IDs of exposures of project refused, as
	// received at at, and returns those not recorded before: the refusals
	// to count, once each however often the exposure is sent.
	ClaimRefusals(ctx context.Context, project workspace.ProjectID, ids []delivery.EventID, at time.Time) (map[delivery.EventID]bool, error)
	// CountRejections adds counts to those of exposures refused in
	// environment, as of at.
	CountRejections(ctx context.Context, environment workspace.EnvironmentID, counts []events.RejectionCount, at time.Time) error
	// EnsureEventPartitions makes sure the raw event tables can take rows
	// received at at, outside any unit of work ctx carries.
	EnsureEventPartitions(ctx context.Context, at time.Time) error
}

// Limits bound what ingest keeps for good. A limit of 0 is the default.
type Limits struct {
	// AliasLinks is the most alias links an environment keeps.
	AliasLinks int
	// FirstExposures is the most units whose first exposure an experiment
	// keeps in an environment.
	FirstExposures int
}

// The limits ingest applies when the configuration gives none.
const (
	DefaultAliasLinksLimit     = 5_000_000
	DefaultFirstExposuresLimit = 10_000_000
)

// IngestEvents stores the events an SDK posts in a batch.
type IngestEvents struct {
	tx        Transactor
	store     EventStore
	clock     clock.Clock
	retention time.Duration
	limits    Limits
}

// NewIngestEvents returns the handler, accepting events within retention of
// the clock's time.
func NewIngestEvents(tx Transactor, store EventStore, c clock.Clock, retention time.Duration, limits Limits) *IngestEvents {
	if limits.AliasLinks <= 0 {
		limits.AliasLinks = DefaultAliasLinksLimit
	}
	if limits.FirstExposures <= 0 {
		limits.FirstExposures = DefaultFirstExposuresLimit
	}
	return &IngestEvents{tx: tx, store: store, clock: c, retention: retention, limits: limits}
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
	// not have, or a variation the flag lacks.
	DroppedRows int
	// Rejected are the events refused, by index.
	Rejected []EventRejection
}

// DroppedRowsTold is the count of dropped rows a key of kind k is told: the
// true count for a server key, 0 for a client key. Told to a client key, the
// count would say whether a flag key it guessed belongs to the project, an
// archived flag included. DroppedRows stays the true count for the metrics.
func (r BatchResult) DroppedRowsTold(k delivery.SDKKeyKind) int {
	if k == delivery.SDKKeyClient {
		return 0
	}
	return r.DroppedRows
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
// batch or for as long as event IDs are kept (see claimRetention), is a
// duplicate and stored once. A summary row naming a flag the project does
// not have is dropped: nobody could read it, and a client key is told none
// were. An exposure is checked against the experiment it names; a server
// key is told the check it failed, and a client key unknown_experiment for
// every refusal, so that it learns nothing of the project's experiments.
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

// rejectUndeclaredUnitType refuses an alias naming a unit type the project
// does not declare. A client key ships inside apps and browsers, where
// anyone can read it, and a code of its own would tell it which unit types
// the project declares: it is answered the generic event_invalid, naming
// nothing of the project.
func rejectUndeclaredUnitType(e events.JudgedEvent, field, unitType string, detailed bool) events.JudgedEvent {
	if !detailed {
		return e.Reject(events.RejectEventInvalid, "the alias is not valid")
	}
	return e.Reject(events.RejectUndeclaredUnitType, fmt.Sprintf("%s %q is not a unit type of this project", field, unitType))
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
	variations, err := h.store.FlagVariations(ctx, key.Project, unique(flags))
	if err != nil {
		return BatchResult{}, err
	}
	named, err := h.store.Experiments(ctx, key.Project, unique(experimentKeys))
	if err != nil {
		return BatchResult{}, err
	}
	full, err := h.atLimit(ctx, key, judged, named)
	if err != nil {
		return BatchResult{}, err
	}
	// An event is a duplicate within the batch only after an earlier copy
	// that was not refused: a copy of a refused event is refused alike
	// rather than counted as received.
	ids := map[events.EventType][]delivery.EventID{}
	duplicate := make([]bool, len(judged))
	seen := map[typedID]bool{}
	var refusals []refusal
	detailed := key.Kind == delivery.SDKKeyServer
	for i, e := range judged {
		if e.Rejection != nil {
			continue
		}
		if a := e.Alias; a != nil {
			switch {
			case !declared[a.FromUnitType]:
				judged[i] = rejectUndeclaredUnitType(e, "from_unit_type", a.FromUnitType, detailed)
				continue
			case !declared[a.ToUnitType]:
				judged[i] = rejectUndeclaredUnitType(e, "to_unit_type", a.ToUnitType, detailed)
				continue
			case !full.admitLink(a.Pair()):
				judged[i] = e.Reject(events.RejectQuotaExceeded, "this environment holds as many alias links as it may keep")
				continue
			}
		}
		if x := e.Exposure; x != nil {
			facts, exists := named[x.ExperimentKey]
			var experiment *events.ExperimentFacts
			if exists {
				experiment = &facts
			}
			if rj := events.CheckExposure(*x, experiment); rj != nil {
				told := *rj
				if !detailed {
					told = rj.ForClient(x.ExperimentKey)
				}
				judged[i] = e.Reject(told.Code, told.Detail)
				// Only a refusal for a started experiment is counted: a key
				// nobody declared would let whoever holds a client key grow
				// the table with keys of their own making, and a draft is
				// refused as if it did not exist, with no unit assigned
				// whose refusals could be told apart.
				if exists && facts.Status != experiments.StatusDraft {
					refusals = append(refusals, refusal{rejectionKey{facts.ID, rj.Code}, e.ID})
				}
				continue
			}
			// An override exposure never makes a first exposure. A refusal
			// for the limit is not counted with the checks' own: it says
			// nothing of the event, and the count's reasons are the checks'.
			unit := events.UnitKey{Environment: key.Environment, Experiment: facts.ID, UnitType: x.UnitType, UnitID: x.UnitID}
			if !x.Override && full.experiments[facts.ID] && !full.exposed[unit] {
				judged[i] = e.Reject(events.RejectQuotaExceeded, "this experiment holds as many first exposures as it may keep")
				continue
			}
		}
		// An ID is claimed per type, so two events of different types
		// sharing one are two events, here as across batches.
		if seen[typedID{e.Type, e.ID}] {
			duplicate[i] = true
			continue
		}
		seen[typedID{e.Type, e.ID}] = true
		ids[e.Type] = append(ids[e.Type], e.ID)
	}
	// Claimed in a fixed order of types and IDs, so two replicas storing
	// overlapping batches at once wait on each other instead of deadlocking.
	fresh := map[typedID]bool{}
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
			fresh[typedID{typ, id}] = true
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
		case duplicate[i] || !fresh[typedID{e.Type, e.ID}]:
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
				// A row of a flag the project lacks, or of a variation
				// the flag lacks, is no evaluation the project can have
				// had: it is dropped rather than kept under a name of
				// the sender's making.
				if of, known := variations[r.Flag]; !known || !events.NamesVariationOf(r, of) {
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
		// The link outlives the raw alias, which retention drops: it keeps
		// the pair linked once and counts against the environment's limit.
		if err := h.store.LinkAliases(ctx, aliases); err != nil {
			return BatchResult{}, err
		}
	}
	if len(exposures) > 0 {
		if err := h.store.SaveExposures(ctx, exposures); err != nil {
			return BatchResult{}, err
		}
	}
	refused, err := h.firstRefusals(ctx, key, refusals, now)
	if err != nil {
		return BatchResult{}, err
	}
	if err := h.countRejections(ctx, key, refused, now); err != nil {
		return BatchResult{}, err
	}
	return res, nil
}

// limitsReached is what a batch is judged against: where an environment or
// an experiment already keeps as much as the limit allows, and of that what
// the batch's events would only update.
type limitsReached struct {
	// limitLinks is whether the batch's new pairs could take the
	// environment past its limit of alias links; linked holds the pairs of
	// the batch it already has, and linkRoom how many new pairs it may
	// still take.
	limitLinks bool
	linked     map[events.AliasPair]bool
	linkRoom   int64
	// admitted holds the new pairs given room so far, so that every alias
	// of one of them is taken alike.
	admitted map[events.AliasPair]bool
	// experiments are those holding as many first exposures as they may;
	// exposed holds the units of the batch they already have.
	experiments map[experiments.ExperimentID]bool
	exposed     map[events.UnitKey]bool
}

// admitLink reports whether an alias of pair may be taken: its pair is
// already linked, or the limit leaves room for one more, which it takes.
func (l *limitsReached) admitLink(pair events.AliasPair) bool {
	if !l.limitLinks || l.linked[pair] || l.admitted[pair] {
		return true
	}
	if l.linkRoom == 0 {
		return false
	}
	l.linkRoom--
	l.admitted[pair] = true
	return true
}

// atLimit reads what the batch's aliases and exposures add to, which is
// nothing a limit stops while the environment or experiment is below it.
func (h *IngestEvents) atLimit(ctx context.Context, key delivery.SDKKey, judged []events.JudgedEvent,
	named map[string]events.ExperimentFacts,
) (limitsReached, error) {
	var (
		res   limitsReached
		pairs []events.AliasPair
		exps  []experiments.ExperimentID
		seen  = map[experiments.ExperimentID]bool{}
	)
	for _, e := range judged {
		if e.Rejection != nil {
			continue
		}
		if a := e.Alias; a != nil {
			pairs = append(pairs, a.Pair())
		}
		if x := e.Exposure; x != nil && !x.Override {
			if facts, ok := named[x.ExperimentKey]; ok && !seen[facts.ID] {
				seen[facts.ID] = true
				exps = append(exps, facts.ID)
			}
		}
	}
	if len(pairs) > 0 {
		links, err := h.store.AliasLinkCount(ctx, key.Environment)
		if err != nil {
			return res, err
		}
		// Only a batch whose pairs could all be new and still overrun the
		// limit reads which of them are linked: below that, every one fits.
		if res.limitLinks = links+int64(len(pairs)) > int64(h.limits.AliasLinks); res.limitLinks {
			if res.linked, err = h.store.LinkedPairs(ctx, key.Environment, pairs); err != nil {
				return res, err
			}
			res.linkRoom = max(0, int64(h.limits.AliasLinks)-links)
			res.admitted = map[events.AliasPair]bool{}
		}
	}
	if len(exps) > 0 {
		counts, err := h.store.UnitsFirstExposed(ctx, key.Environment, exps)
		if err != nil {
			return res, err
		}
		res.experiments = map[experiments.ExperimentID]bool{}
		for id, n := range counts {
			res.experiments[id] = n >= int64(h.limits.FirstExposures)
		}
		if len(res.experiments) > 0 {
			var units []events.UnitKey
			for _, e := range judged {
				if x := e.Exposure; e.Rejection == nil && x != nil && !x.Override {
					if facts, ok := named[x.ExperimentKey]; ok && res.experiments[facts.ID] {
						units = append(units, events.UnitKey{Environment: key.Environment, Experiment: facts.ID, UnitType: x.UnitType, UnitID: x.UnitID})
					}
				}
			}
			if len(units) > 0 {
				if res.exposed, err = h.store.ExposedUnits(ctx, units); err != nil {
					return res, err
				}
			}
		}
	}
	return res, nil
}

// typedID is an event ID of one type: IDs are claimed, and so duplicates
// told, per type.
type typedID struct {
	typ events.EventType
	id  delivery.EventID
}

// refusal is one exposure refused for an experiment that exists.
type refusal struct {
	key rejectionKey
	id  delivery.EventID
}

// firstRefusals returns the refusals of a batch by experiment and reason,
// leaving out those of exposures refused before, in this batch or an earlier
// one, so a batch sent again adds nothing to the counts.
func (h *IngestEvents) firstRefusals(ctx context.Context, key delivery.SDKKey, refusals []refusal, at time.Time,
) (map[rejectionKey]int64, error) {
	if len(refusals) == 0 {
		return nil, nil
	}
	ids := make([]delivery.EventID, 0, len(refusals))
	for _, r := range refusals {
		ids = append(ids, r.id)
	}
	// Claimed in a fixed order, like every claim, so replicas refusing
	// overlapping batches wait on each other instead of deadlocking.
	slices.SortFunc(ids, func(a, b delivery.EventID) int {
		x, y := a.Bytes(), b.Bytes()
		return bytes.Compare(x[:], y[:])
	})
	ids = slices.Compact(ids)
	fresh, err := h.store.ClaimRefusals(ctx, key.Project, ids, at)
	if err != nil {
		return nil, err
	}
	counted := map[delivery.EventID]bool{}
	refused := map[rejectionKey]int64{}
	for _, r := range refusals {
		if fresh[r.id] && !counted[r.id] {
			counted[r.id] = true
			refused[r.key]++
		}
	}
	return refused, nil
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
