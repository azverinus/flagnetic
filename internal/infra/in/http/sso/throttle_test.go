package sso

import (
	"net/netip"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
)

func TestTheStartThrottleForgetsEndedWindowsOfAddressesNoLongerSeen(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	th := newStartThrottle(c)
	for i := range sweepAt {
		if err := th.allow(netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)})); err != nil {
			t.Fatalf("a first start from address %d: %v", i, err)
		}
	}
	if len(th.windows) != sweepAt {
		t.Fatalf("%d counted addresses, want %d", len(th.windows), sweepAt)
	}

	c.Advance(2 * startWindowLength)
	if err := th.allow(netip.MustParseAddr("192.0.2.1")); err != nil {
		t.Fatal(err)
	}

	if len(th.windows) != 1 {
		t.Errorf("%d counted addresses after the windows ended, want only the one seen since", len(th.windows))
	}
}

func TestTheStartThrottleReadsTheSystemClockWithoutOne(t *testing.T) {
	if err := newStartThrottle(nil).allow(netip.MustParseAddr("192.0.2.1")); err != nil {
		t.Fatal(err)
	}
}
