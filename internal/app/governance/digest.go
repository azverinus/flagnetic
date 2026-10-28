package governance

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// StaleFlagReport reads a project's stale flags, which the flags context
// judges from facts several contexts hold.
type StaleFlagReport interface {
	// StaleFlags returns every stale flag of project, by key in byte order.
	// It reads in a view of its own, outside any unit of work ctx carries.
	StaleFlags(ctx context.Context, project workspace.ProjectID) ([]governance.StaleFlagItem, error)
}

// DigestProject is a project a digest round considers.
type DigestProject struct {
	ID  workspace.ProjectID
	Key string
}

// DigestStore holds which digest slots each project has handled.
type DigestStore interface {
	// TryLockDigest takes, until the transaction ends, the lock that lets
	// one replica run a digest round at a time; false when another holds it.
	TryLockDigest(ctx context.Context) (bool, error)
	// PruneDigestSlots forgets the slots that began before cutoff.
	PruneDigestSlots(ctx context.Context, cutoff time.Time) error
	// ProjectsAwaitingDigest returns the projects that have not handled the
	// slot starting at slot, by key.
	ProjectsAwaitingDigest(ctx context.Context, slot time.Time) ([]DigestProject, error)
	// RecordDigestSlot marks the slot starting at slot handled by project;
	// false when it already was.
	RecordDigestSlot(ctx context.Context, project workspace.ProjectID, slot time.Time) (bool, error)
}

// DigestDeps are what the digest use cases use.
type DigestDeps struct {
	Tx     Transactor
	Store  DigestStore
	Report StaleFlagReport
	Outbox Outbox
	Clock  clock.Clock
	IDs    *id.Source
}

// wanted reports whether an enabled subscription of project asks for the
// digest. The outbox is read by the fan-out alone, so an event no
// subscription wants is not written.
func (d DigestDeps) wanted(ctx context.Context, project workspace.ProjectID) (bool, error) {
	types, err := d.Outbox.WantedEventTypes(ctx, project)
	return governance.WantsEventType(types, governance.EventStaleFlags), err
}

// queue writes project's digest of items to the outbox, in the unit of work
// ctx carries.
func (d DigestDeps) queue(ctx context.Context, project workspace.ProjectID, actor governance.Actor, at time.Time,
	items []governance.StaleFlagItem,
) error {
	projectKey, _, err := d.Outbox.ScopeKeys(ctx, project, workspace.EnvironmentID{})
	if err != nil {
		return err
	}
	e, err := governance.StaleFlagsEvent(id.New[governance.Event](d.IDs), project, projectKey, actor, at, items).Queue()
	if err != nil {
		return err
	}
	return d.Outbox.Enqueue(ctx, []governance.QueuedEvent{e})
}

// SendStaleFlagDigestCommand asks for the digest of Project to be sent now.
type SendStaleFlagDigestCommand struct {
	Project workspace.ProjectID
	Request governance.Request
}

// SendStaleFlagDigest sends a project's digest on demand.
type SendStaleFlagDigest struct{ d DigestDeps }

// NewSendStaleFlagDigest returns the handler.
func NewSendStaleFlagDigest(d DigestDeps) *SendStaleFlagDigest { return &SendStaleFlagDigest{d: d} }

// Handle queues the project's `report.stale_flags` event for the caller,
// whatever it holds: a maintainer testing a receiver wants the digest of a
// project with nothing stale too. It records no slot, so it neither
// replaces nor suppresses the weekly one. It returns the stale flags the
// event lists.
func (h *SendStaleFlagDigest) Handle(ctx context.Context, cmd SendStaleFlagDigestCommand) ([]governance.StaleFlagItem, error) {
	items, err := h.d.Report.StaleFlags(ctx, cmd.Project)
	if err != nil {
		return nil, err
	}
	now := h.d.Clock.Now().UTC().Truncate(time.Microsecond)
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		wanted, werr := h.d.wanted(ctx, cmd.Project)
		if werr != nil || !wanted {
			return werr
		}
		return h.d.queue(ctx, cmd.Project, cmd.Request.Actor, now, items)
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// errDigestElsewhere stops a round when another replica holds the lock: it
// is doing the same work.
var errDigestElsewhere = errors.New("governance: another replica is running the digest round")

// RunDigestRound sends the weekly digests.
type RunDigestRound struct {
	d    DigestDeps
	slot governance.DigestSlot
}

// NewRunDigestRound returns the use case for the digest due at slot.
func NewRunDigestRound(d DigestDeps, slot governance.DigestSlot) *RunDigestRound {
	return &RunDigestRound{d: d, slot: slot}
}

// Handle queues, within the hour of the latest slot, the digest of every
// project that has not handled it, and records the slot for each, a project
// with nothing stale included so that its slot is not reconsidered at the
// next round. Outside the hour it does nothing: a slot missed while no
// replica ran is skipped, not sent late. The whole round is one transaction
// under a try lock and each slot is recorded in the transaction that queues
// its event, so two replicas never send one slot twice, and a round that
// fails is rolled back whole and tried again within the hour. A project
// that no subscription asks the digest of is recorded without its report
// being read.
func (h *RunDigestRound) Handle(ctx context.Context) error {
	now := h.d.Clock.Now().UTC().Truncate(time.Microsecond)
	slot, due := h.slot.Current(now)
	if !due {
		return nil
	}
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		held, err := h.d.Store.TryLockDigest(ctx)
		if err != nil {
			return err
		}
		if !held {
			return errDigestElsewhere
		}
		if err = h.d.Store.PruneDigestSlots(ctx, slot.Add(-governance.DigestSlotsKept)); err != nil {
			return err
		}
		projects, err := h.d.Store.ProjectsAwaitingDigest(ctx, slot)
		if err != nil {
			return err
		}
		for _, p := range projects {
			if err = h.send(ctx, p, slot, now); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errDigestElsewhere) {
		return nil
	}
	return err
}

// send handles slot for p.
func (h *RunDigestRound) send(ctx context.Context, p DigestProject, slot, now time.Time) error {
	wanted, err := h.d.wanted(ctx, p.ID)
	if err != nil {
		return err
	}
	var items []governance.StaleFlagItem
	if wanted {
		if items, err = h.d.Report.StaleFlags(ctx, p.ID); err != nil {
			return err
		}
	}
	first, err := h.d.Store.RecordDigestSlot(ctx, p.ID, slot)
	if err != nil || !first || len(items) == 0 {
		return err
	}
	return h.d.queue(ctx, p.ID, governance.DigestActor, now, items)
}
