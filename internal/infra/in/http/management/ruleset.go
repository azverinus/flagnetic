package management

import (
	"context"
	"net/http"
	"strconv"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
)

// GetEnvironmentRuleset answers an environment's ruleset document as
// server SDKs receive it, with its version as the ETag.
func (a *api) GetEnvironmentRuleset(ctx context.Context, req GetEnvironmentRulesetRequestObject,
) (GetEnvironmentRulesetResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, appdelivery.RulesetWait)
	defer cancel()
	rs, err := a.h.ReadRuleset.Handle(ctx, projectID(ctx), req.Environment)
	if err != nil {
		return nil, err
	}
	return rulesetDocument{version: rs.Version, document: rs.Document}, nil
}

// rulesetDocument answers a materialized ruleset as it is, byte for byte,
// rather than decoded and encoded again: a variation's value is any JSON,
// which a round trip through Go values could change.
type rulesetDocument struct {
	version  int64
	document []byte
}

func (d rulesetDocument) VisitGetEnvironmentRulesetResponse(w http.ResponseWriter) error {
	w.Header().Set("ETag", `"`+strconv.FormatInt(d.version, 10)+`"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.document)
	return err
}
