package access

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// InvitationMailData is what the invitation mail's template reads.
type InvitationMailData struct {
	// URL is the link that opens the invitation, carrying its secret.
	URL string
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
	// InvitedBy is the display name of the member who issued the invitation,
	// "" when none did.
	InvitedBy string
	// Project is the name of the project accepting the invitation grants
	// Role in; both are empty when it grants none.
	Project string
	Role    access.RoleName
}

// InvitationDetailsReader reads what an invitation's mail and page name
// besides its member.
type InvitationDetailsReader interface {
	// InvitationDetails returns what the page of the invitation shows
	// besides its member; ErrNotFound when none has the identifier.
	InvitationDetails(ctx context.Context, invitation access.InvitationID) (InvitationDetails, error)
}

// InvitationMailer mails an invitation to the person it invites whenever the
// instance can: it has an SMTP server, and knows the address its console is
// served at, without which the link could only carry whatever address the
// request claimed to come to. A mailed invitation's link is never shown to
// whoever issued it, so accepting proves the person holds the address.
type InvitationMailer struct {
	postman    *Postman
	smtp       SMTPReader
	details    InvitationDetailsReader
	consoleURL string
}

// NewInvitationMailer returns the mailer; consoleURL is the address the
// console is served at, "" when the instance has none.
func NewInvitationMailer(postman *Postman, smtp SMTPReader, details InvitationDetailsReader, consoleURL string) *InvitationMailer {
	return &InvitationMailer{postman: postman, smtp: smtp, details: details, consoleURL: consoleURL}
}

// invitationLinkPath is where the console opens an invitation, followed by
// its secret.
const invitationLinkPath = "/invitations/"

// mail queues the mail of issued to invitee, which leaves once the unit of
// work ctx carries has committed, and returns issued as its issuer is
// answered: mailed, without its secret, which only the mail carries. The mail
// names the inviter and the project and role the invitation grants, and is
// written in the invitee's language, else the inviter's, else the
// instance's. When the instance cannot mail, issued is returned as it is, for
// its issuer to pass the link on; a nil mailer never mails. Whether the mail
// is sent later never undoes the invitation: a lost one is reissued.
func (im *InvitationMailer) mail(ctx context.Context, invitee access.Member, issued IssuedInvitation, requestID string,
) (IssuedInvitation, error) {
	if im == nil || im.consoleURL == "" {
		return issued, nil
	}
	if _, err := im.smtp.SMTPSettings(ctx); errors.Is(err, ErrSMTPNotConfigured) {
		return issued, nil
	} else if err != nil {
		return IssuedInvitation{}, err
	}
	details, err := im.details.InvitationDetails(ctx, issued.ID)
	if err != nil {
		return IssuedInvitation{}, err
	}
	data := InvitationMailData{
		URL: im.consoleURL + invitationLinkPath + issued.Secret.Reveal(), ExpiresAt: issued.ExpiresAt, InvitedBy: details.InvitedBy,
	}
	if g := issued.Grant; g != nil && details.ProjectName != "" {
		data.Project, data.Role = details.ProjectName, g.Role
	}
	im.postman.Queue(ctx, Mail{
		Kind: access.MailInvitation, To: MailRecipient{Name: invitee.DisplayName, Email: invitee.Email},
		Locale: im.postman.Locale(invitee.Locale, details.InviterLocale), Data: data, RequestID: requestID,
	})
	issued.Mailed, issued.Secret = true, secret.String{}
	return issued, nil
}
