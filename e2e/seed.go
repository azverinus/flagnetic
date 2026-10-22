package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// The harness's own project. Keys are fixed so that every SDK's smoke names
// the same things.
const (
	ProjectKey     = "sdk-e2e"
	EnvironmentKey = "development"
	UnitType       = "user"
	SegmentKey     = "e2e-staff"
	// KeyName names the SDK keys the harness creates.
	KeyName = "sdk-e2e"

	FlagOn      = "e2e-on"
	FlagRollout = "e2e-rollout"
	FlagPrivate = "e2e-staff-only"
	FlagOff     = "e2e-off"

	segmentMember = "staff-1"
	// rolloutPercentage is in basis points: half of the units.
	rolloutPercentage = 5000
)

// Seed is what an SDK's smoke reads from a seeded instance. Its JSON form
// is the harness's contract with SDKs in every language, documented in
// README.md: fields are only ever added to it.
type Seed struct {
	// Replicas are the base URLs of the two replicas, a and b; the SDK API
	// is under /sdk/v1.
	Replicas    Replicas `json:"replicas"`
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	ServerKey   string   `json:"server_key"`
	ClientKey   string   `json:"client_key"`
	UnitType    string   `json:"unit_type"`
	// Segment is the private segment.
	Segment string `json:"segment"`
	// Flags maps a role (on, rollout, private_segment, off) to a flag key.
	Flags map[string]string `json:"flags"`
	// Units maps a role (segment_member, in_rollout, outside_rollout) to a
	// unit ID of UnitType.
	Units map[string]string `json:"units"`
	// Cases are the evaluations every SDK asserts.
	Cases []Case `json:"cases"`
}

// Replicas are the base URLs of the two replicas on one database.
type Replicas struct {
	A string `json:"a"`
	B string `json:"b"`
}

// Case is one evaluation of a seeded flag and its expected result.
type Case struct {
	Name    string  `json:"name"`
	Flag    string  `json:"flag"`
	Type    string  `json:"type"`
	Context Context `json:"context"`
	// Value and Variation are served alike to a server key and a client
	// key.
	Value     json.RawMessage `json:"value"`
	Variation string          `json:"variation"`
	// Reason is the true reason, which a server key's local evaluation
	// gives; ClientReason the one a client key is answered, targeting_match
	// wherever targeting decided the value.
	Reason       string `json:"reason"`
	ClientReason string `json:"client_reason"`
}

// Context is an evaluation context in the wire shape of the SDK API.
type Context struct {
	Units map[string]string `json:"units"`
}

// seedFlag is one flag of the seed and its configuration in EnvironmentKey.
type seedFlag struct {
	create management.FlagCreate
	// configuration returns the seed configuration given the current one,
	// whose rule IDs it reuses so that a rollout keeps its salt.
	configuration func(current management.FlagConfiguration) management.FlagConfigurationPut
}

func ptr[T any](v T) *T { return &v }

func variations(keys ...string) *[]management.VariationInput {
	vs := make([]management.VariationInput, 0, len(keys))
	for _, k := range keys {
		vs = append(vs, management.VariationInput{Key: k, Value: json.RawMessage(strconv.Quote(k))})
	}
	return &vs
}

// ruleID returns the ID of the first rule of current serving kind, nil when
// it has none.
func ruleID(current management.FlagConfiguration, kind string) *openapi_types.UUID {
	for _, r := range current.Rules {
		if r.Serve.Kind == kind {
			return ptr(r.Id)
		}
	}
	return nil
}

var segmentConditions = []management.Condition{{
	Kind: "attribute", Attribute: ptr(UnitType), Op: ptr("in"), Value: []any{segmentMember},
}}

var seedFlags = []seedFlag{
	{management.FlagCreate{Key: FlagOn, Type: management.FlagTypeBoolean, Name: "E2E on"},
		func(management.FlagConfiguration) management.FlagConfigurationPut {
			return management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"}
		}},
	{management.FlagCreate{Key: FlagRollout, Type: management.FlagTypeString, Name: "E2E rollout", Variations: variations("control", "treatment")},
		func(current management.FlagConfiguration) management.FlagConfigurationPut {
			rule := management.RuleInput{Id: ruleID(current, "rollout"), Serve: management.Serve{
				Kind: "rollout", UnitType: ptr(UnitType), Variation: ptr("treatment"), Percentage: ptr(rolloutPercentage),
			}}
			return management.FlagConfigurationPut{On: true, OffVariation: "control", Fallthrough: "control", Rules: &[]management.RuleInput{rule}}
		}},
	{management.FlagCreate{Key: FlagPrivate, Type: management.FlagTypeBoolean, Name: "E2E staff only"},
		func(current management.FlagConfiguration) management.FlagConfigurationPut {
			rule := management.RuleInput{
				Id:         ruleID(current, "variation"),
				Conditions: &[]management.Condition{{Kind: "in_segment", Segment: ptr(SegmentKey)}},
				Serve:      management.Serve{Kind: "variation", Variation: ptr("true")},
			}
			return management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false", Rules: &[]management.RuleInput{rule}}
		}},
	{management.FlagCreate{Key: FlagOff, Type: management.FlagTypeBoolean, Name: "E2E off"},
		func(management.FlagConfiguration) management.FlagConfigurationPut {
			return management.FlagConfigurationPut{On: false, OffVariation: "false", Fallthrough: "true"}
		}},
}

// Restore brings the project, the private segment and the flags'
// configurations in EnvironmentKey to the seed state, leaving SDK keys
// alone, and returns the rollout's salt. What already is as the seed has it
// is not written again, so a restore of a seeded instance bumps no ruleset
// version.
func Restore(ctx context.Context, m *Management) (salt string, err error) {
	if err := ensureProject(ctx, m); err != nil {
		return "", err
	}
	if err := ensureSegment(ctx, m); err != nil {
		return "", err
	}
	for _, f := range seedFlags {
		if err := ensureFlag(ctx, m, f.create); err != nil {
			return "", err
		}
		current, err := configuration(ctx, m, f.create.Key)
		if err != nil {
			return "", err
		}
		want := f.configuration(current)
		if !configurationIs(current, want) {
			if current, err = putConfiguration(ctx, m, f.create.Key, current.Version, want); err != nil {
				return "", err
			}
		}
		if f.create.Key == FlagRollout {
			if len(current.Rules) != 1 || current.Rules[0].Serve.Salt == nil {
				return "", fmt.Errorf("configure %s: the answer carries no rollout salt", FlagRollout)
			}
			salt = *current.Rules[0].Serve.Salt
		}
	}
	return salt, nil
}

// seed returns the seed of a restored instance with the rollout's salt.
func seed(replicas Replicas, salt, serverKey, clientKey string) Seed {
	in, out := rolloutUnits(salt)
	contextFor := func(id string) Context { return Context{Units: map[string]string{UnitType: id}} }
	return Seed{
		Replicas:    replicas,
		Project:     ProjectKey,
		Environment: EnvironmentKey,
		ServerKey:   serverKey,
		ClientKey:   clientKey,
		UnitType:    UnitType,
		Segment:     SegmentKey,
		Flags:       map[string]string{"on": FlagOn, "rollout": FlagRollout, "private_segment": FlagPrivate, "off": FlagOff},
		Units:       map[string]string{"segment_member": segmentMember, "in_rollout": in, "outside_rollout": out},
		Cases: []Case{
			{Name: "a flag that is on serves its fallthrough", Flag: FlagOn, Type: "boolean", Context: contextFor(out),
				Value: json.RawMessage(`true`), Variation: "true", Reason: "fallthrough", ClientReason: "targeting_match"},
			{Name: "a unit inside the rollout gets its variation", Flag: FlagRollout, Type: "string", Context: contextFor(in),
				Value: json.RawMessage(`"treatment"`), Variation: "treatment", Reason: "rollout", ClientReason: "targeting_match"},
			{Name: "a unit outside the rollout gets the fallthrough", Flag: FlagRollout, Type: "string", Context: contextFor(out),
				Value: json.RawMessage(`"control"`), Variation: "control", Reason: "fallthrough", ClientReason: "targeting_match"},
			{Name: "a member of the private segment matches its rule", Flag: FlagPrivate, Type: "boolean", Context: contextFor(segmentMember),
				Value: json.RawMessage(`true`), Variation: "true", Reason: "rule", ClientReason: "targeting_match"},
			{Name: "a unit outside the private segment gets the fallthrough", Flag: FlagPrivate, Type: "boolean", Context: contextFor(out),
				Value: json.RawMessage(`false`), Variation: "false", Reason: "fallthrough", ClientReason: "targeting_match"},
			{Name: "a flag that is off serves its off variation", Flag: FlagOff, Type: "boolean", Context: contextFor(out),
				Value: json.RawMessage(`false`), Variation: "false", Reason: "off", ClientReason: "off"},
		},
	}
}

func ensureProject(ctx context.Context, m *Management) error {
	res, err := m.GetProjectWithResponse(ctx, ProjectKey)
	if err != nil {
		return err
	}
	switch res.StatusCode() {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
	default:
		return unexpected("read project "+ProjectKey, res.StatusCode(), res.Body)
	}
	// A new project comes with development, staging and production, the
	// unit types user and anonymous, and the standard attributes.
	created, err := m.CreateProjectWithResponse(ctx, management.ProjectCreate{Key: ProjectKey, Name: "SDK end to end"})
	if err != nil {
		return err
	}
	if created.JSON201 == nil {
		return unexpected("create project "+ProjectKey, created.StatusCode(), created.Body)
	}
	return nil
}

func ensureSegment(ctx context.Context, m *Management) error {
	res, err := m.GetSegmentWithResponse(ctx, ProjectKey, SegmentKey)
	if err != nil {
		return err
	}
	switch {
	case res.JSON200 != nil:
		if res.JSON200.Private && sameJSON(res.JSON200.Conditions, segmentConditions) {
			return nil
		}
		updated, updateErr := m.UpdateSegmentWithResponse(ctx, ProjectKey, SegmentKey,
			&management.UpdateSegmentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(res.JSON200.Version, 10)))},
			management.SegmentPatch{Private: ptr(true), Conditions: &segmentConditions})
		if updateErr != nil {
			return updateErr
		}
		if updated.JSON200 == nil {
			return unexpected("update segment "+SegmentKey, updated.StatusCode(), updated.Body)
		}
		return nil
	case res.StatusCode() != http.StatusNotFound:
		return unexpected("read segment "+SegmentKey, res.StatusCode(), res.Body)
	}
	created, err := m.CreateSegmentWithResponse(ctx, ProjectKey, management.SegmentCreate{
		Key: SegmentKey, Name: "E2E staff", Private: ptr(true), Conditions: &segmentConditions,
	})
	if err != nil {
		return err
	}
	if created.JSON201 == nil {
		return unexpected("create segment "+SegmentKey, created.StatusCode(), created.Body)
	}
	return nil
}

// ensureFlag creates flag unless it exists, and restores it when a smoke
// archived it.
func ensureFlag(ctx context.Context, m *Management, flag management.FlagCreate) error {
	res, err := m.GetFlagWithResponse(ctx, ProjectKey, flag.Key)
	if err != nil {
		return err
	}
	switch {
	case res.JSON200 != nil:
		if res.JSON200.ArchivedAt == nil {
			return nil
		}
		restored, restoreErr := m.RestoreFlagWithResponse(ctx, ProjectKey, flag.Key,
			&management.RestoreFlagParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(res.JSON200.Version, 10)))})
		if restoreErr != nil {
			return restoreErr
		}
		if restored.StatusCode() != http.StatusOK {
			return unexpected("restore flag "+flag.Key, restored.StatusCode(), restored.Body)
		}
		return nil
	case res.StatusCode() != http.StatusNotFound:
		return unexpected("read flag "+flag.Key, res.StatusCode(), res.Body)
	}
	flag.Lifetime = ptr(management.LifetimePermanent)
	created, err := m.CreateFlagWithResponse(ctx, ProjectKey, flag)
	if err != nil {
		return err
	}
	if created.StatusCode() != http.StatusCreated {
		return unexpected("create flag "+flag.Key, created.StatusCode(), created.Body)
	}
	return nil
}

func configuration(ctx context.Context, m *Management, flag string) (management.FlagConfiguration, error) {
	res, err := m.GetFlagConfigurationWithResponse(ctx, ProjectKey, flag, EnvironmentKey)
	if err != nil {
		return management.FlagConfiguration{}, err
	}
	if res.JSON200 == nil {
		return management.FlagConfiguration{}, unexpected("read configuration of "+flag, res.StatusCode(), res.Body)
	}
	return *res.JSON200, nil
}

func putConfiguration(ctx context.Context, m *Management, flag string, version int64, want management.FlagConfigurationPut) (management.FlagConfiguration, error) {
	res, err := m.PutFlagConfigurationWithResponse(ctx, ProjectKey, flag, EnvironmentKey,
		&management.PutFlagConfigurationParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(version, 10)))}, want)
	if err != nil {
		return management.FlagConfiguration{}, err
	}
	if res.JSON200 == nil {
		return management.FlagConfiguration{}, unexpected("configure "+flag, res.StatusCode(), res.Body)
	}
	return *res.JSON200, nil
}

// configurationIs reports whether current already is want, rule IDs and
// salts aside, which want either reuses or leaves to the server.
func configurationIs(current management.FlagConfiguration, want management.FlagConfigurationPut) bool {
	if current.On != want.On || current.OffVariation != want.OffVariation || current.Fallthrough != want.Fallthrough ||
		len(current.Overrides) != 0 || len(current.Prerequisites) != 0 {
		return false
	}
	var rules []management.RuleInput
	if want.Rules != nil {
		rules = *want.Rules
	}
	if len(current.Rules) != len(rules) {
		return false
	}
	for i, r := range current.Rules {
		w := rules[i]
		var conditions []management.Condition
		if w.Conditions != nil {
			conditions = *w.Conditions
		}
		served := r.Serve
		served.Salt = nil
		if !sameJSON(r.Conditions, conditions) || !sameJSON(served, w.Serve) {
			return false
		}
	}
	return true
}

// sameJSON compares two values by their JSON form; an empty list and none
// are the same.
func sameJSON(a, b any) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	if errX != nil || errY != nil {
		return false
	}
	empty := func(s []byte) bool { return string(s) == "null" || string(s) == "[]" }
	return string(x) == string(y) || empty(x) && empty(y)
}

// rolloutUnits returns the first unit IDs user-1, user-2 … whose bucket
// under salt falls inside and outside the rollout, by the published
// bucketing formula.
func rolloutUnits(salt string) (in, out string) {
	for i := 1; in == "" || out == ""; i++ {
		id := "user-" + strconv.Itoa(i)
		if eval.Bucket(salt, id) < rolloutPercentage {
			if in == "" {
				in = id
			}
		} else if out == "" {
			out = id
		}
	}
	return in, out
}
