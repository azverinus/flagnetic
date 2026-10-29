package subnet_test

import (
	"net/netip"
	"testing"

	"github.com/azverinus/flagnetic/internal/shared/subnet"
)

func TestIPv6AddressesAreGroupedByTheir64AndIPv4ByTheAddress(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.5":               "203.0.113.5/32",
		"::ffff:203.0.113.5":        "203.0.113.5/32",
		"2001:db8:1:2:3:4:5:6":      "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff::1": "2001:db8:1:2::/64",
		"fe80::1%eth0":              "fe80::/64",
	} {
		if got := subnet.Of(netip.MustParseAddr(addr)); got.String() != want {
			t.Errorf("Of(%s) = %s, want %s", addr, got, want)
		}
	}
	if got := subnet.Of(netip.Addr{}); got.String() != "0.0.0.0/32" {
		t.Errorf("Of(no address) = %s, want 0.0.0.0/32", got)
	}
}
