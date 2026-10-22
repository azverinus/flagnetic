package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
)

// ListSdkInventory answers the SDKs that use an environment.
func (a *api) ListSdkInventory(ctx context.Context, req ListSdkInventoryRequestObject) (ListSdkInventoryResponseObject, error) {
	rows, err := a.h.ListSDKInventory.Handle(ctx, projectID(ctx), req.Environment)
	if err != nil {
		return nil, err
	}
	return ListSdkInventory200JSONResponse{Items: each(rows, inventoryOut)}, nil
}

// orNull returns nil for "", so an absent part is answered null.
func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func inventoryOut(r delivery.InventoryRow) SdkInventoryItem {
	out := SdkInventoryItem{
		SdkName: r.SDK.Name, SdkVersion: r.SDK.Version, Platform: orNull(r.SDK.Platform), AppVersion: orNull(r.AppVersion),
		SdkKey:    SdkInventoryKey{Id: openapi_types.UUID(r.Key.Bytes()), Kind: SdkKeyKind(r.KeyKind), Name: r.KeyName, KeyId: r.KeyID},
		FirstSeen: r.FirstSeen, LastSeen: r.LastSeen,
	}
	if r.LastRulesetVersion != 0 {
		out.LastRulesetVersion = &r.LastRulesetVersion
	}
	return out
}
