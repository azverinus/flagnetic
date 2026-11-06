package identity

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	filter "github.com/scim2/filter-parser/v2"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// UserSchema names the SCIM User resource.
const UserSchema = "urn:ietf:params:scim:schemas:core:2.0:User"

// maxFilterLength bounds a filter, so the parser meets no input worth
// attacking.
const maxFilterLength = 1024

// UserAttribute is an attribute of a SCIM User a filter may compare.
type UserAttribute string

// The attributes a filter on Users compares.
const (
	// AttrEmail is userName and emails.value, which are one value: the
	// member's email, compared whatever its case.
	AttrEmail UserAttribute = "email"
	// AttrExternalID is externalId, compared exactly.
	AttrExternalID UserAttribute = "external_id"
	// AttrDisplayName is displayName, compared whatever its case.
	AttrDisplayName UserAttribute = "display_name"
	// AttrID is id, compared exactly.
	AttrID UserAttribute = "id"
)

// UserCondition is one equality of a filter.
type UserCondition struct {
	Attribute UserAttribute
	Value     string
}

// UserFilter selects Users by equalities that all hold: the filters the
// instance understands are `eq` on userName, emails.value, externalId,
// displayName and id, alone or joined by `and`.
type UserFilter struct {
	Conditions []UserCondition
}

// ScimError is a refusal a SCIM client reads by its scimType, which the
// adapter writes into the error document.
type ScimError struct {
	// Type is the RFC 7644 scimType: invalidFilter, invalidSyntax,
	// invalidValue, mutability, uniqueness or noTarget.
	Type   string
	Detail string
}

func (e *ScimError) Error() string { return e.Type + ": " + e.Detail }

func scimErr(kind, format string, args ...any) *ScimError {
	return &ScimError{Type: kind, Detail: fmt.Sprintf(format, args...)}
}

// ParseUserFilter returns the filter raw states; an empty one selects every
// User. Anything but `eq` on a known attribute, joined by `and`, is
// invalidFilter, naming what was not understood.
func ParseUserFilter(raw string) (UserFilter, error) {
	if strings.TrimSpace(raw) == "" {
		return UserFilter{}, nil
	}
	if len(raw) > maxFilterLength {
		return UserFilter{}, scimErr("invalidFilter", "a filter has at most %d characters", maxFilterLength)
	}
	expr, err := filter.ParseFilter([]byte(raw))
	if err != nil {
		return UserFilter{}, scimErr("invalidFilter", "the filter is not valid: %v", err)
	}
	var f UserFilter
	if err = collectConditions(expr, &f); err != nil {
		return UserFilter{}, err
	}
	return f, nil
}

func collectConditions(e filter.Expression, f *UserFilter) error {
	switch x := e.(type) {
	case *filter.LogicalExpression:
		if x.Operator != filter.AND {
			return scimErr("invalidFilter", "only `and` joins comparisons")
		}
		if err := collectConditions(x.Left, f); err != nil {
			return err
		}
		return collectConditions(x.Right, f)
	case *filter.AttributeExpression:
		return collectComparison(x, f)
	}
	return scimErr("invalidFilter", "only `eq` comparisons, joined by `and`, are understood")
}

func collectComparison(x *filter.AttributeExpression, f *UserFilter) error {
	if x.Operator != filter.EQ {
		return scimErr("invalidFilter", "the operator %q is not understood; only `eq` is", x.Operator)
	}
	value, ok := x.CompareValue.(string)
	if !ok {
		return scimErr("invalidFilter", "the value compared with %s is a string", x.AttributePath)
	}
	attr, ok := userAttribute(x.AttributePath)
	if !ok {
		return scimErr("invalidFilter", "the attribute %s cannot be filtered on; userName, emails.value, externalId, displayName and id can", x.AttributePath)
	}
	f.Conditions = append(f.Conditions, UserCondition{Attribute: attr, Value: unescape(value)})
	return nil
}

// unescape reads the JSON escapes of a filter's string, which the parser
// hands over as written: \" and \\ among them.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var out string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &out); err != nil {
		return s
	}
	return out
}

// userAttribute names the User attribute a path stands for; attribute names
// are case-insensitive, and the schema's URN may prefix them.
func userAttribute(p filter.AttributePath) (UserAttribute, bool) {
	if p.URIPrefix != nil && !strings.EqualFold(*p.URIPrefix, UserSchema) {
		return "", false
	}
	name, sub := strings.ToLower(p.AttributeName), strings.ToLower(p.SubAttributeName())
	switch {
	case name == "username" && sub == "":
		return AttrEmail, true
	case name == "emails" && sub == "value":
		return AttrEmail, true
	case name == "externalid" && sub == "":
		return AttrExternalID, true
	case name == "displayname" && sub == "":
		return AttrDisplayName, true
	case name == "id" && sub == "":
		return AttrID, true
	}
	return "", false
}

// Email is an element of a User's emails.
type Email struct {
	Value   string
	Primary bool
}

// UserAttributes are the writable attributes a request states of a User; a
// nil or empty one is not stated.
type UserAttributes struct {
	UserName    *string
	Emails      []Email
	DisplayName *string
	GivenName   *string
	FamilyName  *string
	ExternalID  *string
	Active      *bool
}

// EmailAddress returns the email the attributes state: userName, which wins
// over the emails when both are given and differ, else the primary email,
// else the first. False when none is stated.
func (a UserAttributes) EmailAddress() (string, bool) {
	if a.UserName != nil && strings.TrimSpace(*a.UserName) != "" {
		return strings.TrimSpace(*a.UserName), true
	}
	for _, e := range a.Emails {
		if e.Primary && strings.TrimSpace(e.Value) != "" {
			return strings.TrimSpace(e.Value), true
		}
	}
	for _, e := range a.Emails {
		if strings.TrimSpace(e.Value) != "" {
			return strings.TrimSpace(e.Value), true
		}
	}
	return "", false
}

// Name returns the display name the attributes state, as a member is named:
// displayName, else the given and family name, else email.
func (a UserAttributes) Name(email string) string {
	if a.DisplayName != nil && strings.TrimSpace(*a.DisplayName) != "" {
		return strings.TrimSpace(*a.DisplayName)
	}
	given, family := deref(a.GivenName), deref(a.FamilyName)
	if joined := strings.TrimSpace(given + " " + family); joined != "" {
		return joined
	}
	return email
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// PatchOperation is one operation of a SCIM PatchOp, as the request states
// it.
type PatchOperation struct {
	Op string
	// Path is "" when the operation names its attributes in Value.
	Path  string
	Value any
}

// UserPatch is what applying a PatchOp changes of a User; a nil field is left
// as it is.
type UserPatch struct {
	Email *string
	// DisplayName is the new name; RemoveDisplayName returns the member to the
	// name they get without one, their email.
	DisplayName       *string
	RemoveDisplayName bool
	// ExternalID is the new external ID; RemoveExternalID clears it.
	ExternalID       *string
	RemoveExternalID bool
	Active           *bool
}

// ApplyUserPatch folds the operations of a PatchOp into one change, in the
// order they are given, the later overriding the earlier. add and replace
// are alike for the single-valued attributes a member has. An operation with
// a path addresses one attribute; one without carries an object naming
// several, as Okta sends `active`. name is accepted and changes nothing: a
// member has one name, displayName. Anything else is refused: a read-only
// attribute (id, groups, meta) as mutability, one the instance does not map
// as noTarget, a value of the wrong shape as invalidValue.
func ApplyUserPatch(ops []PatchOperation) (UserPatch, error) {
	var p UserPatch
	if len(ops) == 0 {
		return p, scimErr("invalidValue", "a PatchOp has at least one operation")
	}
	for _, o := range ops {
		op := strings.ToLower(strings.TrimSpace(o.Op))
		if op != "add" && op != "replace" && op != "remove" {
			return p, scimErr("invalidSyntax", "the operation %q is none of add, replace and remove", o.Op)
		}
		if err := applyUserOperation(&p, op, o); err != nil {
			return p, err
		}
	}
	return p, nil
}

func applyUserOperation(p *UserPatch, op string, o PatchOperation) error {
	if strings.TrimSpace(o.Path) == "" {
		if op == "remove" {
			return scimErr("noTarget", "a remove operation names the attribute to remove in its path")
		}
		obj, ok := o.Value.(map[string]any)
		if !ok {
			return scimErr("invalidValue", "an operation without a path carries an object of attributes")
		}
		for name, v := range obj {
			if err := applyUserAttribute(p, op, name, v); err != nil {
				return err
			}
		}
		return nil
	}
	return applyUserAttribute(p, op, o.Path, o.Value)
}

func applyUserAttribute(p *UserPatch, op, path string, value any) error {
	parsed, err := filter.ParsePath([]byte(path))
	if err != nil {
		return scimErr("invalidSyntax", "the path %q is not valid: %v", path, err)
	}
	ap := parsed.AttributePath
	if ap.URIPrefix != nil && !strings.EqualFold(*ap.URIPrefix, UserSchema) {
		return scimErr("noTarget", "the path %q names an attribute of another schema", path)
	}
	sub := strings.ToLower(parsed.SubAttributeName())
	if sub == "" {
		sub = strings.ToLower(ap.SubAttributeName())
	}
	switch strings.ToLower(ap.AttributeName) {
	case "active":
		return patchActive(p, op, value)
	case "username":
		return patchEmail(p, op, "userName", value)
	case "emails":
		if sub != "" && sub != "value" {
			return scimErr("noTarget", "only the value of an email is mapped")
		}
		return patchEmail(p, op, "emails", value)
	case "displayname":
		return patchDisplayName(p, op, value)
	case "externalid":
		return patchExternalID(p, op, value)
	case "name":
		if op != "remove" {
			if _, isString := value.(string); !isString && sub != "" {
				return scimErr("invalidValue", "%s is a string", path)
			}
		}
		return nil
	case "id", "groups", "meta":
		return scimErr("mutability", "%s is read only", ap.AttributeName)
	}
	return scimErr("noTarget", "%s is not an attribute the instance maps", path)
}

func patchActive(p *UserPatch, op string, value any) error {
	if op == "remove" {
		return scimErr("invalidValue", "active cannot be removed")
	}
	switch v := value.(type) {
	case bool:
		p.Active = &v
	case string:
		// Entra ID sends booleans as the strings "True" and "False".
		switch strings.ToLower(v) {
		case "true":
			p.Active = new(true)
		case "false":
			p.Active = new(false)
		default:
			return scimErr("invalidValue", "active is a boolean")
		}
	default:
		return scimErr("invalidValue", "active is a boolean")
	}
	return nil
}

func patchEmail(p *UserPatch, op, name string, value any) error {
	if op == "remove" {
		return scimErr("invalidValue", "%s cannot be removed: it is the member's email", name)
	}
	email, ok := emailOf(value)
	if !ok {
		return scimErr("invalidValue", "%s is an email address", name)
	}
	p.Email = &email
	return nil
}

// emailOf reads an email from a plain string, or from an array of
// {value, primary} objects (or one such object): emails is patched either
// way, depending on the path a client sends.
func emailOf(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		s := strings.TrimSpace(v)
		return s, s != ""
	case map[string]any:
		s, ok := v["value"].(string)
		s = strings.TrimSpace(s)
		return s, ok && s != ""
	case []any:
		var first string
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			s, _ := obj["value"].(string)
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			if primary, _ := obj["primary"].(bool); primary {
				return s, true
			}
			if first == "" {
				first = s
			}
		}
		return first, first != ""
	}
	return "", false
}

func patchDisplayName(p *UserPatch, op string, value any) error {
	if op == "remove" {
		p.DisplayName, p.RemoveDisplayName = nil, true
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return scimErr("invalidValue", "displayName is a string")
	}
	p.DisplayName, p.RemoveDisplayName = &s, false
	return nil
}

func patchExternalID(p *UserPatch, op string, value any) error {
	if op == "remove" || value == nil {
		p.ExternalID, p.RemoveExternalID = nil, true
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return scimErr("invalidValue", "externalId is a string")
	}
	p.ExternalID, p.RemoveExternalID = &s, false
	return nil
}

// Bounds of a provisioned member's external ID.
const maxExternalIDLength = 256

// CheckExternalID refuses an external ID that is blank or too long; "" is
// none.
func CheckExternalID(externalID string) error {
	if externalID != strings.TrimSpace(externalID) || utf8.RuneCountInString(externalID) > maxExternalIDLength {
		return scimErr("invalidValue", "externalId has at most %d characters and no space around it", maxExternalIDLength)
	}
	return nil
}

// ErrUserNotFound answers a SCIM User the token's provider does not
// provision, whether it exists elsewhere or not.
var ErrUserNotFound = apperrors.NotFound("not_found", "no member this provider provisions has this id")
