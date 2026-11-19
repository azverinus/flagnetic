package identity

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ProvisionedMember is a member an identity provider provisions, as SCIM
// reads them.
type ProvisionedMember struct {
	ID          identity.MemberID
	Email       string
	DisplayName string
	// Active is whether the member can sign in: not deactivated.
	Active bool
	// ExternalID is the provider's identifier of the person, "" for none.
	ExternalID string
	CreatedAt  time.Time
	// Groups are the provider's SCIM groups the member is in, by name.
	Groups []GroupRef
}

// HeldMember is a member the instance holds, as adopting one needs to judge
// them.
type HeldMember struct {
	ID identity.MemberID
}

// Refusals a SCIM write meets, which the SCIM adapter answers 409
// `uniqueness`.
var (
	// ErrEmailTaken refuses an email another member holds, that may not be
	// adopted.
	ErrEmailTaken = apperrors.Conflict("already_exists", "a member with this email exists and cannot be taken over by this provider")
	// ErrExternalIDTaken refuses an external ID another member of the
	// provider holds.
	ErrExternalIDTaken = apperrors.Conflict("external_id_taken", "another member of this provider holds this externalId")
)

// SCIMMembers is what the identity context asks of the access context to
// provision members, acting in the unit of work the context it is given
// carries. A member of another provider is never found.
type SCIMMembers interface {
	// Get returns the member of provider with the identifier;
	// identity.ErrUserNotFound when none is.
	Get(ctx context.Context, provider string, member identity.MemberID) (ProvisionedMember, error)
	// List returns the page of provider's members satisfying the filter,
	// ordered by email, from the 1-based startIndex, count at most, and how
	// many satisfy it in all.
	List(ctx context.Context, provider string, f identity.UserFilter, startIndex, count int) ([]ProvisionedMember, int, error)
	// ByEmail returns the member holding email, whatever their provider;
	// false when none does.
	ByEmail(ctx context.Context, email string) (HeldMember, bool, error)
	// Provision creates an active member of the provider without a
	// password. An email another member holds is ErrEmailTaken, an external
	// ID another member of the provider holds ErrExternalIDTaken.
	Provision(ctx context.Context, cmd ProvisionUser) (ProvisionedMember, error)
	// Adopt makes the member the instance holds one of the provider's;
	// ErrEmailTaken when they may not be adopted.
	Adopt(ctx context.Context, cmd AdoptUser) (ProvisionedMember, error)
	// Update changes the member's email, name and external ID.
	Update(ctx context.Context, cmd UpdateUser) (ProvisionedMember, error)
	// SetActive deactivates or reactivates the member.
	SetActive(ctx context.Context, cmd SetUserActive) (ProvisionedMember, error)
}

// ProviderRef names the provider a SCIM token is bound to.
type ProviderRef struct {
	ID   identity.ProviderID
	Slug string
}

// ProvisionUser creates a member.
type ProvisionUser struct {
	Provider    ProviderRef
	Email       string
	DisplayName string
	ExternalID  string
	Request     governance.Request
}

// AdoptUser makes a member one of a provider's.
type AdoptUser struct {
	Provider    ProviderRef
	Member      identity.MemberID
	Email       string
	DisplayName string
	ExternalID  string
	Request     governance.Request
}

// UpdateUser changes a member; a nil field is left as it is.
type UpdateUser struct {
	Provider        ProviderRef
	Member          identity.MemberID
	Email           *string
	DisplayName     *string
	ExternalID      *string
	ClearExternalID bool
	Request         governance.Request
}

// SetUserActive deactivates or reactivates a member.
type SetUserActive struct {
	Provider ProviderRef
	Member   identity.MemberID
	Active   bool
	Request  governance.Request
}

// SCIMUserDeps are what the SCIM Users use cases run on.
type SCIMUserDeps struct {
	Tx        Transactor
	Providers ProviderStore
	Members   SCIMMembers
	Groups    SCIMGroupStore
	// Mappings and Access run the provider's group mappings for a member
	// deactivated or reactivated, so their mapped access follows their
	// status.
	Mappings MappingStore
	Access   ProviderAccess
	Logger   *slog.Logger
}

// holdProvider holds the caller's provider until the unit of work ctx carries
// ends, before anything else is held: running its group mappings for a member
// whose status changes must not interleave with replacing them or with a
// group change, which hold it first too.
func (d SCIMUserDeps) holdProvider(ctx context.Context, req SCIMRequest) (identity.Provider, error) {
	return lockProvider(ctx, d.Providers, req.Caller.ProviderSlug)
}

// setActive deactivates or reactivates the member and runs the provider's
// group mappings for them: a deactivated member keeps none of the provider's
// grants, and a reactivated one regains what their groups map to. The caller
// holds the provider p.
func (d SCIMUserDeps) setActive(ctx context.Context, p identity.Provider, req SCIMRequest, member identity.MemberID, active bool) (ProvisionedMember, error) {
	out, err := d.Members.SetActive(ctx, SetUserActive{Provider: req.provider(), Member: member, Active: active, Request: req.governance()})
	if err != nil {
		return out, err
	}
	return out, rerunMappings(ctx, d.Mappings, GroupMappingDeps{Access: d.Access, Logger: d.Logger}, p, req, []identity.MemberID{member})
}

// withGroups fills in the groups of the caller's provider each member is in.
func (d SCIMUserDeps) withGroups(ctx context.Context, caller SCIMCaller, members ...*ProvisionedMember) error {
	ids := make([]identity.MemberID, len(members))
	for i, m := range members {
		ids[i] = m.ID
	}
	if len(ids) == 0 {
		return nil
	}
	groups, err := d.Groups.GroupsOfMembers(ctx, caller.Provider, ids)
	for _, m := range members {
		m.Groups = groups[m.ID]
	}
	return err
}

// answer is out with its groups, unless err.
func (d SCIMUserDeps) answer(ctx context.Context, caller SCIMCaller, out ProvisionedMember, err error) (ProvisionedMember, error) {
	if err != nil {
		return out, err
	}
	return out, d.withGroups(ctx, caller, &out)
}

// SCIMRequest is a request of a SCIM token: the caller and the request it
// made.
type SCIMRequest struct {
	Caller    SCIMCaller
	RequestID string
}

func (r SCIMRequest) provider() ProviderRef {
	return ProviderRef{ID: r.Caller.Provider, Slug: r.Caller.ProviderSlug}
}

// governance returns the request as the audit log names it: the token the
// identity provider presented is the actor.
func (r SCIMRequest) governance() governance.Request {
	return governance.Request{Actor: governance.SCIMActor(r.Caller.Token.String(), r.Caller.Hint), ID: r.RequestID}
}

// ListSCIMUsers lists the members a provider provisions.
type ListSCIMUsers struct{ d SCIMUserDeps }

// NewListSCIMUsers returns the handler.
func NewListSCIMUsers(d SCIMUserDeps) *ListSCIMUsers { return &ListSCIMUsers{d: d} }

// Page is a page of provisioned members and how many match in all.
type Page struct {
	Members []ProvisionedMember
	Total   int
}

// Handle returns the page of the caller's provider's members the filter
// selects. The filter is judged before anything is read: invalidFilter.
func (h *ListSCIMUsers) Handle(ctx context.Context, caller SCIMCaller, rawFilter string, startIndex, count int) (Page, error) {
	f, err := identity.ParseUserFilter(rawFilter)
	if err != nil {
		return Page{}, err
	}
	members, total, err := h.d.Members.List(ctx, caller.ProviderSlug, f, max(startIndex, 1), count)
	if err != nil {
		return Page{}, err
	}
	refs := make([]*ProvisionedMember, len(members))
	for i := range members {
		refs[i] = &members[i]
	}
	return Page{Members: members, Total: total}, h.d.withGroups(ctx, caller, refs...)
}

// GetSCIMUser reads one member a provider provisions.
type GetSCIMUser struct{ d SCIMUserDeps }

// NewGetSCIMUser returns the handler.
func NewGetSCIMUser(d SCIMUserDeps) *GetSCIMUser { return &GetSCIMUser{d: d} }

// Handle returns the member; identity.ErrUserNotFound when the caller's
// provider does not provision them.
func (h *GetSCIMUser) Handle(ctx context.Context, caller SCIMCaller, member identity.MemberID) (ProvisionedMember, error) {
	m, err := h.d.Members.Get(ctx, caller.ProviderSlug, member)
	return h.d.answer(ctx, caller, m, err)
}

// CreateSCIMUser provisions or adopts a member.
type CreateSCIMUser struct{ d SCIMUserDeps }

// NewCreateSCIMUser returns the handler.
func NewCreateSCIMUser(d SCIMUserDeps) *CreateSCIMUser { return &CreateSCIMUser{d: d} }

// Handle creates the member, or adopts the one holding the email, when the
// email is in the provider's domains (otherwise invalidValue); the adopted one
// is no instance admin, is active, and nobody provisions them; any other holder of the email is ErrEmailTaken. It records
// member.provisioned or member.adopted, and, for a user stated inactive,
// member.deactivated.
func (h *CreateSCIMUser) Handle(ctx context.Context, req SCIMRequest, attrs identity.UserAttributes) (ProvisionedMember, error) {
	email, ok := attrs.EmailAddress()
	if !ok {
		return ProvisionedMember{}, &identity.ScimError{Type: "invalidValue", Detail: "userName or emails is required"}
	}
	externalID, err := externalIDOf(attrs.ExternalID)
	if err != nil {
		return ProvisionedMember{}, err
	}
	inactive := attrs.Active != nil && !*attrs.Active
	var out ProvisionedMember
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var (
			p     identity.Provider
			txErr error
		)
		if inactive {
			// Deactivating re-runs the provider's mappings, which must not
			// interleave with replacing them: the provider is held before
			// anything else, as for a patch.
			if p, txErr = h.d.holdProvider(ctx, req); txErr != nil {
				return txErr
			}
		}
		if out, txErr = h.provisionOrAdopt(ctx, req, attrs, email, externalID); txErr != nil {
			return txErr
		}
		if inactive {
			out, txErr = h.d.setActive(ctx, p, req, out.ID, false)
		}
		return txErr
	})
	return h.d.answer(ctx, req.Caller, out, err)
}

// errEmailNotVouched refuses an email a SCIM write names that is outside the
// provider's domains. Sign-in links a member to whoever signs in through a
// provider vouching for the member's email, so a directory that could name an
// address outside its domains could own the account of a person who later
// signs in through the provider that does vouch for it, and could hold
// addresses that are not its own against their invitations.
var errEmailNotVouched = &identity.ScimError{Type: "invalidValue", Detail: "the email is not in one of the provider's domains"}

// vouchedEmail is nil when email is in one of the caller's provider's
// domains, and errEmailNotVouched when it is not.
func (d SCIMUserDeps) vouchedEmail(ctx context.Context, caller SCIMCaller, email string) error {
	p, err := providerOf(ctx, d.Providers, caller.ProviderSlug)
	if err != nil {
		return err
	}
	if !p.VouchesForEmail(email) {
		return errEmailNotVouched
	}
	return nil
}

// provisionOrAdopt creates the member holding no email yet, and adopts the one
// that does when the provider may take them over. An email outside the
// provider's domains is named by neither.
func (h *CreateSCIMUser) provisionOrAdopt(ctx context.Context, req SCIMRequest, attrs identity.UserAttributes, email, externalID string) (ProvisionedMember, error) {
	if err := h.d.vouchedEmail(ctx, req.Caller, email); err != nil {
		return ProvisionedMember{}, err
	}
	held, found, err := h.d.Members.ByEmail(ctx, email)
	if err != nil {
		return ProvisionedMember{}, err
	}
	if !found {
		return h.d.Members.Provision(ctx, ProvisionUser{
			Provider: req.provider(), Email: email, DisplayName: attrs.Name(email), ExternalID: externalID, Request: req.governance(),
		})
	}
	return h.d.Members.Adopt(ctx, AdoptUser{
		Provider: req.provider(), Member: held.ID, Email: email, DisplayName: attrs.Name(email), ExternalID: externalID, Request: req.governance(),
	})
}

func externalIDOf(externalID *string) (string, error) {
	if externalID == nil {
		return "", nil
	}
	return *externalID, identity.CheckExternalID(*externalID)
}

// ReplaceSCIMUser replaces a member's mapped attributes.
type ReplaceSCIMUser struct{ d SCIMUserDeps }

// NewReplaceSCIMUser returns the handler.
func NewReplaceSCIMUser(d SCIMUserDeps) *ReplaceSCIMUser { return &ReplaceSCIMUser{d: d} }

// Handle replaces the member's email, name and, when stated, external ID with
// the attributes', and deactivates or reactivates them when active is stated.
// A member the caller's provider does not provision is not found; an email
// that is new to the member and outside the provider's domains is invalidValue.
func (h *ReplaceSCIMUser) Handle(ctx context.Context, req SCIMRequest, member identity.MemberID, attrs identity.UserAttributes) (ProvisionedMember, error) {
	email, ok := attrs.EmailAddress()
	if !ok {
		return ProvisionedMember{}, &identity.ScimError{Type: "invalidValue", Detail: "userName or emails is required"}
	}
	externalID, err := externalIDOf(attrs.ExternalID)
	if err != nil {
		return ProvisionedMember{}, err
	}
	name := attrs.Name(email)
	cmd := UpdateUser{Provider: req.provider(), Member: member, Email: &email, DisplayName: &name, Request: req.governance()}
	if attrs.ExternalID != nil {
		cmd.ExternalID = &externalID
		cmd.ClearExternalID = externalID == ""
	}
	out, err := applyUserChange(ctx, h.d, req, cmd, attrs.Active)
	return h.d.answer(ctx, req.Caller, out, err)
}

// applyUserChange updates the member and sets them active or not as asked,
// in one unit of work.
func applyUserChange(ctx context.Context, d SCIMUserDeps, req SCIMRequest, cmd UpdateUser, active *bool) (ProvisionedMember, error) {
	var out ProvisionedMember
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		var (
			p   identity.Provider
			err error
		)
		if active != nil {
			if p, err = d.holdProvider(ctx, req); err != nil {
				return err
			}
		}
		if err = d.vouchedChange(ctx, req.Caller, cmd); err != nil {
			return err
		}
		if out, err = d.Members.Update(ctx, cmd); err != nil {
			return err
		}
		if active != nil && out.Active != *active {
			out, err = d.setActive(ctx, p, req, cmd.Member, *active)
		}
		return err
	})
	return out, err
}

// vouchedChange refuses a change of the member's email to an address outside
// the provider's domains. An email the member holds already is left alone
// whatever the domains are now, so a directory still renames or deactivates a
// member whose domain the provider no longer lists.
func (d SCIMUserDeps) vouchedChange(ctx context.Context, caller SCIMCaller, cmd UpdateUser) error {
	if cmd.Email == nil {
		return nil
	}
	current, err := d.Members.Get(ctx, caller.ProviderSlug, cmd.Member)
	if err != nil {
		return err
	}
	if strings.EqualFold(current.Email, *cmd.Email) {
		return nil
	}
	return d.vouchedEmail(ctx, caller, *cmd.Email)
}

// PatchSCIMUser applies a PatchOp to a member.
type PatchSCIMUser struct{ d SCIMUserDeps }

// NewPatchSCIMUser returns the handler.
func NewPatchSCIMUser(d SCIMUserDeps) *PatchSCIMUser { return &PatchSCIMUser{d: d} }

// Handle applies the operations, in order, to the member: their email, name,
// external ID and whether they are active. The operations are judged before
// anything is read or written.
func (h *PatchSCIMUser) Handle(ctx context.Context, req SCIMRequest, member identity.MemberID, ops []identity.PatchOperation) (ProvisionedMember, error) {
	patch, err := identity.ApplyUserPatch(ops)
	if err != nil {
		return ProvisionedMember{}, err
	}
	if patch.ExternalID != nil {
		if err = identity.CheckExternalID(*patch.ExternalID); err != nil {
			return ProvisionedMember{}, err
		}
	}
	cmd := UpdateUser{
		Provider: req.provider(), Member: member, Email: patch.Email, DisplayName: patch.DisplayName,
		ExternalID: patch.ExternalID, ClearExternalID: patch.RemoveExternalID, Request: req.governance(),
	}
	var out ProvisionedMember
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		if patch.RemoveDisplayName {
			// A member without a name is named after their email, as a new
			// one is.
			var current ProvisionedMember
			if current, txErr = h.d.Members.Get(ctx, req.Caller.ProviderSlug, member); txErr != nil {
				return txErr
			}
			email := current.Email
			if patch.Email != nil {
				email = *patch.Email
			}
			cmd.DisplayName = &email
		}
		out, txErr = applyUserChange(ctx, h.d, req, cmd, patch.Active)
		return txErr
	})
	return h.d.answer(ctx, req.Caller, out, err)
}

// DeleteSCIMUser deprovisions a member.
type DeleteSCIMUser struct{ d SCIMUserDeps }

// NewDeleteSCIMUser returns the handler.
func NewDeleteSCIMUser(d SCIMUserDeps) *DeleteSCIMUser { return &DeleteSCIMUser{d: d} }

// Handle deactivates the member, who keeps none of the provider's mapped
// grants; none is ever removed. A member the caller's provider does not
// provision is not found.
func (h *DeleteSCIMUser) Handle(ctx context.Context, req SCIMRequest, member identity.MemberID) error {
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		p, err := h.d.holdProvider(ctx, req)
		if err != nil {
			return err
		}
		_, err = h.d.setActive(ctx, p, req, member, false)
		return err
	})
}
