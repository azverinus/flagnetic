package openfeature

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// reasonsFixture is the mapping every Flagnetic provider in every language
// follows, so that dashboards agree across languages.
const reasonsFixture = "../../../conformance/openfeature/reasons.json"

type reasonsFile struct {
	Name        string            `json:"name"`
	MetadataKey string            `json:"metadata_key"`
	Reasons     map[string]string `json:"reasons"`
}

func decodeReasonsFixture(t *testing.T) reasonsFile {
	t.Helper()
	data, err := os.ReadFile(reasonsFixture)
	if err != nil {
		t.Fatal(err)
	}
	var fixture reasonsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixture); err != nil {
		t.Fatalf("decode %s: %v", reasonsFixture, err)
	}
	if len(fixture.Reasons) == 0 {
		t.Fatal("the fixture maps no reason")
	}
	return fixture
}

// readReasonsFixture is the fixture's mapping from Flagnetic reason to
// OpenFeature reason.
func readReasonsFixture(t *testing.T) map[string]string {
	t.Helper()
	return decodeReasonsFixture(t).Reasons
}

// TestReasonsFollowTheConformanceMapping maps every Flagnetic reason of
// the fixture as the fixture says, carries the Flagnetic reason under the
// fixture's metadata key, and fails when the SDK gains a reason the fixture
// does not map.
func TestReasonsFollowTheConformanceMapping(t *testing.T) {
	fixture := decodeReasonsFixture(t)
	for reason, want := range fixture.Reasons {
		if got := reasonOf(flagnetic.Reason(reason)); string(got) != want {
			t.Errorf("reason %s maps to %s, want %s", reason, got, want)
		}
	}
	if fixture.MetadataKey != reasonMetadataKey {
		t.Errorf("the Flagnetic reason travels under %q, the fixture says %q", reasonMetadataKey, fixture.MetadataKey)
	}
	for _, r := range []flagnetic.Reason{
		flagnetic.ReasonRule, flagnetic.ReasonRollout, flagnetic.ReasonExperiment, flagnetic.ReasonOverride,
		flagnetic.ReasonPrerequisiteFailed, flagnetic.ReasonOff, flagnetic.ReasonFallthrough,
		flagnetic.ReasonLocalOverride, flagnetic.ReasonFallback,
	} {
		if _, ok := fixture.Reasons[string(r)]; !ok {
			t.Errorf("the SDK's reason %s is missing from the fixture", r)
		}
	}
	if got := reasonOf("a_reason_from_a_newer_server"); got != of.UnknownReason {
		t.Errorf("an unknown reason maps to %s, want UNKNOWN", got)
	}
}

// TestAFallbackIsAnErrorCarryingTheFlagneticReason: a fallback resolves to
// ERROR with its error code, and the metadata still names fallback.
func TestAFallbackIsAnErrorCarryingTheFlagneticReason(t *testing.T) {
	r := resolution(flagnetic.Detail{Reason: flagnetic.ReasonFallback, ErrorCode: flagnetic.ErrorTypeMismatch})
	if r.Reason != of.ErrorReason || r.ResolutionError.Error() != of.NewTypeMismatchResolutionError("type_mismatch").Error() {
		t.Errorf("fallback resolves to %s / %v", r.Reason, r.ResolutionError)
	}
	if r.FlagMetadata[reasonMetadataKey] != "fallback" {
		t.Errorf("metadata %s = %v, want fallback", reasonMetadataKey, r.FlagMetadata[reasonMetadataKey])
	}
}
