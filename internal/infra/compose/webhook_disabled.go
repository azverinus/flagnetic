package compose

import (
	"context"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
)

// disabledWebhookMail gives the governance context's delivery worker the
// access context's mail to those who manage a subscription it disabled.
type disabledWebhookMail struct {
	mailer *appaccess.WebhookDisabledMailer
}

var _ appgovernance.DisabledNotifier = disabledWebhookMail{}

func (m disabledWebhookMail) WebhookDisabled(ctx context.Context, w governance.Webhook) error {
	return m.mailer.Notify(ctx, appaccess.DisabledWebhook{
		ID: w.ID.String(), Name: w.Name, Project: w.Project, Reason: string(w.DisabledReason),
	})
}
