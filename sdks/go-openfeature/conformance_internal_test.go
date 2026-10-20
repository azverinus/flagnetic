package openfeature

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
	"github.com/azverinus/flagnetic/sdks/go/flagnetictest"
)

// fixturesDir is the language-agnostic conformance suite, two
// levels up from sdks/go-openfeature.
const fixturesDir = "../../conformance/fixtures"

type fixture struct {
	Ruleset json.RawMessage `json:"ruleset"`
	Cases   []struct {
		Context flagnetic.Context `json:"context"`
		Flag    string            `json:"flag"`
		Expect  struct {
			Reason string `json:"reason"`
		} `json:"expect"`
	} `json:"cases"`
}

type flagTypes struct {
	Flags []struct {
		Key  string `json:"key"`
		Type string `json:"type"`
	} `json:"flags"`
}

var conformanceDefaults = map[string]any{
	"boolean": false,
	"string":  "conformance-default",
	"number":  float64(-1),
	"json":    json.RawMessage(`{"conformance":"default"}`),
}

// flatten turns a flagnetic.Context back into a FlattenedContext, every
// unit routed through the "units" attribute so the test needs no unit type
// of its own: p.context (the code under test) reads it back out, and the
// oracle below evaluates the very same flagnetic.Context directly.
func flattenForTest(ctx flagnetic.Context) of.FlattenedContext {
	flat := of.FlattenedContext{}
	for k, v := range ctx.Attributes {
		flat[k] = v
	}
	if len(ctx.Units) > 0 {
		units := make(map[string]any, len(ctx.Units))
		for unitType, unitID := range ctx.Units {
			units[unitType] = unitID
		}
		flat[attributeUnits] = units
	}
	return flat
}

// TestConformanceAgreesWithDetail runs every conformance fixture case
// through the provider's typed evaluation methods and checks the result
// against the wrapped client's own Detail for the identical context: the
// context translation, the value conversion and the reason/error mapping
// must never disagree with what the SDK itself would have served. The
// OpenFeature reason of every case is also checked against the fixture's
// expected reason as the reason fixture maps it, so a wrong mapping cannot
// agree with itself.
func TestConformanceAgreesWithDetail(t *testing.T) {
	mapping := readReasonsFixture(t)
	var files []string
	err := filepath.WalkDir(fixturesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// murmur3.json and regex.json hold vectors, not rulesets: they pin
		// the evaluator, which the SDK's own runner puts them through.
		if base := filepath.Base(path); !d.IsDir() && strings.HasSuffix(path, ".json") && base != "murmur3.json" && base != "regex.json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures found")
	}
	cases := 0
	for _, path := range files {
		rel, _ := filepath.Rel(fixturesDir, path)
		t.Run(strings.TrimSuffix(filepath.ToSlash(rel), ".json"), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var f fixture
			if err = json.Unmarshal(data, &f); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			var types flagTypes
			if err = json.Unmarshal(f.Ruleset, &types); err != nil {
				t.Fatal(err)
			}
			typeOf := map[string]string{}
			for _, fl := range types.Flags {
				typeOf[fl.Key] = fl.Type
			}

			oracle := flagnetictest.New(t, f.Ruleset)
			provider, err := New("", "", WithClientOptions(flagnetic.WithOffline(f.Ruleset)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(func() { _ = provider.ShutdownWithContext(context.Background()) })

			for _, tc := range f.Cases {
				cases++
				typ := typeOf[tc.Flag]
				if typ == "" {
					typ = "boolean"
				}
				def := conformanceDefaults[typ]
				want := oracle.Detail(context.Background(), tc.Context, tc.Flag, def)
				flat := flattenForTest(tc.Context)
				var res of.ProviderResolutionDetail

				switch typ {
				case "boolean":
					got := provider.BooleanEvaluation(context.Background(), tc.Flag, false, flat)
					wantValue, _ := want.Value.(bool)
					if want.Reason == flagnetic.ReasonFallback {
						wantValue = false
					}
					agree(t, got.Value, wantValue, got.ProviderResolutionDetail, want)
					res = got.ProviderResolutionDetail
				case "string":
					got := provider.StringEvaluation(context.Background(), tc.Flag, "conformance-default", flat)
					wantValue, _ := want.Value.(string)
					if want.Reason == flagnetic.ReasonFallback {
						wantValue = "conformance-default"
					}
					agree(t, got.Value, wantValue, got.ProviderResolutionDetail, want)
					res = got.ProviderResolutionDetail
				case "number":
					gotFloat := provider.FloatEvaluation(context.Background(), tc.Flag, -1, flat)
					wantFloat, _ := want.Value.(float64)
					if want.Reason == flagnetic.ReasonFallback {
						wantFloat = -1
					}
					agree(t, gotFloat.Value, wantFloat, gotFloat.ProviderResolutionDetail, want)
					res = gotFloat.ProviderResolutionDetail

					// Integers follow the SDK's Int rule, which Detail
					// applies to an int64 default.
					gotInt := provider.IntEvaluation(context.Background(), tc.Flag, -1, flat)
					wantInt := oracle.Detail(context.Background(), tc.Context, tc.Flag, int64(-1))
					wantIntValue, _ := wantInt.Value.(int64)
					agree(t, gotInt.Value, wantIntValue, gotInt.ProviderResolutionDetail, wantInt)
				case "json":
					got := provider.ObjectEvaluation(context.Background(), tc.Flag, conformanceDefaults["json"], flat)
					var wantValue any
					if want.Reason == flagnetic.ReasonFallback {
						wantValue = conformanceDefaults["json"]
					} else if raw, ok := want.Value.(json.RawMessage); ok {
						if err := json.Unmarshal(raw, &wantValue); err != nil {
							t.Fatalf("decode expected JSON: %v", err)
						}
					}
					gotNormalized := normalizeJSON(t, got.Value)
					wantNormalized := normalizeJSON(t, wantValue)
					if !reflect.DeepEqual(gotNormalized, wantNormalized) {
						t.Errorf("ObjectEvaluation.Value = %#v, want %#v", got.Value, wantValue)
					}
					agreeResolution(t, got.ProviderResolutionDetail, want)
					res = got.ProviderResolutionDetail
				default:
					t.Fatalf("unknown flag type %q", typ)
				}
				if wantReason, ok := mapping[tc.Expect.Reason]; !ok {
					t.Errorf("%s: the reason fixture does not map %q", tc.Flag, tc.Expect.Reason)
				} else if string(res.Reason) != wantReason {
					t.Errorf("%s: OpenFeature reason %s, want %s for %s", tc.Flag, res.Reason, wantReason, tc.Expect.Reason)
				}
			}
		})
	}
	if cases == 0 {
		t.Fatal("no cases")
	}
}

// normalizeJSON round-trips v through JSON so a json.RawMessage and its
// decoded form, or two structurally-equal-but-differently-typed values,
// compare equal.
func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return out
}

// agree checks a typed evaluation's value and resolution against the
// oracle's Detail for the same case.
func agree(t *testing.T, gotValue, wantValue any, gotRes of.ProviderResolutionDetail, want flagnetic.Detail) {
	t.Helper()
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("value = %#v, want %#v", gotValue, wantValue)
	}
	agreeResolution(t, gotRes, want)
}

func agreeResolution(t *testing.T, gotRes of.ProviderResolutionDetail, want flagnetic.Detail) {
	t.Helper()
	wantRes := resolution(want)
	if gotRes.Reason != wantRes.Reason {
		t.Errorf("reason = %q, want %q (flagnetic reason %q)", gotRes.Reason, wantRes.Reason, want.Reason)
	}
	if gotRes.Variant != wantRes.Variant {
		t.Errorf("variant = %q, want %q", gotRes.Variant, wantRes.Variant)
	}
	if gotRes.ResolutionError.Error() != wantRes.ResolutionError.Error() {
		t.Errorf("resolution error = %v, want %v", gotRes.ResolutionError, wantRes.ResolutionError)
	}
	if !reflect.DeepEqual(gotRes.FlagMetadata, wantRes.FlagMetadata) {
		t.Errorf("flag metadata = %#v, want %#v", gotRes.FlagMetadata, wantRes.FlagMetadata)
	}
}
