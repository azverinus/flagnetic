package sinks_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestABatchIsSetAsideOnTheRejectionThatReachesTheLimit(t *testing.T) {
	for _, tc := range []struct {
		priorRejections, limit int
		want                   bool
	}{
		{0, 5, false}, {3, 5, false}, {4, 5, true}, {5, 5, true}, {0, 1, true},
	} {
		if got := sinks.SetsAside(tc.priorRejections, tc.limit); got != tc.want {
			t.Errorf("SetsAside(%d, %d) = %v, want %v: the rejection now is the %dth", tc.priorRejections, tc.limit, got, tc.want, tc.priorRejections+1)
		}
	}
}

func TestASinkStopsOnTheDeadLetterThatReachesTheLimitInARow(t *testing.T) {
	for _, tc := range []struct {
		inARow, limit int
		want          bool
	}{
		{1, 3, false}, {2, 3, false}, {3, 3, true}, {4, 3, true}, {1, 1, true},
	} {
		if got := sinks.StopsAfter(tc.inARow, tc.limit); got != tc.want {
			t.Errorf("StopsAfter(%d, %d) = %v, want %v", tc.inARow, tc.limit, got, tc.want)
		}
	}
}

func TestARedeliveryHasADeliveryIDOfItsOwnPerAttempt(t *testing.T) {
	src := id.NewSource(clock.NewFake(time.Unix(1_800_000_000, 0)))
	dl, other := id.New[sinks.DeadLetter](src), id.New[sinks.DeadLetter](src)
	first := sinks.RedeliveryID(dl, 5)
	if len(first) != len("dlv_")+26 || !strings.HasPrefix(first, "dlv_") {
		t.Fatalf("RedeliveryID = %q, want dlv_ and 26 hex digits", first)
	}
	if again := sinks.RedeliveryID(dl, 5); again != first {
		t.Errorf("the same dead letter and attempt gave %q then %q", first, again)
	}
	if sinks.RedeliveryID(dl, 6) == first || sinks.RedeliveryID(other, 5) == first {
		t.Error("another attempt or another dead letter gave the same delivery ID")
	}
}
