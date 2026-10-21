// Package workspace is the domain of where flags live: projects and their
// environments. Other contexts refer to a project or an environment by the
// identifier types declared here, so every context means the same entity by
// the same type.
package workspace

import "github.com/azverinus/flagnetic/internal/shared/id"

// Project is a project: the flags, segments, experiments and environments of
// one product, and the grants of the members working on it.
type Project struct{}

// ProjectID identifies a project.
type ProjectID = id.Of[Project]

// Environment is one of a project's environments, such as production.
type Environment struct{}

// EnvironmentID identifies an environment.
type EnvironmentID = id.Of[Environment]
