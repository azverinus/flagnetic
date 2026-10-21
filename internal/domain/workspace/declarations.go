package workspace

import (
	"slices"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Environment is one of a project's environments, such as production.
type Environment struct {
	ID      EnvironmentID
	Project ProjectID
	// Key names the environment in SDK calls and never changes.
	Key  string
	Name string
	// SortOrder places the environment among its project's, the first
	// created first.
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts the environment's accepted changes, from 1.
	Version int64
}

// EnvironmentID identifies an environment.
type EnvironmentID = id.Of[Environment]

// NewEnvironment returns an environment of project placed at sortOrder.
func NewEnvironment(environmentID EnvironmentID, project ProjectID, key, name string, sortOrder int, now time.Time) (Environment, error) {
	f := check("invalid_environment", "the environment is not valid").key(key).nameOf(name)
	if err := f.err(); err != nil {
		return Environment{}, err
	}
	return Environment{
		ID: environmentID, Project: project, Key: key, Name: f.name, SortOrder: sortOrder, CreatedAt: now, UpdatedAt: now, Version: 1,
	}, nil
}

// EnvironmentChange is a change to an environment; a nil field is left as
// it is.
type EnvironmentChange struct {
	// Key is refused whatever its value, since a key never changes.
	Key  *string
	Name *string
}

// Change returns the environment with c applied at now, and whether anything
// changed.
func (e Environment) Change(c EnvironmentChange, now time.Time) (Environment, bool, error) {
	if c.Key != nil {
		return e, false, errKeyImmutable
	}
	if c.Name == nil {
		return e, false, nil
	}
	f := check("invalid_environment", "the environment is not valid").nameOf(*c.Name)
	if err := f.err(); err != nil {
		return e, false, err
	}
	if f.name == e.Name {
		return e, false, nil
	}
	out := e
	out.Name, out.UpdatedAt, out.Version = f.name, now, e.Version+1
	return out, true, nil
}

// CanDeleteEnvironment refuses to delete one of a project's count
// environments when it is the last: a project always has somewhere to serve
// its flags from.
func CanDeleteEnvironment(count int) error {
	if count <= 1 {
		return apperrors.Conflict("last_environment", "the last environment of a project cannot be deleted")
	}
	return nil
}

// ErrDeclarationKeyTaken refuses a unit type or attribute whose key the
// project's unit types or attributes already use: a rule names either by its
// key alone, so one key must mean one thing.
var ErrDeclarationKeyTaken = apperrors.Conflict("already_exists", "a unit type or attribute of the project already has this key")

// UnitType is one declared kind of unit in a project, such as user.
type UnitType struct {
	ID          UnitTypeID
	Project     ProjectID
	Key         string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// Version counts the unit type's accepted changes, from 1.
	Version int64
}

// UnitTypeID identifies a unit type.
type UnitTypeID = id.Of[UnitType]

// NewUnitType returns a unit type of project.
func NewUnitType(unitTypeID UnitTypeID, project ProjectID, key, name, description string, now time.Time) (UnitType, error) {
	f := check("invalid_unit_type", "the unit type is not valid").key(key).nameOf(name).descriptionOf(description)
	if err := f.err(); err != nil {
		return UnitType{}, err
	}
	return UnitType{
		ID: unitTypeID, Project: project, Key: key, Name: f.name, Description: f.description, CreatedAt: now, UpdatedAt: now, Version: 1,
	}, nil
}

// UnitTypeChange is a change to a unit type; a nil field is left as it is.
type UnitTypeChange struct {
	// Key is refused whatever its value, since a key never changes.
	Key         *string
	Name        *string
	Description *string
}

// Change returns the unit type with c applied at now, and whether anything
// changed.
func (u UnitType) Change(c UnitTypeChange, now time.Time) (UnitType, bool, error) {
	if c.Key != nil {
		return u, false, errKeyImmutable
	}
	f := check("invalid_unit_type", "the unit type is not valid").
		nameOf(valueOr(c.Name, u.Name)).descriptionOf(valueOr(c.Description, u.Description))
	if err := f.err(); err != nil {
		return u, false, err
	}
	if f.name == u.Name && f.description == u.Description {
		return u, false, nil
	}
	out := u
	out.Name, out.Description, out.UpdatedAt, out.Version = f.name, f.description, now, u.Version+1
	return out, true, nil
}

func valueOr(v *string, otherwise string) string {
	if v == nil {
		return otherwise
	}
	return *v
}

// AttributeType is the type of an attribute's values.
type AttributeType string

// The attribute types.
const (
	TypeString  AttributeType = "string"
	TypeNumber  AttributeType = "number"
	TypeBoolean AttributeType = "boolean"
	// TypeSemver is a semantic version, compared as one.
	TypeSemver AttributeType = "semver"
	TypeDate   AttributeType = "date"
	TypeList   AttributeType = "list"
)

// AttributeTypes returns every attribute type.
func AttributeTypes() []AttributeType {
	return []AttributeType{TypeString, TypeNumber, TypeBoolean, TypeSemver, TypeDate, TypeList}
}

// Attribute is one named, typed value of a context a project declares.
type Attribute struct {
	ID          AttributeID
	Project     ProjectID
	Key         string
	Name        string
	Description string
	Type        AttributeType
	// Standard attributes are predeclared in every project, so SDKs can rely
	// on them.
	Standard  bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// Version counts the attribute's accepted changes, from 1.
	Version int64
}

// AttributeID identifies an attribute.
type AttributeID = id.Of[Attribute]

// reservedAttributeKey is the key no attribute may have: the management API
// serves the attributes SDKs report without a declaration under it.
const reservedAttributeKey = "discovered"

// NewAttribute returns an attribute of project, declared rather than
// standard.
func NewAttribute(attributeID AttributeID, project ProjectID, key, name, description string, typ AttributeType, now time.Time) (Attribute, error) {
	f := check("invalid_attribute", "the attribute is not valid")
	if key == reservedAttributeKey {
		f.violations = append(f.violations, apperrors.Violation{
			Pointer: "/key", Code: "reserved_key", Detail: "the key discovered is reserved",
			Params: map[string]any{"key": reservedAttributeKey},
		})
	} else {
		f.key(key)
	}
	f.nameOf(name).descriptionOf(description)
	if !slices.Contains(AttributeTypes(), typ) {
		types := make([]string, 0, len(AttributeTypes()))
		for _, t := range AttributeTypes() {
			types = append(types, string(t))
		}
		f.violations = append(f.violations, apperrors.Violation{
			Pointer: "/type", Code: "invalid_type", Detail: "the type is not one an attribute can have",
			Params: map[string]any{"types": types},
		})
	}
	if err := f.err(); err != nil {
		return Attribute{}, err
	}
	return Attribute{
		ID: attributeID, Project: project, Key: key, Name: f.name, Description: f.description, Type: typ,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}, nil
}

// AttributeChange is a change to an attribute; a nil field is left as it
// is.
type AttributeChange struct {
	// Key and Type are refused whatever their value: rules written against
	// the attribute rely on both.
	Key         *string
	Type        *AttributeType
	Name        *string
	Description *string
}

// errTypeImmutable refuses a change to an attribute's type, which would
// change the meaning of every rule written against it.
var errTypeImmutable = apperrors.Invalid("type_immutable", "an attribute's type never changes", apperrors.Violation{
	Pointer: "/type", Code: "type_immutable", Detail: "an attribute's type never changes; declare a new attribute instead",
})

// Change returns the attribute with c applied at now, and whether anything
// changed. A standard attribute is renamed like any other and stays
// standard.
func (a Attribute) Change(c AttributeChange, now time.Time) (Attribute, bool, error) {
	if c.Key != nil {
		return a, false, errKeyImmutable
	}
	if c.Type != nil {
		return a, false, errTypeImmutable
	}
	f := check("invalid_attribute", "the attribute is not valid").
		nameOf(valueOr(c.Name, a.Name)).descriptionOf(valueOr(c.Description, a.Description))
	if err := f.err(); err != nil {
		return a, false, err
	}
	if f.name == a.Name && f.description == a.Description {
		return a, false, nil
	}
	out := a
	out.Name, out.Description, out.UpdatedAt, out.Version = f.name, f.description, now, a.Version+1
	return out, true, nil
}

// CanDelete refuses to delete a standard attribute, which SDKs rely on
// finding in every project.
func (a Attribute) CanDelete() error {
	if a.Standard {
		return apperrors.Conflict("standard_attribute", "a standard attribute cannot be deleted")
	}
	return nil
}

// Draft is what a project is created with besides itself: the key and name
// of a default environment, unit type or standard attribute, and its
// description or type where it has one.
type Draft struct {
	Key, Name, Description string
	Type                   AttributeType
}

// defaultEnvironments are created with every project, in this order: the
// path a change usually takes to production.
var defaultEnvironments = []Draft{
	{Key: "development", Name: "Development"},
	{Key: "staging", Name: "Staging"},
	{Key: "production", Name: "Production"},
}

// defaultUnitTypes are created with every project: the two kinds of unit
// nearly every product evaluates flags for.
var defaultUnitTypes = []Draft{
	{Key: "user", Name: "User", Description: "A signed-in user."},
	{Key: "anonymous", Name: "Anonymous", Description: "A visitor or device before sign-in."},
}

// standardAttributes are predeclared in every project with the types SDKs
// send them as.
var standardAttributes = []Draft{
	{Key: "platform", Name: "Platform", Type: TypeString},
	{Key: "os", Name: "OS", Type: TypeString},
	{Key: "app_id", Name: "App ID", Type: TypeString},
	{Key: "sdk", Name: "SDK", Type: TypeString},
	{Key: "locale", Name: "Locale", Type: TypeString},
	{Key: "country", Name: "Country", Type: TypeString},
	{Key: "os_version", Name: "OS version", Type: TypeSemver},
	{Key: "app_version", Name: "App version", Type: TypeSemver},
	{Key: "sdk_version", Name: "SDK version", Type: TypeSemver},
	{Key: "build_number", Name: "Build number", Type: TypeNumber},
}

// ProjectDefaults is what a new project is created with.
type ProjectDefaults struct {
	Environments []Environment
	UnitTypes    []UnitType
	Attributes   []Attribute
}

// IDs mints the identifiers of a new project's defaults.
type IDs interface {
	Environment() EnvironmentID
	UnitType() UnitTypeID
	Attribute() AttributeID
}

// DefaultsOf returns what p is created with, at its creation time:
// development, staging and production environments in that order, the user
// and anonymous unit types, and the standard attributes.
func DefaultsOf(p Project, ids IDs) ProjectDefaults {
	var d ProjectDefaults
	for i, e := range defaultEnvironments {
		d.Environments = append(d.Environments, Environment{
			ID: ids.Environment(), Project: p.ID, Key: e.Key, Name: e.Name, SortOrder: i + 1,
			CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt, Version: 1,
		})
	}
	for _, u := range defaultUnitTypes {
		d.UnitTypes = append(d.UnitTypes, UnitType{
			ID: ids.UnitType(), Project: p.ID, Key: u.Key, Name: u.Name, Description: u.Description,
			CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt, Version: 1,
		})
	}
	for _, a := range standardAttributes {
		d.Attributes = append(d.Attributes, Attribute{
			ID: ids.Attribute(), Project: p.ID, Key: a.Key, Name: a.Name, Type: a.Type, Standard: true,
			CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt, Version: 1,
		})
	}
	return d
}
