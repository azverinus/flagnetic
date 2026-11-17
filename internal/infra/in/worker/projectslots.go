package worker

import (
	"context"
	"sync"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// attemptsPerProject is how many of a replica's slots one project holds at
// once, a quarter of them: a project with many slow receivers or sinks
// waits behind its own attempts instead of behind a replica's whole pool, so
// the deliveries of every other project still find a free slot.
const attemptsPerProject = attemptsAtOnce / 4

// projectSlots shares a replica's attempts between projects: each project
// holds at most per at once, whatever else the replica is doing. An instance's
// own deliveries, of the zero project, share one allowance as well.
type projectSlots struct {
	per  int
	mu   sync.Mutex
	held map[workspace.ProjectID]chan struct{}
}

func newProjectSlots(per int) *projectSlots {
	return &projectSlots{per: per, held: map[workspace.ProjectID]chan struct{}{}}
}

// acquire waits for one of project's slots and returns what gives it back,
// or the context's error. Only the caller waits: another project's
// acquisition never queues behind it.
func (p *projectSlots) acquire(ctx context.Context, project workspace.ProjectID) (func(), error) {
	p.mu.Lock()
	slots, ok := p.held[project]
	if !ok {
		slots = make(chan struct{}, p.per)
		p.held[project] = slots
	}
	p.mu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
