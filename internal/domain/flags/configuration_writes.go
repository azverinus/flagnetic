package flags

import (
	"slices"
	"strings"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// maxReasonLength bounds a kill switch's reason, which the audit log keeps.
const maxReasonLength = 2000

// KillReason returns the reason of a kill switch trimmed, "" for none, or
// invalid_configuration when it is over 2,000 characters.
func KillReason(reason *string) (string, error) {
	if reason == nil {
		return "", nil
	}
	r := strings.TrimSpace(*reason)
	if utf8.RuneCountInString(r) > maxReasonLength {
		ck := newCheck(invalidConfiguration, "the kill switch is not valid")
		ck.add("/reason", "too_long", "a reason has at most 2,000 characters", map[string]any{"max": maxReasonLength})
		return "", ck.err()
	}
	return r, nil
}

// Kill returns the configuration switched off by w, serving its off
// variation to everyone, and what changed: switching off a configuration
// that is off changes nothing.
func (c Configuration) Kill(w Write) (Configuration, Changed) {
	out := c
	out.On = false
	after, changed, _ := c.changedTo(out, w)
	return after, changed
}

// SetOverride returns the configuration of f with o set by w, checked
// against d, and what changed; or invalid_configuration with every field to
// fix. An override of the same unit is replaced in place, variation and
// note together, and is then w's; another is added, up to 200.
func (c Configuration) SetOverride(f Flag, o OverrideInput, d Declarations, w Write) (Configuration, Changed, error) {
	ck := newCheck(invalidConfiguration, configurationNotValid)
	ov := ck.override("", o, f, d, w.By)
	out := c
	out.Overrides = slices.Clone(c.Overrides)
	i := slices.IndexFunc(out.Overrides, ov.sameUnit)
	switch {
	case i >= 0 && out.Overrides[i].Variation == ov.Variation && out.Overrides[i].Note == ov.Note:
		ov.AddedBy = out.Overrides[i].AddedBy
		out.Overrides[i] = ov
	case i >= 0:
		out.Overrides[i] = ov
	case len(out.Overrides) >= maxOverrides:
		ck.add("", "too_many", "a configuration has at most 200 overrides", map[string]any{"max": maxOverrides})
	default:
		out.Overrides = append(out.Overrides, ov)
	}
	if err := ck.err(); err != nil {
		return c, Changed{}, err
	}
	return c.changedTo(out, w)
}

// errNoOverride answers a unit the configuration has no override for.
var errNoOverride = apperrors.NotFound("not_found", "the configuration has no override for this unit")

// RemoveOverride returns the configuration without the override of the
// unit of unitType with unitID, removed by w, and what changed; not_found
// when it has none.
func (c Configuration) RemoveOverride(unitType, unitID string, w Write) (Configuration, Changed, error) {
	unit := Override{UnitType: unitType, UnitID: unitID}
	i := slices.IndexFunc(c.Overrides, unit.sameUnit)
	if i < 0 {
		return c, Changed{}, errNoOverride
	}
	out := c
	out.Overrides = slices.Delete(slices.Clone(c.Overrides), i, i+1)
	return c.changedTo(out, w)
}

// errNoRule answers a rule the configuration does not have.
var errNoRule = apperrors.NotFound("not_found", "the configuration has no rule with this identifier")

// errNotARollout refuses to regenerate the salt of a rule serving no
// rollout: only a rollout has one.
var errNotARollout = apperrors.Conflict("not_a_rollout", "the rule serves no rollout, so it has no salt")

// RegenerateSalt returns the configuration whose rule keyed rule serves its
// rollout with a new salt from m, set by w, and what changed: units are
// bucketed afresh, so some in the rollout fall out and others come in.
// not_found for a rule it does not have, not_a_rollout for a rule serving
// no rollout.
func (c Configuration) RegenerateSalt(rule RuleID, m Mint, w Write) (Configuration, Changed, error) {
	i := slices.IndexFunc(c.Rules, func(r Rule) bool { return r.ID == rule })
	if i < 0 {
		return c, Changed{}, errNoRule
	}
	if c.Rules[i].Serve.Kind != ServeRollout {
		return c, Changed{}, errNotARollout
	}
	out := c
	out.Rules = slices.Clone(c.Rules)
	out.Rules[i].Serve.Salt = m.Salt()
	return c.changedTo(out, w)
}
