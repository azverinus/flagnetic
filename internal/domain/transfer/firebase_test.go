package transfer_test

import (
	"encoding/json"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/transfer/firebase"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

func booleanVariations() []flags.Variation {
	return []flags.Variation{{Key: "true", Value: json.RawMessage(`true`)}, {Key: "false", Value: json.RawMessage(`false`)}}
}

func TestAFirebaseDocumentHoldsTheTemplatesFlagsAndAttributesAndNothingElse(t *testing.T) {
	current := existing()
	res := firebase.Result{
		Attributes: []firebase.Attribute{{Key: "plan_type", Type: workspace.TypeString, Name: "Plan Type", Description: "The Firebase user property."}},
		Flags: []firebase.Flag{{
			Key: "fresh", Type: flags.TypeBoolean, Name: "fresh", Description: "New.", Tags: []string{"Group"}, Variations: booleanVariations(),
			On: true, OffVariation: "false", Fallthrough: "false",
			Rules: []flags.Rule{{Description: `Firebase condition "ios"`, Conditions: []flags.Condition{{Kind: "attribute", Attribute: "platform", Op: "eq", Value: "ios"}},
				Serve: flags.Serve{Kind: flags.ServeVariation, Variation: "true"}}},
		}},
	}

	doc := transfer.FirebaseDocument(current, res, "development")

	if doc.Project != nil || doc.Environments != nil || doc.UnitTypes != nil || doc.Segments != nil || doc.Experiments != nil {
		t.Errorf("document = %+v, want only attributes and flags", doc)
	}
	if len(doc.Attributes) != 1 || doc.Attributes[0] != (transfer.Attribute{Key: "plan_type", Type: "string", Name: "Plan Type", Description: "The Firebase user property."}) {
		t.Errorf("attributes = %+v", doc.Attributes)
	}
	if len(doc.Flags) != 1 || doc.Flags[0].Key != "fresh" || doc.Flags[0].Name != "fresh" || doc.Flags[0].Owner != "" {
		t.Fatalf("flags = %+v", doc.Flags)
	}
	c := doc.Flags[0].Configurations["development"]
	if !c.On || len(c.Rules) != 1 || c.Rules[0].ID != "" || c.Rules[0].Serve.Variation != "true" || c.Prerequisites == nil || c.Overrides == nil {
		t.Errorf("configuration = %+v, want on with one rule that has no id yet", c)
	}
	if _, ok := doc.Flags[0].Configurations["production"]; ok {
		t.Error("a new flag is configured in an environment the template does not name")
	}
}

func TestAFirebaseDocumentKeepsWhatTheTemplateDoesNotSayOfAnExistingFlag(t *testing.T) {
	current := existing()
	keptRule := flags.Rule{Description: "Half"}
	if err := keptRule.ID.UnmarshalText([]byte(ruleA)); err != nil {
		t.Fatal(err)
	}
	keptRule.Serve = flags.Serve{Kind: flags.ServeRollout, Variation: "true", UnitType: "user", Percentage: 5000, Salt: "abcdefghij012345"}
	res := firebase.Result{Flags: []firebase.Flag{{
		Key: "checkout", Type: flags.TypeBoolean, Name: "Checkout", Description: "From Firebase.", Tags: []string{"Group"}, Existing: true,
		Variations: booleanVariations(), On: true, OffVariation: "true", Fallthrough: "true", Rules: []flags.Rule{keptRule},
	}}}

	doc := transfer.FirebaseDocument(current, res, "production")

	f := doc.Flags[0]
	if f.Owner != "ada@example.com" || f.Description != "From Firebase." || len(f.Tags) != 1 {
		t.Errorf("flag = %+v, want its owner kept and its description and tags set", f)
	}
	if got, want := f.Configurations["development"], current.Flags[0].Configurations["development"]; got.OffVariation != want.OffVariation || got.On != want.On {
		t.Errorf("development = %+v, want %+v", got, want)
	}
	c := f.Configurations["production"]
	if c.OffVariation != "true" || len(c.Overrides) != 2 || c.Rules[0].ID != ruleA || c.Rules[0].Serve.Salt != "abcdefghij012345" {
		t.Errorf("production = %+v, want the translation with the overrides and the rule's identity kept", c)
	}
	// The current document is not changed by building the next one.
	if current.Flags[0].Configurations["production"].OffVariation != "false" {
		t.Error("FirebaseDocument changed the document it was given")
	}
}

func TestFirebaseWarningsKeepTheirCodePointerAndParams(t *testing.T) {
	got := transfer.FirebaseWarnings([]firebase.Warning{{Code: "in_app_default", Pointer: "/parameters/a", Detail: "d", Params: map[string]any{"parameter": "a"}}})
	if len(got) != 1 || got[0].Code != "in_app_default" || got[0].Pointer != "/parameters/a" || got[0].Params["parameter"] != "a" {
		t.Errorf("warnings = %+v", got)
	}
}
