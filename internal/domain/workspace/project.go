package workspace

import (
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ProjectID identifies a project.
type ProjectID = id.Of[Project]

// Project is a project: the flags, segments, experiments and environments of
// one product, and the grants of the members working on it.
type Project struct {
	ID ProjectID
	// Key names the project in URLs and SDK calls, and never changes.
	Key         string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewProject returns a project with key, name and description, created at
// now.
func NewProject(projectID ProjectID, key, name, description string, now time.Time) (Project, error) {
	f := check("invalid_project", "the project is not valid").key(key).nameOf(name).descriptionOf(description)
	if err := f.err(); err != nil {
		return Project{}, err
	}
	return Project{ID: projectID, Key: key, Name: f.name, Description: f.description, CreatedAt: now, UpdatedAt: now}, nil
}

// ProjectChange is a change to a project; a nil field is left as it is.
type ProjectChange struct {
	// Key is refused whatever its value, since a key never changes.
	Key         *string
	Name        *string
	Description *string
}

// Change returns the project with c applied at now, and whether anything
// changed: a change to the values it already has is none.
func (p Project) Change(c ProjectChange, now time.Time) (Project, bool, error) {
	if c.Key != nil {
		return p, false, errKeyImmutable
	}
	f := check("invalid_project", "the project is not valid").
		nameOf(valueOr(c.Name, p.Name)).descriptionOf(valueOr(c.Description, p.Description))
	if err := f.err(); err != nil {
		return p, false, err
	}
	if f.name == p.Name && f.description == p.Description {
		return p, false, nil
	}
	out := p
	out.Name, out.Description, out.UpdatedAt = f.name, f.description, now
	return out, true, nil
}
