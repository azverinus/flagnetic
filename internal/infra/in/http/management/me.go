package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// GetMe answers who the caller is: the member, the credential in use and
// the address the request came from.
func (a *api) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	return GetMe200JSONResponse(meOf(ctx, callerOf(ctx))), nil
}

// meOf describes c, calling from the request ctx belongs to.
func meOf(ctx context.Context, c access.Caller) Me {
	me := Me{
		ClientIp:   flaghttp.ClientOf(ctx).IP.String(),
		Credential: Credential{Method: CredentialMethod(c.Method)},
	}
	if c.Token != nil {
		tokenID, name := openapi_types.UUID(c.Token.ID.Bytes()), c.Token.Name
		me.Credential.TokenId, me.Credential.TokenName = &tokenID, &name
	}
	if m := c.Member; m != nil {
		me.Member = &MeMember{
			Id: openapi_types.UUID(m.ID.Bytes()), Email: m.Email, DisplayName: m.DisplayName,
			Status: MemberStatus(m.Status), InstanceAdmin: m.InstanceAdmin,
		}
		if m.Locale != "" {
			locale := m.Locale
			me.Member.Locale = &locale
		}
	}
	return me
}
