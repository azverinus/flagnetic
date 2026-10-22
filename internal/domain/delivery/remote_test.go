package delivery_test

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestAClientKeyLearnsThatTargetingDecidedButNotHow(t *testing.T) {
	cases := map[eval.Reason]eval.Reason{
		eval.ReasonRule:               delivery.ReasonTargetingMatch,
		eval.ReasonRollout:            delivery.ReasonTargetingMatch,
		eval.ReasonFallthrough:        delivery.ReasonTargetingMatch,
		eval.ReasonPrerequisiteFailed: delivery.ReasonTargetingMatch,
		eval.ReasonOff:                eval.ReasonOff,
		eval.ReasonOverride:           eval.ReasonOverride,
		eval.ReasonError:              eval.ReasonError,
		eval.ReasonExperiment:         eval.ReasonExperiment,
	}
	bucket := 4200
	for reason, want := range cases {
		r := eval.Result{
			Type: "boolean", Value: json.RawMessage("true"), Variation: "true", Reason: reason,
			RuleID: "0192a7c0-0000-7000-8000-000000000001", Prerequisite: "gate", Bucket: &bucket,
		}
		got := delivery.MaskForClient(r)
		if got.Reason != want {
			t.Errorf("%s: reason = %q, want %q", reason, got.Reason, want)
		}
		if got.RuleID != "" || got.Prerequisite != "" || got.Bucket != nil {
			t.Errorf("%s: masked result still says how it was reached: %+v", reason, got)
		}
	}
}

func TestAMaskedAnswerNeverTellsHowAndNeverChangesWhat(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 9))
	reasons := []eval.Reason{
		eval.ReasonOff, eval.ReasonOverride, eval.ReasonRule, eval.ReasonRollout, eval.ReasonExperiment,
		eval.ReasonFallthrough, eval.ReasonPrerequisiteFailed, eval.ReasonError,
	}
	client := []eval.Reason{eval.ReasonOff, eval.ReasonOverride, eval.ReasonError, eval.ReasonExperiment, delivery.ReasonTargetingMatch}
	for i := range 2000 {
		in := eval.Result{
			Type:               pick(r, "boolean", "string", "number", "json"),
			Value:              json.RawMessage(pick(r, "true", `"a"`, "3", `{"k":[1]}`, "")),
			Variation:          pick(r, "", "a", "b"),
			Reason:             pick(r, reasons...),
			RuleID:             pick(r, "", "0192a7c0-0000-7000-8000-00000000000a"),
			AutomaticExposure:  r.IntN(2) == 0,
			Prerequisite:       pick(r, "", "gate"),
			ErrorCode:          pick(r, eval.ErrorCode(""), eval.ErrorFlagNotFound, eval.ErrorInconsistentRuleset),
			OverrideInfluenced: r.IntN(2) == 0,
		}
		if r.IntN(3) == 0 {
			b := r.IntN(10000)
			in.Bucket = &b
		}
		if r.IntN(3) == 0 {
			in.Experiment = &eval.ExperimentDetail{
				Key: "exp", Variation: in.Variation, UnitType: "user", UnitID: "u-" + string(rune('a'+i%26)),
				InclusionSalt: "is", VariationSalt: "vs", InclusionBucket: r.IntN(10000), VariationBucket: r.IntN(10000),
				Override: in.OverrideInfluenced,
			}
		}
		out := delivery.MaskForClient(in)
		if out.RuleID != "" {
			t.Fatalf("masked %+v carries rule_id %q", in, out.RuleID)
		}
		if !slices.Contains(client, out.Reason) {
			t.Fatalf("masked %+v has reason %q outside the client set", in, out.Reason)
		}
		if string(out.Value) != string(in.Value) || out.Variation != in.Variation || out.Type != in.Type ||
			out.AutomaticExposure != in.AutomaticExposure || out.OverrideInfluenced != in.OverrideInfluenced ||
			out.ErrorCode != in.ErrorCode || !reflect.DeepEqual(out.Experiment, in.Experiment) {
			t.Fatalf("masking changed what was served: %+v became %+v", in, out)
		}
	}
}

func contextViolations(t *testing.T, err error) []apperrors.Violation {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Code != "invalid_context" || e.Kind != apperrors.KindInvalid {
		t.Fatalf("err = %v, want invalid_context", err)
	}
	return e.Violations
}

func TestARemoteContextOverItsLimitsNamesEveryViolation(t *testing.T) {
	attrs := map[string]any{
		"country": strings.Repeat("я", 257),
		"tags":    make([]any, 65),
		"labels":  []any{"ok", strings.Repeat("x", 257)},
	}
	c := delivery.RemoteContext{
		Units: []delivery.ContextUnit{
			{Type: "user", ID: strings.Repeat("a", 257)},
			{Type: "device", ID: "d-1"},
			{Type: "device", ID: "d-2"},
		},
		Attributes: attrs,
	}
	got := contextViolations(t, delivery.CheckRemoteContext(c))
	want := []struct{ pointer, code string }{
		{"/attributes/country", "too_long"},
		{"/attributes/labels/1", "too_long"},
		{"/attributes/tags", "too_many"},
		{"/units/device", "duplicate"},
		{"/units/user", "too_long"},
	}
	if len(got) != len(want) {
		t.Fatalf("violations = %+v, want %v", got, want)
	}
	for i, w := range want {
		if got[i].Pointer != w.pointer || got[i].Code != w.code {
			t.Errorf("violation %d = %s %s, want %s %s", i, got[i].Pointer, got[i].Code, w.pointer, w.code)
		}
	}
	if got[0].Params["max"] != 256 || got[2].Params["max"] != 64 || got[4].Params["max"] != 256 {
		t.Errorf("params = %v %v %v, want the limits broken", got[0].Params, got[2].Params, got[4].Params)
	}
}

func TestARemoteContextAtItsLimitsIsValid(t *testing.T) {
	attrs := map[string]any{}
	for i := range 63 {
		attrs["a"+strings.Repeat("x", i)] = true
	}
	attrs["a"] = strings.Repeat("я", 256)
	list := make([]any, 64)
	for i := range list {
		list[i] = strings.Repeat("я", 256)
	}
	attrs["s"] = list
	c := delivery.RemoteContext{Units: []delivery.ContextUnit{{Type: "user", ID: strings.Repeat("я", 128)}}, Attributes: attrs}
	if err := delivery.CheckRemoteContext(c); err != nil {
		t.Fatalf("a context at its limits: %v", err)
	}
	attrs["one_more"] = 1.0
	got := contextViolations(t, delivery.CheckRemoteContext(c))
	if len(got) != 1 || got[0].Pointer != "/attributes" || got[0].Code != "too_many" || got[0].Params["max"] != 64 {
		t.Errorf("65 attributes: violations = %+v", got)
	}
}

func remoteRuleset(t *testing.T) *eval.Ruleset {
	t.Helper()
	rs, err := eval.Decode([]byte(`{"schema_version":1,"version":7,"scope":"server",
		"environment":{"key":"production","protected":false},
		"unit_types":["user"],
		"attributes":[{"key":"plan","type":"string"},{"key":"seats","type":"number"},{"key":"beta","type":"boolean"},
			{"key":"app_version","type":"semver"},{"key":"signed_up","type":"date"},{"key":"tags","type":"list"}],
		"segments":[],"flags":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestAttributesAreTypedAgainstTheDeclaredOnes(t *testing.T) {
	in := map[string]any{
		"plan": "pro", "seats": 3.0, "beta": true, "app_version": "1.2.3", "signed_up": 1.7e12, "tags": []any{"a", "b"},
		"favourite_colour": "green", "user": "u-1", "gone": nil,
	}
	typed, ignored := delivery.TypeAttributes(remoteRuleset(t), in)
	wantTyped := map[string]any{"plan": "pro", "seats": 3.0, "beta": true, "app_version": "1.2.3", "signed_up": 1.7e12, "tags": []any{"a", "b"}}
	if !reflect.DeepEqual(typed, wantTyped) {
		t.Errorf("typed = %v, want %v", typed, wantTyped)
	}
	wantIgnored := []delivery.IgnoredAttribute{
		{Name: "favourite_colour", Code: delivery.IgnoredUndeclared},
		{Name: "gone", Code: delivery.IgnoredUndeclared},
		{Name: "user", Code: delivery.IgnoredUndeclared},
	}
	if !reflect.DeepEqual(ignored, wantIgnored) {
		t.Errorf("ignored = %v, want %v", ignored, wantIgnored)
	}

	mismatched := map[string]any{
		"plan": 3.0, "seats": "3", "beta": "true", "app_version": 1.0, "signed_up": true, "tags": []any{"a", 1.0},
		"nulled": nil,
	}
	mismatched["plan"] = 3.0
	typed, ignored = delivery.TypeAttributes(remoteRuleset(t), mismatched)
	if len(typed) != 0 {
		t.Errorf("typed = %v, want nothing of the wrong type", typed)
	}
	names := make([]string, 0, len(ignored))
	for _, a := range ignored {
		if a.Name != "nulled" && a.Code != delivery.IgnoredTypeMismatch {
			t.Errorf("%s: code = %q, want type_mismatch", a.Name, a.Code)
		}
		names = append(names, a.Name)
	}
	if want := []string{"app_version", "beta", "nulled", "plan", "seats", "signed_up", "tags"}; !slices.Equal(names, want) {
		t.Errorf("ignored = %v, want %v in order", names, want)
	}

	typed, ignored = delivery.TypeAttributes(remoteRuleset(t), map[string]any{"plan": nil, "signed_up": "2026-01-02T03:04:05Z"})
	if !reflect.DeepEqual(typed, map[string]any{"plan": nil, "signed_up": "2026-01-02T03:04:05Z"}) || len(ignored) != 0 {
		t.Errorf("null and an RFC 3339 date: typed = %v, ignored = %v", typed, ignored)
	}
}
