package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListSdkKeys answers an environment's SDK keys, never their values.
func (a *api) ListSdkKeys(ctx context.Context, req ListSdkKeysRequestObject) (ListSdkKeysResponseObject, error) {
	keys, err := a.h.ListSDKKeys.Handle(ctx, sdkKeyRef(ctx, req.Environment))
	if err != nil {
		return nil, err
	}
	return ListSdkKeys200JSONResponse{Items: each(keys, sdkKeyOf)}, nil
}

// CreateSdkKey creates an SDK key and answers its value, this once.
func (a *api) CreateSdkKey(ctx context.Context, req CreateSdkKeyRequestObject) (CreateSdkKeyResponseObject, error) {
	created, err := a.h.CreateSDKKey.Handle(ctx, appdelivery.CreateSDKKeyCommand{
		Ref: sdkKeyRef(ctx, req.Environment), Kind: delivery.SDKKeyKind(req.Body.Kind), Name: req.Body.Name,
		AllowedOrigins: valueOr(req.Body.AllowedOrigins, nil), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateSdkKey201JSONResponse(createdSDKKeyOf(created)), nil
}

// UpdateSdkKey renames an SDK key and replaces its allowed origins.
func (a *api) UpdateSdkKey(ctx context.Context, req UpdateSdkKeyRequestObject) (UpdateSdkKeyResponseObject, error) {
	key, err := sdkKeyID(req.SdkKey)
	if err != nil {
		return nil, err
	}
	if req.Body.AllowedOrigins == nil {
		// Absent and null decode alike; neither lifts a restriction.
		return nil, errAllowedOriginsRequired
	}
	k, err := a.h.UpdateSDKKey.Handle(ctx, appdelivery.UpdateSDKKeyCommand{
		Ref: sdkKeyRef(ctx, req.Environment), Key: key, Request: requestOf(ctx),
		Change: delivery.SDKKeyChange{Name: req.Body.Name, AllowedOrigins: req.Body.AllowedOrigins},
	})
	if err != nil {
		return nil, err
	}
	return UpdateSdkKey200JSONResponse(sdkKeyOf(k)), nil
}

// RotateSdkKey replaces an SDK key and answers the replacement's value,
// this once.
func (a *api) RotateSdkKey(ctx context.Context, req RotateSdkKeyRequestObject) (RotateSdkKeyResponseObject, error) {
	key, err := sdkKeyID(req.SdkKey)
	if err != nil {
		return nil, err
	}
	var seconds *int64
	if req.Body != nil {
		seconds = req.Body.OverlapSeconds
	}
	overlap, err := delivery.RotationOverlap(seconds)
	if err != nil {
		return nil, err
	}
	created, err := a.h.RotateSDKKey.Handle(ctx, appdelivery.RotateSDKKeyCommand{
		Ref: sdkKeyRef(ctx, req.Environment), Key: key, Overlap: overlap, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RotateSdkKey201JSONResponse(createdSDKKeyOf(created)), nil
}

// RevokeSdkKey revokes an SDK key and answers it.
func (a *api) RevokeSdkKey(ctx context.Context, req RevokeSdkKeyRequestObject) (RevokeSdkKeyResponseObject, error) {
	key, err := sdkKeyID(req.SdkKey)
	if err != nil {
		return nil, err
	}
	k, err := a.h.RevokeSDKKey.Handle(ctx, appdelivery.RevokeSDKKeyCommand{
		Ref: sdkKeyRef(ctx, req.Environment), Key: key, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RevokeSdkKey200JSONResponse(sdkKeyOf(k)), nil
}

var errAllowedOriginsRequired = apperrors.Invalid("invalid_sdk_key", "the SDK key is not valid", apperrors.Violation{
	Pointer: "/allowed_origins", Code: "required",
	Detail: "an update names the allowed origins, so one that forgets them cannot lift a restriction; [] allows any origin",
})

func sdkKeyRef(ctx context.Context, environment string) appdelivery.SDKKeyRef {
	return appdelivery.SDKKeyRef{Project: projectID(ctx), Environment: environment}
}

func sdkKeyID(s string) (delivery.SDKKeyID, error) {
	key, err := id.Parse[delivery.SDKKey](s)
	if err != nil {
		// Not an identifier, so no key has it.
		return delivery.SDKKeyID{}, appdelivery.ErrSDKKeyNotFound
	}
	return key, nil
}

func sdkKeyOf(k appdelivery.SDKKeyState) SdkKey {
	return SdkKey{
		Id: openapi_types.UUID(k.ID.Bytes()), Kind: SdkKeyKind(k.Kind), Name: k.Name, KeyId: k.KeyID,
		Status: SdkKeyStatus(k.Status), AllowedOrigins: valueOr(&k.AllowedOrigins, []string{}), CreatedAt: k.CreatedAt,
		LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt, RevokedAt: k.RevokedAt,
	}
}

func createdSDKKeyOf(c appdelivery.CreatedSDKKey) CreatedSdkKey {
	return CreatedSdkKey{Key: sdkKeyOf(c.Key), Value: c.Value.Reveal()}
}
