package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// servedUnder returns a request the edge serves under ctx, as net/http hands
// it over.
func servedUnder(ctx context.Context) *http.Request {
	return withState(httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil), &requestState{})
}

func TestOnlyAContextTheClientEndedIsACancellationByTheClient(t *testing.T) {
	gone, leave := context.WithCancel(context.Background())
	leave()
	timedOut, stop := context.WithTimeout(context.Background(), time.Nanosecond)
	defer stop()
	<-timedOut.Done()
	stopping, stopReplica := context.WithCancelCause(context.Background())
	stopReplica(errors.New("the replica is stopping"))
	// A handler that cancels, with a plain cancel, a context it derived from
	// the request's while the client is still there.
	abandoned := servedUnder(context.Background())
	abandonedCtx, abandon := context.WithCancel(abandoned.Context())
	abandon()

	// As PostgreSQL's driver reports a query whose context ended.
	interrupted := fmt.Errorf("listing a member's projects: %w", fmt.Errorf("timeout: context already done: %w", context.Canceled))
	for _, c := range []struct {
		name string
		r    *http.Request
		err  error
		want bool
	}{
		{"the client went away", servedUnder(gone), interrupted, true},
		{"the client went away, the error is another", servedUnder(gone), errors.New("connection reset"), false},
		{"a server deadline", servedUnder(timedOut), fmt.Errorf("listing: %w", context.DeadlineExceeded), false},
		{"the replica ended the request", servedUnder(stopping), interrupted, false},
		{"a cancellation of the server's own while the client waits", servedUnder(context.Background()), interrupted, false},
		{"the handler cancelled a context it derived", abandoned.WithContext(abandonedCtx), interrupted, false},
		{"a request the edge did not serve", httptest.NewRequestWithContext(gone, http.MethodGet, "/", nil), interrupted, false},
	} {
		if got := canceledByClient(c.r, c.err); got != c.want {
			t.Errorf("%s: canceledByClient = %v, want %v", c.name, got, c.want)
		}
	}
}
