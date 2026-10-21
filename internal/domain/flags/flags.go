// Package flags is the domain of what a project serves: flags with their
// definition and variations, their configuration in each environment, and
// the segments their rules target. Every rule spanning several of them (a
// variation is not removed while a configuration serves it, a segment is not
// deleted while a rule names it) is a pure function over what the handler
// loaded under its project's row lock, so it holds for as long as that lock.
package flags

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Bounds shared by the flags context's entities.
const (
	maxNameLength        = 200
	maxDescriptionLength = 2000
)

// check gathers every broken rule of one write under code, such as
// invalid_flag, so a form shows every field to fix in one round.
type check struct {
	code, detail string
	violations   []apperrors.Violation
}

func newCheck(code, detail string) *check { return &check{code: code, detail: detail} }

func (c *check) add(pointer, code, detail string, params map[string]any) {
	c.violations = append(c.violations, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
}

// name returns name trimmed, refusing a blank one or one over 200
// characters at pointer.
func (c *check) name(pointer, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		c.add(pointer, "invalid_name", "a name has 1 to 200 characters and is not blank",
			map[string]any{"min": 1, "max": maxNameLength})
	}
	return name
}

// description returns description trimmed, refusing one over 2,000
// characters at pointer.
func (c *check) description(pointer, description string) string {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > maxDescriptionLength {
		c.add(pointer, "too_long", "a description has at most 2,000 characters", map[string]any{"max": maxDescriptionLength})
	}
	return description
}

// immutable refuses a change to a field that never changes: everything
// naming the entity by it (SDKs, rules, scripts, URLs) would silently stop
// matching.
func (c *check) immutable(pointer, what string) {
	c.add(pointer, "immutable", what+" never changes; create a new one instead", nil)
}

func (c *check) err() error {
	if len(c.violations) == 0 {
		return nil
	}
	return apperrors.Invalid(c.code, c.detail, c.violations...)
}

// Reference is something that names an entity a write would remove or hide,
// such as a flag's configuration in one environment serving a variation.
type Reference struct {
	// Kind is the kind of what names it, such as flag or segment.
	Kind string `json:"kind"`
	// Key is its key.
	Key string `json:"key"`
	// Environment is the key of the environment it names it in, "" when it
	// names it in every environment, as a segment does.
	Environment string `json:"environment"`
}

// MarshalJSON writes a reference naming no one environment with environment
// null, so a client tells "every environment" from an environment's key.
func (r Reference) MarshalJSON() ([]byte, error) {
	var env *string
	if r.Environment != "" {
		env = &r.Environment
	}
	return json.Marshal(struct {
		Kind        string  `json:"kind"`
		Key         string  `json:"key"`
		Environment *string `json:"environment"`
	}{r.Kind, r.Key, env})
}

// ErrInUse refuses a write that would leave refs naming nothing, listing
// them so the caller knows what to change first.
func ErrInUse(refs []Reference) error {
	return apperrors.Conflict("in_use", "something still names what this write would remove").
		WithExtension("references", refs)
}
