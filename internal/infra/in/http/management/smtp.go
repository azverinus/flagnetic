package management

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// GetMailSettings answers the instance's SMTP server, never its password.
func (a *api) GetMailSettings(ctx context.Context, _ GetMailSettingsRequestObject) (GetMailSettingsResponseObject, error) {
	s, err := a.h.GetSMTP.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return GetMailSettings200JSONResponse(smtpSettingsOut(s)), nil
}

// UpdateMailSettings sets the instance's SMTP server.
func (a *api) UpdateMailSettings(ctx context.Context, req UpdateMailSettingsRequestObject) (UpdateMailSettingsResponseObject, error) {
	b := req.Body
	change := access.SMTPChange{
		Host: b.Host, Port: b.Port, Security: access.SMTPSecurity(b.Security), Password: b.Password, From: b.From,
	}
	if b.Username != nil {
		change.Username = *b.Username
	}
	s, err := a.h.ConfigureSMTP.Handle(ctx, change, requestOf(ctx))
	if err != nil {
		return nil, err
	}
	return UpdateMailSettings200JSONResponse(smtpSettingsOut(s)), nil
}

// DeleteMailSettings removes the instance's SMTP server.
func (a *api) DeleteMailSettings(ctx context.Context, _ DeleteMailSettingsRequestObject) (DeleteMailSettingsResponseObject, error) {
	if err := a.h.RemoveSMTP.Handle(ctx, requestOf(ctx)); err != nil {
		return nil, err
	}
	return DeleteMailSettings204Response{}, nil
}

// SendTestMail sends the caller a mail while they wait.
func (a *api) SendTestMail(ctx context.Context, _ SendTestMailRequestObject) (SendTestMailResponseObject, error) {
	if err := a.h.SendTestMail.Handle(ctx, callerOf(ctx), flaghttp.RequestIDOf(ctx)); err != nil {
		return nil, err
	}
	return SendTestMail204Response{}, nil
}

func smtpSettingsOut(s access.SMTPSettings) SmtpSettings {
	return SmtpSettings{
		Host: s.Host, Port: s.Port, Security: SmtpSecurity(s.Security), Username: s.Username,
		PasswordSet: s.PasswordSet(), From: s.From, UpdatedAt: s.UpdatedAt,
	}
}
