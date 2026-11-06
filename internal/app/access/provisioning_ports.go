package access

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ProvisionedField is a member attribute a directory looks members up by.
type ProvisionedField string

// The attributes a directory looks provisioned members up by.
const (
	// FieldEmail is the member's email, compared whatever its case.
	FieldEmail ProvisionedField = "email"
	// FieldExternalID is the provider's own identifier, compared exactly.
	FieldExternalID ProvisionedField = "external_id"
	// FieldDisplayName is the member's name, compared whatever its case.
	FieldDisplayName ProvisionedField = "display_name"
	// FieldID is the member's identifier.
	FieldID ProvisionedField = "id"
)

// ProvisionedCondition is one equality a lookup of provisioned members needs.
type ProvisionedCondition struct {
	Field ProvisionedField
	Value string
}

// ErrExternalIDTaken refuses an external ID another member of the provider
// holds.
var ErrExternalIDTaken = apperrors.Conflict("external_id_taken", "another member of the provider holds this external ID")

// ProvisionedMemberStore is what provisioning needs of the stored members.
type ProvisionedMemberStore interface {
	MemberChanger
	DeactivationStore
	// MemberByID returns the member; ErrNotFound when none has the identifier.
	MemberByID(ctx context.Context, member access.MemberID) (access.Member, error)
	// MemberByEmail returns the member whose email is email whatever its
	// case, with their password hash; ErrNotFound when none.
	MemberByEmail(ctx context.Context, email string) (access.Member, string, error)
	// CreateProvisionedMember stores m, a member of a provider, without a
	// password; an email another member holds is refused already_exists, an
	// external ID another member of the provider holds ErrExternalIDTaken.
	CreateProvisionedMember(ctx context.Context, m access.Member, at time.Time) error
	// SaveProvisionedMember stores m's email, display name, status, provider
	// and external ID, refusing as CreateProvisionedMember does.
	SaveProvisionedMember(ctx context.Context, m access.Member, at time.Time) error
	// ProvisionedMembers returns the page of provider's members that satisfy
	// every condition, ordered by email, from the 1-based startIndex, count
	// at most (none when 0), and how many satisfy them in all.
	ProvisionedMembers(ctx context.Context, provider string, conds []ProvisionedCondition, startIndex, count int) ([]access.Member, int, error)
	// ProviderProvisionedMembers reports whether the provider holds a member.
	ProviderProvisionedMembers(ctx context.Context, provider string) (bool, error)
}

// ErrProvisionedMemberNotFound answers a member the provider does not
// provision, whether it exists elsewhere or not.
var ErrProvisionedMemberNotFound = errors.New("access: no such provisioned member")
