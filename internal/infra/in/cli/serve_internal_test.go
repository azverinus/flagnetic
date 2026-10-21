package cli

import (
	"context"
	"testing"
)

func TestTheReplicaEndsItsRequestsWithACauseOfItsOwn(t *testing.T) {
	for _, c := range []struct {
		name string
		end  func(kill context.CancelFunc, cancelRequests func())
	}{
		{"the grace period ran out", func(_ context.CancelFunc, cancelRequests func()) { cancelRequests() }},
		{"a second signal killed the replica", func(kill context.CancelFunc, _ func()) { kill() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			kill, killNow := context.WithCancel(context.Background())
			defer killNow()
			base, cancelRequests := requestBase(kill)
			defer cancelRequests()
			// A request's context, as net/http derives it from the base.
			req, leave := context.WithCancel(base)
			defer leave()

			c.end(killNow, cancelRequests)

			<-req.Done()
			if got := context.Cause(req); got != errStopped { //nolint:errorlint // the cause itself, as the edge reads it
				t.Errorf("the request's cause is %v, want %v", got, errStopped)
			}
		})
	}
}
