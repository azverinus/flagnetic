package workspace

import (
	"time"

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
		ID: environmentID, Project: project, Key: key, Name: f.name, SortOrder: sortOrder, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// UnitType is one declared kind of unit in a project, such as user.
type UnitType struct {
	ID          UnitTypeID
	Project     ProjectID
	Key         string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
		ID: unitTypeID, Project: project, Key: key, Name: f.name, Description: f.description, CreatedAt: now, UpdatedAt: now,
	}, nil
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
}

// AttributeID identifies an attribute.
type AttributeID = id.Of[Attribute]

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
			CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt,
		})
	}
	for _, u := range defaultUnitTypes {
		d.UnitTypes = append(d.UnitTypes, UnitType{
			ID: ids.UnitType(), Project: p.ID, Key: u.Key, Name: u.Name, Description: u.Description,
			CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt,
		})
	}
	for _, a := range standardAttributes {
		d.Attributes = append(d.Attributes, Attribute{
			ID: ids.Attribute(), Project: p.ID, Key: a.Key, Name: a.Name, Type: a.Type, Standard: true,
			CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt,
		})
	}
	return d
}
