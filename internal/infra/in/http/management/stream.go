package management

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

const (
	// streamRetry is the reconnection delay a change stream tells its
	// client first, in milliseconds.
	streamRetry = 3000
	// heartbeatInterval spaces the heartbeats, by which a client tells a
	// quiet stream from a dead one, and at which the caller's access is
	// resolved again.
	heartbeatInterval = 15 * time.Second
	// streamLifetime and streamJitter bound how long one stream stays open,
	// so connections spread over the replicas as they come and go, and do
	// not all reconnect at the same moment.
	streamLifetime = 60 * time.Minute
	streamJitter   = 5 * time.Minute
	// streamsPerCaller is how many change streams one caller holds open on
	// one replica: a console opens one per project it shows, so more is a
	// client that leaks them.
	streamsPerCaller = 10
	// streamSlotWait is what a caller holding every slot is told to wait: a
	// slot frees when a stream closes, not when a window passes.
	streamSlotWait = time.Second
)

// errTooManyStreams refuses a stream to a caller holding every slot.
var errTooManyStreams = apperrors.RateLimited("too_many_streams",
	fmt.Sprintf("you hold %d change streams open on this replica, the most allowed; close one or retry after Retry-After seconds", streamsPerCaller),
	streamSlotWait)

// streamOperation is the operation of the change stream, against which the
// caller is judged again while the stream is open.
var streamOperation = operations.Table["GET /projects/{project}/stream"].Operation

// changeStreams are the slots each caller holds and the streams' metrics.
type changeStreams struct {
	mu   sync.Mutex
	held map[string]int

	open    prometheus.Gauge
	refused prometheus.Counter
}

func newChangeStreams(reg prometheus.Registerer) *changeStreams {
	s := &changeStreams{
		held: map[string]int{},
		open: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metrics.Namespace, Subsystem: "management", Name: "streams",
			Help: "Management change streams open on this replica.",
		}),
		refused: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "management", Name: "streams_refused_total",
			Help: "Management change streams refused because their caller held the most one replica allows.",
		}),
	}
	reg.MustRegister(s.open, s.refused)
	return s
}

// holder is who a stream's slot is counted against: the member, whatever
// credential they present, or the project token, which acts for none.
func holder(c access.Caller) string {
	if c.Member != nil {
		return "member:" + c.Member.ID.String()
	}
	return "token:" + c.Token.ID.String()
}

// take takes one of the slots of c, or reports false when c holds every
// one.
func (s *changeStreams) take(c access.Caller) (release func(), ok bool) {
	k := holder(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[k] >= streamsPerCaller {
		s.refused.Inc()
		return nil, false
	}
	s.held[k]++
	s.open.Inc()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.held[k]--; s.held[k] <= 0 {
			delete(s.held, k)
		}
		s.open.Dec()
	}, true
}

// StreamProjectChanges answers the project's change stream; the stream is
// written once the strict server visits the answer.
func (a *api) StreamProjectChanges(ctx context.Context, req StreamProjectChangesRequestObject) (StreamProjectChangesResponseObject, error) {
	lastEventID := ""
	if req.Params.LastEventID != nil {
		lastEventID = strings.TrimSpace(*req.Params.LastEventID)
	}
	return &changeStream{ctx: ctx, a: a, caller: callerOf(ctx), project: projectOf(ctx).project, lastEventID: lastEventID}, nil
}

// changeStream writes one change stream, from its caller's slot to its
// close.
type changeStream struct {
	ctx         context.Context
	a           *api
	caller      access.Caller
	project     workspace.Project
	lastEventID string
}

func (c *changeStream) VisitStreamProjectChangesResponse(w http.ResponseWriter) error {
	release, ok := c.a.streams.take(c.caller)
	if !ok {
		return errTooManyStreams
	}
	defer release()
	s, err := c.a.h.StreamChanges.Open(c.ctx, c.project.ID, c.lastEventID)
	if err != nil {
		return err
	}
	defer s.Close()

	// Armed before the first frame, so a client that has read it knows the
	// stream's time runs.
	clk := c.a.opts.clock()
	heartbeat := clock.After(clk, heartbeatInterval)
	closing := clock.After(clk, streamLifetime+rand.N(streamJitter+1)) //nolint:gosec // spreads reconnections; nothing to guess

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	out := &eventWriter{w: w, rc: http.NewResponseController(w)}
	out.printf("retry: %d\n\n", streamRetry)
	out.send(s.Take())
	out.flush()
	for out.err == nil {
		select {
		case <-c.ctx.Done():
			return nil
		case <-c.a.edge.Draining():
			return nil
		case <-closing:
			return nil
		case <-s.Changed():
			if !c.stillReads() {
				return nil
			}
			out.send(s.Take())
		case <-heartbeat:
			heartbeat = clock.After(clk, heartbeatInterval)
			if !c.stillReads() {
				return nil
			}
			out.printf(": hb\n\n")
		}
		out.flush()
	}
	return nil
}

// stillReads judges the caller again as the seam judged them, against the
// project as it is now: a stream outlives the request that resolved its
// caller's access, so a member who lost it, or a project deleted (its key
// perhaps taken by another), ends the stream at its next notice or
// heartbeat. The credential itself is not judged again. Access that cannot
// be resolved for now keeps the stream.
func (c *changeStream) stillReads() bool {
	in, err := c.a.address(c.ctx, c.caller, c.project.Key)
	if err != nil {
		return c.ctx.Err() == nil
	}
	if in.project.ID != c.project.ID {
		return false
	}
	return access.Authorize(c.caller, streamOperation,
		&access.ProjectAccess{Project: in.project.ID, Permissions: in.permissions}) == nil
}

// notice is the data of a change event.
type notice struct {
	Kind        string  `json:"kind"`
	Key         string  `json:"key"`
	Environment *string `json:"environment"`
	Version     *int64  `json:"version"`
	Actor       Actor   `json:"actor"`
}

// eventWriter writes a stream's frames, keeping the first failure, after
// which the client is gone and the stream ends.
type eventWriter struct {
	w   http.ResponseWriter
	rc  *http.ResponseController
	err error
}

func (e *eventWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}

func (e *eventWriter) flush() {
	if e.err == nil {
		e.err = e.rc.Flush()
	}
}

// send writes a refetch when refetch is set, then a change event per
// notice.
func (e *eventWriter) send(notices []governance.ChangeNotice, refetch bool) {
	if refetch {
		e.printf("event: refetch\ndata: {}\n\n")
	}
	for _, n := range notices {
		data := notice{Kind: n.Entity.Kind, Key: n.Entity.Key, Version: n.Version, Actor: actorOut(n.Actor)}
		if n.Environment != "" {
			data.Environment = &n.Environment
		}
		b, err := json.Marshal(data)
		if err != nil {
			e.err = err
			return
		}
		e.printf("event: change\nid: %s\ndata: %s\n\n", n.Entry.String(), b)
	}
}
