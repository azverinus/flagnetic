package testkit

import (
	"context"
	"net"
	"net/netip"
	"strings"
)

// reservedNamesResolver answers a name under a top-level domain reserved for
// documentation and tests as not found without asking anyone, and any other
// name with the system's resolver. Asking the machine's DNS for
// receiver.example waits on whatever it answers: a network that has no
// answer waits out a timeout, and one that answers every name hands out an
// address that is not the receiver's.
type reservedNamesResolver struct{}

func (reservedNamesResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	name := strings.TrimSuffix(strings.ToLower(host), ".")
	for _, tld := range []string{"example", "invalid", "test"} {
		if name == tld || strings.HasSuffix(name, "."+tld) {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
	}
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}
