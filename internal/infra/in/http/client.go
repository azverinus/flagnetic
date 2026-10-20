package http

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
)

// Client is who sent a request, as far as the edge can tell.
type Client struct {
	// IP is the client's address: the TCP peer, or the address a trusted
	// proxy says it forwarded for.
	IP netip.Addr
	// Scheme is http or https, as the client used it.
	Scheme string
}

// ClientOf returns the client of the request ctx belongs to. Rate limits,
// sessions and audit entries read it, so a client cannot choose the address
// they see.
func ClientOf(ctx context.Context) Client {
	if st := stateOf(ctx); st != nil {
		return st.client
	}
	return Client{}
}

// resolveClient returns r's client. The TCP peer is the client unless it is
// a trusted proxy; then X-Forwarded-For names the client and
// X-Forwarded-Proto the scheme. From any other peer both headers are ignored.
func resolveClient(r *http.Request, trusted []netip.Prefix) Client {
	c := Client{Scheme: "http"}
	if r.TLS != nil {
		c.Scheme = "https"
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return c
	}
	c.IP = plain(peer.Addr())
	if !isTrusted(c.IP, trusted) {
		return c
	}
	c.IP = forwardedFor(r.Header.Values("X-Forwarded-For"), c.IP, trusted)
	if proto := forwardedProto(r.Header.Values("X-Forwarded-Proto")); proto != "" {
		c.Scheme = proto
	}
	return c
}

// forwardedFor walks X-Forwarded-For from the right, where each trusted proxy
// appended the address it received the request from, and returns the first
// address outside the trusted list; when every address is trusted, the
// leftmost. An entry may carry a port. An entry that is not an address (some
// proxies write "unknown") is skipped: ending the walk there would collapse
// every client behind that hop onto one address.
func forwardedFor(headers []string, peer netip.Addr, trusted []netip.Prefix) netip.Addr {
	client := peer
	for i := len(headers) - 1; i >= 0; i-- {
		entries := strings.Split(headers[i], ",")
		for j := len(entries) - 1; j >= 0; j-- {
			addr, ok := parseForwarded(strings.TrimSpace(entries[j]))
			if !ok {
				continue
			}
			client = addr
			if !isTrusted(client, trusted) {
				return client
			}
		}
	}
	return client
}

// forwardedProto returns the scheme in the rightmost entry of the last header
// line, the one the nearest proxy set, or "" when it is not http or https. An
// entry further left, or a line before, may come from the client.
func forwardedProto(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	header := lines[len(lines)-1]
	last := header[strings.LastIndex(header, ",")+1:]
	switch proto := strings.ToLower(strings.TrimSpace(last)); proto {
	case "http", "https":
		return proto
	}
	return ""
}

// parseForwarded reads one X-Forwarded-For entry: an address, with or without
// a port.
func parseForwarded(entry string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(entry); err == nil {
		return plain(a), true
	}
	if ap, err := netip.ParseAddrPort(entry); err == nil {
		return plain(ap.Addr()), true
	}
	return netip.Addr{}, false
}

// plain drops an IPv6 zone and unmaps an IPv4-mapped address, so an address
// matches a trusted prefix however it was written and logs the same way.
func plain(a netip.Addr) netip.Addr { return a.WithZone("").Unmap() }

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
