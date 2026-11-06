package sinks

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// SampleCommand asks for sample rows of the event kinds Kinds to be sent to
// the sink Sink of Project.
type SampleCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	// Kinds are the event kinds to send samples of; none for every kind the
	// sink exports.
	Kinds   []sinks.EventKind
	Request governance.Request
}

// Sampled is what sending the sample of one event kind found.
type Sampled struct {
	EventKind sinks.EventKind
	// Failure is why the target did not acknowledge it; nil when it did.
	Failure *sinks.Failure
}

// Sample sends sample rows to a sink's target.
type Sample struct{ d Deps }

// NewSample returns the handler.
func NewSample(d Deps) *Sample { return &Sample{d: d} }

// Handle sends the ten sample rows of each event kind, one call per kind,
// under delivery IDs of their own. It reads and writes no stream, count or
// health, and a sink that is disabled or stopped is sampled all the same. A
// target that fails is a result, not an error. It records sink.sampled.
func (h *Sample) Handle(ctx context.Context, cmd SampleCommand) ([]Sampled, error) {
	s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return nil, err
	}
	kinds, err := sinks.SelectKinds(cmd.Kinds, s.StreamKinds())
	if err != nil {
		return nil, err
	}
	target, ok := h.d.Targets.For(s.Kind)
	if !ok {
		return nil, errNoTarget
	}
	now := h.d.now()
	secrets, err := openSecrets(h.d.Opener, s, now)
	if err != nil {
		return nil, err
	}
	projectKey, err := h.d.Store.ProjectKey(ctx, s.Project)
	if err != nil {
		return nil, err
	}
	call := Call{Sink: s, ProjectKey: projectKey, Secrets: secrets}
	out := make([]Sampled, 0, len(kinds))
	for _, k := range kinds {
		delivery, idErr := uuid.NewRandomFromReader(h.d.Random)
		if idErr != nil {
			return nil, fmt.Errorf("drawing a delivery ID: %w", idErr)
		}
		failure := target.Deliver(ctx, call, Delivery{
			ID: "sample_" + delivery.String(), Kind: k, Rows: sinks.SampleRows(k, projectKey, now), Sample: true, Now: now,
		})
		out = append(out, Sampled{EventKind: k, Failure: failure})
	}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if txErr := h.d.lockProject(ctx, cmd.Project); txErr != nil {
			return txErr
		}
		after := sinks.Sink{ID: s.ID, Project: s.Project, Input: sinks.Input{Name: s.Name, Kind: s.Kind}}
		return h.d.recordDoc(ctx, now, cmd.Request, "sink.sampled", after, sampledDoc(out))
	})
	return out, err
}

// sampledDoc is what the audit log records of a sample: for each event kind
// whether it was acknowledged, and the failure by class, status and code.
func sampledDoc(results []Sampled) map[string]any {
	list := make([]any, len(results))
	kinds := make([]string, len(results))
	for i, r := range results {
		kinds[i] = string(r.EventKind)
		entry := map[string]any{"event_kind": string(r.EventKind), "ok": r.Failure == nil, "failure": nil}
		if r.Failure != nil {
			entry["failure"] = map[string]any{
				"class": string(r.Failure.Class), "status": nilIfZero(r.Failure.Status), "code": nilIfEmpty(r.Failure.Code),
			}
		}
		list[i] = entry
	}
	return map[string]any{"event_kinds": kinds, "results": list}
}
