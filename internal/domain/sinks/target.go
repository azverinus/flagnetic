package sinks

import (
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
)

// The longest one call to a target may take, from dialling to the end of the
// answer: a webhook receiver is held to what subscriptions are, an insert into
// ClickHouse and an upload of a batch to a bucket are given longer.
const (
	WebhookCallTimeout       = 10 * time.Second
	ClickHouseCallTimeout    = 60 * time.Second
	ObjectStorageCallTimeout = 2 * time.Minute
)

// CallTimeout returns the longest one call to a target of kind k may take.
func (k Kind) CallTimeout() time.Duration {
	switch k {
	case KindClickHouse:
		return ClickHouseCallTimeout
	case KindObjectStorage:
		return ObjectStorageCallTimeout
	default:
		return WebhookCallTimeout
	}
}

// AddressRefused reports whether a call of a sink of kind k may not be made to
// a, whatever name it was reached by. A webhook sink is held to the
// subscriptions' policy: nothing outside the public internet unless the
// operator allows private networks. A ClickHouse or object storage sink may
// reach loopback and private addresses, in a cluster's own network, but never
// a link-local one, where cloud metadata services answer, and no setting
// lifts that; an address nothing is called at, unspecified or multicast, stays
// refused to them too.
func (k Kind) AddressRefused(a netip.Addr, policy governance.TargetPolicy) bool {
	if k == KindWebhook {
		return !policy.AllowPrivateNetworks && governance.TargetAddressRefused(a)
	}
	return governance.AddressUnreachable(a) || governance.AddressLinkLocal(a)
}
