package identity

import (
	"net/url"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ID identifies an identity.
type ID = id.Of[Identity]

// Identity is the subject an auth provider knows a member by. A subject
// belongs to one member at its provider, for good: the link is what lets the
// member sign in without the provider vouching for their email again.
type Identity struct {
	ID       ID
	Provider ProviderID
	// Subject is the provider's stable identifier of the person.
	Subject      string
	Member       MemberID
	LinkedAt     time.Time
	LastSignInAt *time.Time
}

// RedirectURI is where the identity provider sends a browser back to after
// a sign-in through the provider with slug, built from the instance's public
// URL.
func RedirectURI(baseURL *url.URL, slug string) string {
	return strings.TrimSuffix(baseURL.String(), "/") + "/sso/" + slug + "/callback"
}
