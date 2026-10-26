// Package webhook makes the HTTP calls of webhook deliveries.
package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
)

// errRefusedAddress fails an attempt whose target resolved, at the moment
// of the call, to an address the operator does not allow. It is no answer
// from the receiver, so the delivery is retried: what a name resolves to
// can change back.
var errRefusedAddress = errors.New("the target resolved to a loopback, private, link-local, unspecified or " +
	"multicast address, which FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS does not allow, so the call was not made")

// Sender posts deliveries to receivers.
type Sender struct{ client *http.Client }

var _ appgovernance.Sender = (*Sender)(nil)

// NewSender returns the sender of every delivery attempt, connecting only
// where policy allows. Every attempt is bounded by
// governance.DeliveryTimeout and follows no redirect: a 3xx would point at a
// target nobody reviewed, so it is the answer, and a refusal. Unless policy
// allows private networks, the dialer refuses every address
// governance.TargetAddressRefused refuses as it connects, whatever the name
// resolved to when the subscription was written: a name can be pointed
// anywhere afterwards.
func NewSender(policy governance.TargetPolicy) *Sender {
	dialer := &net.Dialer{Timeout: governance.DeliveryTimeout, KeepAlive: 30 * time.Second}
	if !policy.AllowPrivateNetworks {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errRefusedAddress
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || governance.TargetAddressRefused(addr) {
				return errRefusedAddress
			}
			return nil
		}
	}
	return &Sender{client: &http.Client{
		Timeout:       governance.DeliveryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			// No proxy from the environment: a proxy would connect to the
			// target on the replica's behalf, past the dialer's check.
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: governance.DeliveryTimeout,
			MaxIdleConnsPerHost: governance.MaxDeliveriesInFlight,
			IdleConnTimeout:     90 * time.Second,
		},
	}}
}

// Send posts body with headers to target and returns what came of it.
func (s *Sender) Send(ctx context.Context, target string, headers map[string]string, body []byte) appgovernance.Sent {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return appgovernance.Sent{Err: fmt.Errorf("building the call: %w", err)}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "Flagnetic-Webhook")
	started := time.Now()
	res, err := s.client.Do(req)
	if err != nil {
		return appgovernance.Sent{Latency: time.Since(started), Err: cause(err)}
	}
	defer func() { _ = res.Body.Close() }()
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, governance.ResponseSnippetLimit))
	// The rest is read and dropped, briefly, so the connection can be
	// reused; a receiver streaming more is cut off by the timeout.
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	code := res.StatusCode
	return appgovernance.Sent{StatusCode: &code, Body: snippet, Latency: time.Since(started)}
}

// cause is why a call got no answer, without the method and target the
// client wraps it in, which the delivery log shows beside it anyway; a
// refused address is named plainly, since "operation not permitted" would
// send an operator hunting a firewall.
func cause(err error) error {
	if errors.Is(err, errRefusedAddress) {
		return errRefusedAddress
	}
	var wrapped *url.Error
	if errors.As(err, &wrapped) {
		return wrapped.Err
	}
	return err
}
