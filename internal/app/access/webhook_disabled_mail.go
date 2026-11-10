package access

import (
	"context"
	"errors"
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// WebhookDisabledMailData is what the webhook_disabled mail's template
// reads.
type WebhookDisabledMailData struct {
	// Name is the subscription's name.
	Name string
	// Project is the name of the project the subscription belongs to; ""
	// for an instance subscription.
	Project string
	// Reason is why the platform disabled it: the subscription's disabled
	// reason, such as persistent_failure.
	Reason string
	// URL is the subscription's page in the console.
	URL string
}

// WebhookManagerDirectory reads who is told of a disabled subscription.
type WebhookManagerDirectory interface {
	// ProjectMembers returns the members holding grants in project, each
	// with their grants there.
	ProjectMembers(ctx context.Context, project workspace.ProjectID) ([]ProjectMember, error)
	// ActiveInstanceAdmins returns the active members who are instance
	// admins.
	ActiveInstanceAdmins(ctx context.Context) ([]access.Member, error)
	// ProjectKeyAndName returns the key and the name of project;
	// ErrNotFound when it no longer exists.
	ProjectKeyAndName(ctx context.Context, project workspace.ProjectID) (key, name string, err error)
}

// DisabledWebhook is a subscription the platform disabled.
type DisabledWebhook struct {
	// ID is the subscription's identifier, as its address in the console
	// spells it.
	ID   string
	Name string
	// Project is the subscription's project, zero for the instance's.
	Project workspace.ProjectID
	// Reason is the subscription's disabled reason.
	Reason string
}

// WebhookDisabledMailer mails those who manage a webhook subscription when
// the platform disables it: the active members whose grants give them
// webhook.manage in its project, instance admins among them, or the active
// instance admins for an instance subscription. Like every mail it goes out
// once the unit of work that disabled the subscription has committed, and
// nothing that happens to it undoes the disable.
type WebhookDisabledMailer struct {
	postman    *Postman
	smtp       SMTPReader
	directory  WebhookManagerDirectory
	consoleURL string
}

// NewWebhookDisabledMailer returns the mailer; consoleURL is the address the
// console is served at, "" when the instance has none, in which case nothing
// is mailed: a mail linking to nowhere helps nobody.
func NewWebhookDisabledMailer(postman *Postman, smtp SMTPReader, directory WebhookManagerDirectory, consoleURL string,
) *WebhookDisabledMailer {
	return &WebhookDisabledMailer{postman: postman, smtp: smtp, directory: directory, consoleURL: consoleURL}
}

// Notify queues the mail of w to each recipient, in their language else the
// instance's. It queues nothing while the instance has no SMTP server or no
// console address, and a nil mailer never mails.
func (m *WebhookDisabledMailer) Notify(ctx context.Context, w DisabledWebhook) error {
	if m == nil || m.consoleURL == "" {
		return nil
	}
	if _, err := m.smtp.SMTPSettings(ctx); errors.Is(err, ErrSMTPNotConfigured) {
		return nil
	} else if err != nil {
		return err
	}
	data := WebhookDisabledMailData{Name: w.Name, Reason: w.Reason, URL: m.consoleURL + "/admin/webhooks/" + w.ID}
	if !w.Project.IsZero() {
		key, name, err := m.directory.ProjectKeyAndName(ctx, w.Project)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		data.Project, data.URL = name, m.consoleURL+"/projects/"+key+"/webhooks/"+w.ID
	}
	recipients, err := m.recipients(ctx, w.Project)
	if err != nil {
		return err
	}
	for _, r := range recipients {
		m.postman.Queue(ctx, Mail{
			Kind: access.MailWebhookDisabled, To: MailRecipient{Name: r.DisplayName, Email: r.Email},
			Locale: m.postman.Locale(r.Locale), Data: data,
		})
	}
	return nil
}

// recipients returns the active members who manage webhooks in project, the
// instance's when project is zero, each once.
func (m *WebhookDisabledMailer) recipients(ctx context.Context, project workspace.ProjectID) ([]access.Member, error) {
	admins, err := m.directory.ActiveInstanceAdmins(ctx)
	if err != nil || project.IsZero() {
		return admins, err
	}
	members, err := m.directory.ProjectMembers(ctx, project)
	if err != nil {
		return nil, err
	}
	out := admins
	for _, pm := range members {
		if pm.Member.Status != access.MemberActive || pm.Member.InstanceAdmin || !managesWebhooks(pm, project) {
			// An instance admin is among the admins already.
			continue
		}
		out = append(out, pm.Member)
	}
	return out, nil
}

// managesWebhooks reports whether a member's grants in project give them
// webhook.manage.
func managesWebhooks(pm ProjectMember, project workspace.ProjectID) bool {
	roles := make([]access.RoleName, len(pm.Grants))
	for i, g := range pm.Grants {
		roles[i] = g.Role
	}
	member := pm.Member
	return slices.Contains(access.EffectivePermissions(access.Caller{Member: &member}, project, roles), access.PermWebhookManage)
}
