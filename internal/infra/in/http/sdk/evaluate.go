package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// maxEvaluateBody bounds an evaluation request after decompression: far
// above any real context within its limits, small enough that a replica
// never reads much for one.
const maxEvaluateBody = 32 << 10

// errNotJSON answers a body sent as another media type than JSON.
var errNotJSON = flaghttp.UnsupportedMediaType("the body must be sent as application/json")

// requireJSON refuses a request whose body is not declared JSON. A
// parameter such as charset is accepted.
func requireJSON(r *http.Request) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errNotJSON
	}
	return nil
}

// evaluationBody is an evaluation request as posted. Units are kept raw so
// that a unit type given twice, which a map would silently collapse, is
// refused rather than evaluated for one of its IDs at random.
type evaluationBody struct {
	Context *struct {
		Units      json.RawMessage `json:"units"`
		Attributes map[string]any  `json:"attributes"`
	} `json:"context"`
	Flags *[]string `json:"flags"`
}

// decodeEvaluation reads an evaluation request from body: one JSON object
// with a context, nothing after it.
func decodeEvaluation(body io.Reader) (appdelivery.RemoteQuery, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return appdelivery.RemoteQuery{}, err
	}
	var b evaluationBody
	dec := json.NewDecoder(bytes.NewReader(data))
	if err = dec.Decode(&b); err != nil {
		return appdelivery.RemoteQuery{}, flaghttp.InvalidRequest("the body is not an evaluation request: " + err.Error())
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return appdelivery.RemoteQuery{}, flaghttp.InvalidRequest("the body holds more than one JSON value")
	}
	if b.Context == nil {
		return appdelivery.RemoteQuery{}, flaghttp.InvalidRequest("the body has no context")
	}
	units, err := decodeUnits(b.Context.Units)
	if err != nil {
		return appdelivery.RemoteQuery{}, flaghttp.InvalidRequest("context.units must be an object of unit IDs: " + err.Error())
	}
	q := appdelivery.RemoteQuery{Context: delivery.RemoteContext{Units: units, Attributes: b.Context.Attributes}}
	if b.Flags != nil {
		q.Flags = *b.Flags
		if q.Flags == nil {
			q.Flags = []string{}
		}
	}
	return q, nil
}

// errUnitsShape refuses units that are not an object of strings.
var errUnitsShape = errors.New("not an object of strings")

// decodeUnits reads the units of a context in the order given, a unit
// type given twice included; null or absent is no unit.
func decodeUnits(raw json.RawMessage) ([]delivery.ContextUnit, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errUnitsShape
	}
	var units []delivery.ContextUnit
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		unitType, _ := t.(string)
		var id string
		if err := dec.Decode(&id); err != nil {
			return nil, errUnitsShape
		}
		units = append(units, delivery.ContextUnit{Type: unitType, ID: id})
	}
	return units, nil
}

// Evaluate answers the served value of every flag, or of the flags asked
// for, for the context posted.
func (a *api) Evaluate(w http.ResponseWriter, r *http.Request, params EvaluateParams) {
	if err := checkSchemaVersion(params.FlagneticSchemaVersion); err != nil {
		a.writeError(w, r, err)
		return
	}
	if err := requireJSON(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	q, err := decodeEvaluation(r.Body)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	q.SDK = delivery.ParseUserAgent(r.UserAgent())
	ctx, cancel := context.WithTimeout(r.Context(), appdelivery.RulesetWait)
	defer cancel()
	ev, err := a.h.Evaluate.Handle(ctx, keyOf(r.Context()), q)
	if err != nil {
		if e, ok := apperrors.From(err); ok && e.Code == "invalid_context" {
			err = e.Under("/context")
		}
		a.writeError(w, r, err)
		return
	}
	appVersion, _ := q.Context.Attributes["app_version"].(string)
	a.h.Inventory.Note(keyOf(r.Context()), q.SDK, appVersion, ev.RulesetVersion)
	out := Evaluation{
		SchemaVersion: 1, RulesetVersion: ev.RulesetVersion,
		Environment:       RulesetEnvironment{Key: ev.Environment, Protected: ev.Protected},
		Flags:             make(map[string]FlagEvaluation, len(ev.Results)),
		IgnoredAttributes: make([]IgnoredAttribute, 0, len(ev.Ignored)),
	}
	for key, res := range ev.Results {
		f := FlagEvaluation{
			Type: res.Type, Reason: string(res.Reason),
			AutomaticExposure: res.AutomaticExposure, OverrideInfluenced: res.OverrideInfluenced,
		}
		if res.Value != nil {
			f.Value = res.Value
		}
		if res.Variation != "" {
			f.Variation = &res.Variation
		}
		if res.RuleID != "" {
			f.RuleId = &res.RuleID
		}
		if res.ErrorCode != "" {
			code := string(res.ErrorCode)
			f.ErrorCode = &code
		}
		if x := res.Experiment; x != nil {
			f.Experiment = &ExperimentDetail{
				Key: x.Key, Variation: x.Variation, UnitType: x.UnitType, UnitId: x.UnitID,
				InclusionSalt: x.InclusionSalt, VariationSalt: x.VariationSalt,
				InclusionBucket: x.InclusionBucket, VariationBucket: x.VariationBucket, Override: x.Override,
			}
		}
		out.Flags[key] = f
	}
	for _, ig := range ev.Ignored {
		out.IgnoredAttributes = append(out.IgnoredAttributes, IgnoredAttribute{Name: ig.Name, Code: string(ig.Code)})
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}
