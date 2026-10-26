package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// DueChangeRequest names a change request the scheduler applies.
type DueChangeRequest struct {
	Project       workspace.ProjectID
	ChangeRequest governance.ChangeRequestID
}

// ScheduleStore is the store of change requests as the scheduler reads and
// writes them.
type ScheduleStore interface {
	// DueChangeRequests returns at most limit change requests of any
	// project that are due at now, earliest first, read without a lock.
	DueChangeRequests(ctx context.Context, now time.Time, limit int) ([]DueChangeRequest, error)
	// RecordScheduleFailure stores cr's schedule failure and nothing else.
	RecordScheduleFailure(ctx context.Context, cr governance.ChangeRequest) error
}

// schedulerActor is who the entries of a scheduled application name.
var schedulerActor = governance.Actor{Kind: governance.ActorSystem, ID: "change_request_scheduler", Name: "change request scheduler"}

// dueBatch bounds the change requests one round applies; the rest wait for
// the next round, a tick later.
const dueBatch = 100

// RunScheduler applies the approved change requests whose apply time has
// come.
type RunScheduler struct {
	d           ChangeRequestDeps
	store       ScheduleStore
	maxFailures int
}

// NewRunScheduler returns the handler, stopping a change request's
// scheduled application after maxFailures failures in a row.
func NewRunScheduler(d ChangeRequestDeps, store ScheduleStore, maxFailures int) *RunScheduler {
	return &RunScheduler{d: d, store: store, maxFailures: maxFailures}
}

// Handle applies every change request due now, late ones included, each in
// a transaction of its own as the system. Every replica runs it: each
// application locks its project's row and reads the change request again, so
// a second replica waits behind the first and finds it applied. A failed
// application is rolled back, then recorded on the change request; Handle
// answers the failures joined once the others were applied, so a round with
// a broken scheduled change counts as failed.
func (h *RunScheduler) Handle(ctx context.Context) error {
	due, err := h.store.DueChangeRequests(ctx, h.d.now(), dueBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, cr := range due {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := h.applyOne(ctx, cr); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// applyOne applies due when it is still due under its project's lock, and
// records why when that fails.
func (h *RunScheduler) applyOne(ctx context.Context, due DueChangeRequest) error {
	now := h.d.now()
	req := governance.Request{Actor: schedulerActor}
	by := governance.Taker{System: true}
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		cr, found, err := h.stillDue(ctx, due, now)
		if err != nil || !found {
			return err
		}
		// A base that moved supersedes the change request, which is
		// committed and refused: no failure, since trying again cannot
		// succeed.
		_, _, err = transitionOf(ctx, h.d, cr, precondition.On(cr.Version), req, "change_request.applied",
			func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error) {
				return applyChange(ctx, h.d, cr, by, req, now)
			}, now)
		return err
	})
	if err == nil || ctx.Err() != nil {
		return err
	}
	if recordErr := h.recordFailure(ctx, due, err); recordErr != nil {
		return errors.Join(err, recordErr)
	}
	return fmt.Errorf("applying the scheduled change request %s: %w", due.ChangeRequest, err)
}

// stillDue locks due's project and reads due again, answering it and true
// when it is still due at now: another replica may have applied it, or a
// member cancelled it, since it was read.
func (h *RunScheduler) stillDue(ctx context.Context, due DueChangeRequest, now time.Time,
) (governance.ChangeRequest, bool, error) {
	if err := h.d.Projects.LockProject(ctx, due.Project); errors.Is(err, ErrNotFound) {
		return governance.ChangeRequest{}, false, nil
	} else if err != nil {
		return governance.ChangeRequest{}, false, err
	}
	cr, err := h.d.Store.ChangeRequest(ctx, due.Project, due.ChangeRequest)
	if errors.Is(err, ErrNotFound) {
		return cr, false, nil
	}
	return cr, err == nil && cr.Due(now), err
}

// recordFailure records on due, in a short transaction of its own after the
// application rolled back, that it failed with cause, touching nothing but
// its failure; at the limit it records the one change_request.schedule_failed
// entry, since the scheduler stops trying.
func (h *RunScheduler) recordFailure(ctx context.Context, due DueChangeRequest, cause error) error {
	now := h.d.now()
	code, message := failureOf(cause)
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		cr, found, err := h.stillDue(ctx, due, now)
		if err != nil || !found {
			return err
		}
		failed := cr.FailSchedule(code, message, now, h.maxFailures)
		if err = h.store.RecordScheduleFailure(ctx, failed); err != nil {
			return err
		}
		if !failed.ScheduleFailure.Stopped {
			return nil
		}
		return h.d.record(ctx, now, governance.Request{Actor: schedulerActor}, "change_request.schedule_failed", &cr, failed, nil)
	})
}

// failureOf is the code and message a failed application is recorded with:
// the refusal's, which tell the author what to fix, or internal for an error
// of the server, whose detail stays in its log.
func failureOf(err error) (string, string) {
	if e, ok := apperrors.From(err); ok && e.Kind != apperrors.KindUnavailable {
		return e.Code, e.Detail
	}
	return "internal", "the application failed with an internal error; the server log has its detail"
}
