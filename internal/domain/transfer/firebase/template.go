// Package firebase translates a Firebase Remote Config template into the
// flags, rules and attributes to merge into one environment: parameters
// become flags, conditional values become rules, and everything the
// translation cannot reproduce becomes a coded warning pointing into the
// template. The translation is pure: it reads the project's current flags and
// declarations it is given and returns its own result, and the transfer
// context turns that result into a project document.
package firebase

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// The refusal of a template that cannot be translated, whatever the fault.
const (
	invalidDocument = "invalid_document"
	invalidDetail   = "the Remote Config template is not valid"
)

// Template is a Remote Config template as the REST API and `firebase
// remoteconfig:get` return it. Fields the translation does not use, such as
// version, are ignored, so a template from a newer Firebase still imports.
type Template struct {
	Conditions      []Condition          `json:"conditions"`
	Parameters      map[string]Parameter `json:"parameters"`
	ParameterGroups map[string]Group     `json:"parameterGroups"`
}

// Condition is a named expression that conditional values refer to.
type Condition struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

// Group is a parameter group; its name becomes a tag of its flags.
type Group struct {
	Description string               `json:"description"`
	Parameters  map[string]Parameter `json:"parameters"`
}

// Parameter is one Remote Config parameter.
type Parameter struct {
	DefaultValue      *Value           `json:"defaultValue"`
	ConditionalValues map[string]Value `json:"conditionalValues"`
	Description       string           `json:"description"`
	ValueType         string           `json:"valueType"`
}

// Value is a parameter value: a literal, the in-app default, or a kind of
// value the translation cannot reproduce.
type Value struct {
	Value                *string         `json:"value"`
	UseInAppDefault      bool            `json:"useInAppDefault"`
	PersonalizationValue json.RawMessage `json:"personalizationValue"`
	RolloutValue         json.RawMessage `json:"rolloutValue"`
}

// Decode reads a template and refuses one that is not JSON, that holds a
// condition without a name or with a name already taken, whose conditional
// value names a condition the template lacks, or that holds a parameter in
// two places. Every fault found is reported at once, each with its JSON
// Pointer into the template.
func Decode(data []byte) (Template, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var t Template
	if err := dec.Decode(&t); err != nil {
		return Template{}, invalid(apperrors.Violation{
			Pointer: "", Code: "invalid_json", Detail: "the template is not a Remote Config template: " + err.Error(),
		})
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Template{}, invalid(apperrors.Violation{
			Pointer: "", Code: "invalid_json", Detail: "the template is malformed: data follows the template",
		})
	}
	if _, err := t.validate(); err != nil {
		return Template{}, err
	}
	return t, nil
}

// validate finds the faults a template can hold whatever its source and
// returns its parameters, by key.
func (t Template) validate() ([]grouped, error) {
	var faults []apperrors.Violation
	names := map[string]bool{}
	for i, c := range t.Conditions {
		pointer := "/conditions/" + strconv.Itoa(i) + "/name"
		switch {
		case c.Name == "":
			faults = append(faults, apperrors.Violation{Pointer: pointer, Code: "required", Detail: "a condition has a name"})
		case names[c.Name]:
			faults = append(faults, apperrors.Violation{Pointer: pointer, Code: "duplicate_condition",
				Detail: "two conditions are named " + strconv.Quote(c.Name), Params: map[string]any{"condition": c.Name}})
		}
		names[c.Name] = true
	}
	params, duplicates := t.parameters()
	faults = append(faults, duplicates...)
	for _, p := range params {
		for _, name := range slices.Sorted(maps.Keys(p.ConditionalValues)) {
			if !names[name] {
				faults = append(faults, apperrors.Violation{Pointer: p.pointer + "/conditionalValues/" + escape(name),
					Code:   "unknown_condition",
					Detail: "the parameter names a condition the template does not have", Params: map[string]any{"parameter": p.key, "condition": name}})
			}
		}
	}
	if len(faults) > 0 {
		return nil, invalid(faults...)
	}
	return params, nil
}

func invalid(faults ...apperrors.Violation) error {
	return apperrors.Invalid(invalidDocument, invalidDetail, faults...)
}

// grouped is a parameter with its key, the group it belongs to (empty for a
// top-level one) and its JSON Pointer in the template.
type grouped struct {
	Parameter
	key     string
	group   string
	pointer string
}

// parameters lists every parameter, top-level and grouped, by key, and a
// fault for each key that appears a second time; only its first place is
// listed.
func (t Template) parameters() ([]grouped, []apperrors.Violation) {
	var out []grouped
	var faults []apperrors.Violation
	seen := map[string]bool{}
	add := func(group, prefix string, params map[string]Parameter) {
		for _, key := range slices.Sorted(maps.Keys(params)) {
			pointer := prefix + "/" + escape(key)
			if seen[key] {
				faults = append(faults, apperrors.Violation{Pointer: pointer, Code: "duplicate_parameter",
					Detail: "the parameter " + strconv.Quote(key) + " appears in two places", Params: map[string]any{"parameter": key}})
				continue
			}
			seen[key] = true
			out = append(out, grouped{Parameter: params[key], key: key, group: group, pointer: pointer})
		}
	}
	add("", "/parameters", t.Parameters)
	for _, g := range slices.Sorted(maps.Keys(t.ParameterGroups)) {
		add(g, "/parameterGroups/"+escape(g)+"/parameters", t.ParameterGroups[g].Parameters)
	}
	slices.SortFunc(out, func(a, b grouped) int { return cmp.Compare(a.key, b.key) })
	return out, faults
}

// escape writes a key as one segment of a JSON Pointer (RFC 6901).
func escape(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}
