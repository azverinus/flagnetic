package management

import (
	"context"
	"math"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// GetAuthSettings answers the instance's auth settings.
func (a *api) GetAuthSettings(ctx context.Context, _ GetAuthSettingsRequestObject) (GetAuthSettingsResponseObject, error) {
	s, err := a.h.GetAuthSettings.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return GetAuthSettings200JSONResponse(authSettingsOf(s)), nil
}

// UpdateAuthSettings replaces the instance's auth settings.
func (a *api) UpdateAuthSettings(ctx context.Context, req UpdateAuthSettingsRequestObject) (UpdateAuthSettingsResponseObject, error) {
	b := req.Body
	s, err := a.h.UpdateAuthSettings.Handle(ctx, access.AuthSettings{
		SessionIdleTimeout:     seconds(b.SessionIdleTimeoutSeconds),
		SessionAbsoluteTimeout: seconds(b.SessionAbsoluteTimeoutSeconds),
		TokenMaxLifetime:       seconds(b.TokenMaxLifetimeSeconds),
		TokenExpiryRequired:    b.TokenExpiryRequired,
		PasswordMinLength:      b.PasswordMinLength,
	}, requestOf(ctx))
	if err != nil {
		return nil, err
	}
	return UpdateAuthSettings200JSONResponse(authSettingsOf(s)), nil
}

// seconds returns n seconds as a duration, saturated at the bounds a
// duration holds, so a number too large to be one stays out of range rather
// than wrapping into it.
func seconds(n int64) time.Duration {
	const limit = math.MaxInt64 / int64(time.Second)
	switch {
	case n > limit:
		return math.MaxInt64
	case n < -limit:
		return math.MinInt64
	}
	return time.Duration(n) * time.Second
}

func authSettingsOf(s access.AuthSettings) AuthSettings {
	return AuthSettings{
		SessionIdleTimeoutSeconds:     int64(s.SessionIdleTimeout / time.Second),
		SessionAbsoluteTimeoutSeconds: int64(s.SessionAbsoluteTimeout / time.Second),
		TokenMaxLifetimeSeconds:       int64(s.TokenMaxLifetime / time.Second),
		TokenExpiryRequired:           s.TokenExpiryRequired,
		PasswordMinLength:             s.PasswordMinLength,
	}
}
