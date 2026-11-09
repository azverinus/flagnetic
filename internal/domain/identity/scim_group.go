package identity

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	filter "github.com/scim2/filter-parser/v2"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// GroupSchema names the SCIM Group resource.
const GroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"

// SCIMGroup is a group an identity provider pushes through SCIM. It belongs
// to the provider of the token that created it; group mappings match its
// display name as they match a claimed group.
type SCIMGroup struct{}

// SCIMGroupID identifies a SCIM group.
type SCIMGroupID = id.Of[SCIMGroup]

// ErrGroupNotFound answers a SCIM Group the token's provider does not hold,
// whether it exists elsewhere or not.
var ErrGroupNotFound = apperrors.NotFound("not_found", "no group of this provider has this id")

// GroupAttribute is an attribute of a SCIM Group a filter may compare.
type GroupAttribute string

// The attributes a filter on Groups compares, each exactly.
const (
	GroupDisplayName GroupAttribute = "display_name"
	GroupExternalID  GroupAttribute = "external_id"
	GroupID          GroupAttribute = "id"
)

// GroupCondition is one equality of a filter on Groups.
type GroupCondition struct {
	Attribute GroupAttribute
	Value     string
}

// GroupFilter selects Groups by equalities that all hold: `eq` on
// displayName, externalId and id, alone or joined by `and`.
type GroupFilter struct {
	Conditions []GroupCondition
}

// ParseGroupFilter returns the filter raw states; an empty one selects every
// Group. Anything else is invalidFilter, as for Users.
func ParseGroupFilter(raw string) (GroupFilter, error) {
	conds, err := parseEqualities(raw, groupFilterAttribute, "displayName, externalId and id")
	var f GroupFilter
	for _, c := range conds {
		f.Conditions = append(f.Conditions, GroupCondition{Attribute: GroupAttribute(c.attribute), Value: c.value})
	}
	return f, err
}

func groupFilterAttribute(p filter.AttributePath) (string, bool) {
	if p.URIPrefix != nil && !strings.EqualFold(*p.URIPrefix, GroupSchema) {
		return "", false
	}
	if p.SubAttributeName() != "" {
		return "", false
	}
	switch strings.ToLower(p.AttributeName) {
	case "displayname":
		return string(GroupDisplayName), true
	case "externalid":
		return string(GroupExternalID), true
	case "id":
		return string(GroupID), true
	}
	return "", false
}

// GroupState is what a SCIM client writes of a group: its name, its
// external ID ("" for none) and its members, each once, in the order they
// were given.
type GroupState struct {
	DisplayName string
	ExternalID  string
	Members     []MemberID
}

// maxGroupNameLength bounds a group's display name.
const maxGroupNameLength = 256

// NewGroupState checks a group as a POST or a PUT states it: a display name
// that is not blank, an external ID as a member's is bounded (a blank one is
// none), and members named by their identifiers. A member named twice is one
// member.
func NewGroupState(displayName, externalID string, members []string) (GroupState, error) {
	var g GroupState
	if err := setGroupName(&g, displayName); err != nil {
		return GroupState{}, err
	}
	if strings.TrimSpace(externalID) != "" {
		if err := CheckExternalID(externalID); err != nil {
			return GroupState{}, err
		}
		g.ExternalID = externalID
	}
	for _, raw := range members {
		m, err := groupMember(raw)
		if err != nil {
			return GroupState{}, err
		}
		g.Members = addMembers(g.Members, m)
	}
	return g, nil
}

func setGroupName(g *GroupState, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxGroupNameLength {
		return scimErr("invalidValue", "displayName is required, with at most %d characters", maxGroupNameLength)
	}
	g.DisplayName = name
	return nil
}

func groupMember(raw string) (MemberID, error) {
	m, err := id.Parse[Member](strings.TrimSpace(raw))
	if err != nil {
		return MemberID{}, scimErr("invalidValue", "members: %q is not the id of a User", raw)
	}
	return m, nil
}

func addMembers(to []MemberID, add ...MemberID) []MemberID {
	for _, m := range add {
		if !slices.Contains(to, m) {
			to = append(to, m)
		}
	}
	return to
}

// ApplyGroupPatch applies the operations of a PatchOp, in order, to g, the
// state of the group whose id is self, and returns the state it leads to.
// The shapes Entra ID and Okta send are all read: `members` added, removed
// and replaced by a list of {value}, `remove` of `members` without a value
// emptying the group, `remove` of `members[value eq "…"]`, `displayName` and
// `externalId` by path or in a path-less object (Okta repeats the group's id
// in it, which is accepted when it is self). Removing a member who is not
// one changes nothing. The display name cannot be removed; anything else is
// refused as for Users: read-only as mutability, unmapped as noTarget.
func ApplyGroupPatch(g GroupState, self string, ops []PatchOperation) (GroupState, error) {
	out := GroupState{DisplayName: g.DisplayName, ExternalID: g.ExternalID, Members: slices.Clone(g.Members)}
	if len(ops) == 0 {
		return out, scimErr("invalidValue", "a PatchOp has at least one operation")
	}
	for _, o := range ops {
		op := strings.ToLower(strings.TrimSpace(o.Op))
		if op != "add" && op != "replace" && op != "remove" {
			return out, scimErr("invalidSyntax", "the operation %q is none of add, replace and remove", o.Op)
		}
		if err := applyGroupOperation(&out, self, op, o); err != nil {
			return out, err
		}
	}
	return out, nil
}

func applyGroupOperation(g *GroupState, self, op string, o PatchOperation) error {
	if strings.TrimSpace(o.Path) != "" {
		return applyGroupPath(g, op, o.Path, o.Value, o.Value != nil)
	}
	if op == "remove" {
		return scimErr("noTarget", "a remove operation names the attribute to remove in its path")
	}
	obj, ok := o.Value.(map[string]any)
	if !ok {
		return scimErr("invalidValue", "an operation without a path carries an object of attributes")
	}
	// Applied in the order of their names, so that one request always has
	// one outcome.
	names := slices.SortedFunc(maps.Keys(obj), func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	for _, name := range names {
		// Okta repeats the group's own id among them, which is no change.
		if strings.EqualFold(name, "id") && self != "" {
			if s, isString := obj[name].(string); isString && strings.EqualFold(s, self) {
				continue
			}
		}
		if err := applyGroupPath(g, op, name, obj[name], true); err != nil {
			return err
		}
	}
	return nil
}

func applyGroupPath(g *GroupState, op, path string, value any, hasValue bool) error {
	parsed, err := filter.ParsePath([]byte(path))
	if err != nil {
		return scimErr("invalidSyntax", "the path %q is not valid: %v", path, err)
	}
	ap := parsed.AttributePath
	if ap.URIPrefix != nil && !strings.EqualFold(*ap.URIPrefix, GroupSchema) {
		return scimErr("noTarget", "the path %q names an attribute of another schema", path)
	}
	switch strings.ToLower(ap.AttributeName) {
	case "members":
		if parsed.ValueExpression != nil {
			return removeMemberByFilter(g, op, parsed.ValueExpression)
		}
		if ap.SubAttributeName() != "" || parsed.SubAttributeName() != "" {
			return scimErr("noTarget", "only members as a whole, or one chosen by value, can be patched")
		}
		return patchMembers(g, op, value, hasValue)
	case "displayname":
		if op == "remove" {
			return scimErr("invalidValue", "displayName cannot be removed")
		}
		name, ok := value.(string)
		if !ok {
			return scimErr("invalidValue", "displayName is a string")
		}
		return setGroupName(g, name)
	case "externalid":
		return patchGroupExternalID(g, op, value)
	case "id", "meta":
		return scimErr("mutability", "%s is read only", ap.AttributeName)
	}
	return scimErr("noTarget", "%s is not an attribute the instance maps", path)
}

func patchGroupExternalID(g *GroupState, op string, value any) error {
	if op == "remove" || value == nil {
		g.ExternalID = ""
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return scimErr("invalidValue", "externalId is a string")
	}
	if strings.TrimSpace(s) == "" {
		g.ExternalID = ""
		return nil
	}
	if err := CheckExternalID(s); err != nil {
		return err
	}
	g.ExternalID = s
	return nil
}

// removeMemberByFilter applies `members[value eq "…"]`, which only removes.
func removeMemberByFilter(g *GroupState, op string, e filter.Expression) error {
	if op != "remove" {
		return scimErr("invalidValue", "a member chosen by a filter can only be removed")
	}
	x, ok := e.(*filter.AttributeExpression)
	if !ok || x.Operator != filter.EQ || !strings.EqualFold(x.AttributePath.AttributeName, "value") || x.AttributePath.SubAttributeName() != "" {
		return scimErr("invalidFilter", "members are chosen by `value eq \"<id>\"` alone")
	}
	raw, ok := x.CompareValue.(string)
	if !ok {
		return scimErr("invalidFilter", "a member's value is a string")
	}
	m, err := groupMember(unescape(raw))
	if err != nil {
		return err
	}
	g.Members = slices.DeleteFunc(g.Members, func(held MemberID) bool { return held == m })
	return nil
}

func patchMembers(g *GroupState, op string, value any, hasValue bool) error {
	if op == "remove" && !hasValue {
		g.Members = nil
		return nil
	}
	members, err := memberRefs(value)
	if err != nil {
		return err
	}
	switch op {
	case "add":
		g.Members = addMembers(g.Members, members...)
	case "remove":
		g.Members = slices.DeleteFunc(g.Members, func(held MemberID) bool { return slices.Contains(members, held) })
	default:
		g.Members = addMembers(nil, members...)
	}
	return nil
}

// memberRefs reads members as a list of {value} objects, or one such object.
func memberRefs(value any) ([]MemberID, error) {
	items, ok := value.([]any)
	if obj, isObj := value.(map[string]any); isObj {
		items, ok = []any{obj}, true
	}
	if !ok {
		return nil, scimErr("invalidValue", `members is a list of {"value": "<id of a User>"}`)
	}
	out := make([]MemberID, 0, len(items))
	for _, item := range items {
		obj, isObj := item.(map[string]any)
		raw, isString := obj["value"].(string)
		if !isObj || !isString {
			return nil, scimErr("invalidValue", `members is a list of {"value": "<id of a User>"}`)
		}
		m, err := groupMember(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// TouchedMembers returns the members a change of a group from before to
// after may give or take access: those who joined or left it, and every
// member before and after when it was renamed, since group mappings name
// groups by name. A change of the external ID touches none.
func TouchedMembers(before, after GroupState) []MemberID {
	if before.DisplayName != after.DisplayName {
		return addMembers(slices.Clone(before.Members), after.Members...)
	}
	var out []MemberID
	for _, m := range before.Members {
		if !slices.Contains(after.Members, m) {
			out = append(out, m)
		}
	}
	for _, m := range after.Members {
		if !slices.Contains(before.Members, m) {
			out = append(out, m)
		}
	}
	return out
}
