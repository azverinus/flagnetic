package sdk

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

const (
	// streamRetry is the reconnection delay a stream tells its client
	// first, in milliseconds: short enough that an SDK whose stream ended
	// hears the next version within seconds.
	streamRetry = 3000
	// heartbeatInterval spaces the heartbeats, by which an SDK tells a quiet
	// stream from a dead one: an SDK hearing nothing for three of them
	// reconnects.
	heartbeatInterval = 15 * time.Second
	// streamLifetime and streamJitter bound how long one stream stays open,
	// so connections spread over the replicas as they come and go, and do
	// not all reconnect at the same moment.
	streamLifetime = 60 * time.Minute
	streamJitter   = 5 * time.Minute
)

// StreamRuleset holds the ruleset stream of the key's environment: each
// version this replica loads above the last one sent, a heartbeat every
// heartbeatInterval re-checking the key and catching up a version a failed
// reload left unsent, until the stream's lifetime ends, the replica drains
// or the client goes away.
func (a *api) StreamRuleset(w http.ResponseWriter, r *http.Request, params StreamRulesetParams) {
	p := presentedOf(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), appdelivery.RulesetWait)
	sub, current, err := a.h.WatchRuleset.Open(ctx, p.key)
	cancel()
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	defer sub.Close()
	a.h.Inventory.Note(p.key, delivery.ParseUserAgent(r.UserAgent()), "", current)

	// Armed before the first frame, so a client that has read it knows
	// the stream's time runs.
	heartbeat := clock.After(a.clock, heartbeatInterval)
	closing := clock.After(a.clock, streamLifetime+rand.N(streamJitter+1)) //nolint:gosec // spreads reconnections; nothing to guess

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	out := &eventWriter{w: w, rc: http.NewResponseController(w)}
	out.printf("retry: %d\n\n", streamRetry)

	sent := lastEventID(params.LastEventID)
	send := func(version int64) {
		if version > sent {
			out.printf("event: ruleset\nid: %d\ndata: {\"version\":%d}\n\n", version, version)
			sent = version
		}
	}
	send(current)
	out.flush()
	for out.err == nil {
		select {
		case <-r.Context().Done():
			return
		case <-a.edge.Draining():
			return
		case <-closing:
			return
		case <-sub.Changed():
			send(sub.Version())
		case <-heartbeat:
			heartbeat = clock.After(a.clock, heartbeatInterval)
			// Bounded, so a lookup that hangs delays the heartbeat by
			// seconds, never past the silence after which the SDK
			// reconnects.
			ctx, cancel := context.WithTimeout(r.Context(), appdelivery.RulesetWait)
			_, err := a.h.Authenticate.Handle(ctx, p.value)
			cancel()
			if refusesKey(err) {
				return
			}
			ctx, cancel = context.WithTimeout(r.Context(), appdelivery.RulesetWait)
			version, err := a.h.WatchRuleset.CatchUp(ctx, p.key, sent)
			cancel()
			if err == nil {
				send(version)
			}
			out.printf(": hb\n\n")
		}
		out.flush()
	}
}

// refusesKey reports whether err refuses the key itself: revoked, expired
// or gone. A key that cannot be checked for now keeps its stream.
func refusesKey(err error) bool {
	e, ok := apperrors.From(err)
	return ok && e.Kind == apperrors.KindUnauthenticated
}

// lastEventID reads Last-Event-ID as the last version the client heard;
// one that is missing or unreadable counts as none.
func lastEventID(header *string) int64 {
	if header == nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(*header), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// eventWriter writes a stream's frames, keeping the first failure, after
// which the client is gone and the stream ends.
type eventWriter struct {
	w   io.Writer
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
