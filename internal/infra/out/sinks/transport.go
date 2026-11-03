// Package sinks makes the HTTP calls of sinks: the clients each kind of target
// is called with, guarded by kind, and the reduction of whatever a target
// answers to a failure class.
package sinks

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

// ErrRefusedTarget fails a call whose target resolved, at the moment of the
// call, to an address a sink of its kind may not reach. It is no answer from
// the target, so the call is retried: what a name resolves to can change back.
var ErrRefusedTarget = errors.New("the target resolved to an address a sink of its kind may not call, so the call was not made")

// Resolver resolves a target's host name to its addresses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options are what a client of a kind of target depends on.
type Options struct {
	// Policy is the operator's webhook policy, which lifts the guard of
	// webhook sinks only; the other kinds ignore it.
	Policy governance.TargetPolicy
	// Resolver resolves the target's name on every dial; the system's when nil.
	Resolver Resolver
	// TLSSkipVerify accepts any certificate the target presents; a ClickHouse
	// sink may ask for it, no other kind may.
	TLSSkipVerify bool
}

// NewClient returns the client every call to a target of kind k is made with.
// It dials only addresses k.AddressRefused allows, judged after the name is
// resolved on every dial, whatever it resolved to when the sink was written:
// a name can be pointed anywhere afterwards. It uses no proxy, which would
// connect to the target on the replica's behalf past that check, and follows no
// redirect: a 3xx would point at a target nobody reviewed, so it is the answer
// (class redirect). Every call is bounded by k.CallTimeout.
func NewClient(k sinks.Kind, o Options) *http.Client {
	timeout := k.CallTimeout()
	resolver := o.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	var tlsConfig *tls.Config
	if k == sinks.KindClickHouse && o.TLSSkipVerify {
		tlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the sink's own, explicit choice, for a ClickHouse with a private certificate authority
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialGuarded(ctx, dialer, resolver, k, o.Policy, network, address)
			},
			TLSClientConfig:     tlsConfig,
			TLSHandshakeTimeout: timeout,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// dialGuarded resolves address's host and connects to the first of its
// addresses that k may call, by the address and not by the name, so that the
// name cannot be pointed elsewhere between the check and the connection.
func dialGuarded(ctx context.Context, dialer *net.Dialer, resolver Resolver, k sinks.Kind, policy governance.TargetPolicy,
	network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := lookup(ctx, resolver, host)
	if err != nil {
		return nil, err
	}
	var last error
	for _, a := range addrs {
		if k.AddressRefused(a, policy) {
			last = ErrRefusedTarget
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last == nil {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return nil, last
}

// lookup returns host's addresses: host itself when it is one.
func lookup(ctx context.Context, resolver Resolver, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	return resolver.LookupNetIP(ctx, "ip", host)
}
