package identity

import (
	"slices"
	"strconv"
	"strings"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// GroupMappingInput is a group mapping as a client states it: a directory
// group, and either a role in the project with the key Project or instance
// admin, never both.
type GroupMappingInput struct {
	Group         string
	Project       string
	Role          string
	InstanceAdmin bool
}

// GroupMapping is a stored group mapping: members of the directory group Group
// hold Role in Project, or are instance admins.
type GroupMapping struct {
	Group string
	// Project and Role are zero for a mapping that makes instance admins.
	Project workspace.ProjectID
	// ProjectKey is the key of Project, for the clients that read mappings.
	ProjectKey    string
	Role          string
	InstanceAdmin bool
}

// CheckGroupMappings returns in as it is when every mapping is well formed,
// and otherwise invalid_group_mappings naming each field to fix: a group that
// is blank; a mapping that is neither a role in a project nor instance admin,
// or both, or a project without a role or a role without a project; a role
// knownRole does not accept. That the projects exist is for the caller to
// judge.
func CheckGroupMappings(in []GroupMappingInput, knownRole func(role string) bool) ([]GroupMappingInput, error) {
	var v violations
	for i, m := range in {
		at := "/items/" + strconv.Itoa(i)
		if strings.TrimSpace(m.Group) == "" {
			v.add(at+"/group", "blank", "the group must not be blank", nil)
		}
		switch {
		case m.InstanceAdmin && (m.Project != "" || m.Role != ""):
			v.add(at+"/instance_admin", "exclusive", "instance admin stands alone: a mapping grants it or a role in a project, not both", nil)
		case m.InstanceAdmin:
		case m.Project == "" && m.Role == "":
			v.add(at+"/role", "required", "a mapping grants instance admin or a role in a project", nil)
		case m.Project == "":
			v.add(at+"/project", "required", "a role is granted in a project", nil)
		case m.Role == "":
			v.add(at+"/role", "required", "a project is granted a role", nil)
		case !knownRole(m.Role):
			v.add(at+"/role", "unknown_role", "the role is none of owner, maintainer, developer and viewer", nil)
		}
	}
	if len(v) > 0 {
		return nil, apperrors.Invalid("invalid_group_mappings", "the group mappings break a rule; the violations name each field", v...)
	}
	return in, nil
}

// ProjectRole is a role held in a project.
type ProjectRole struct {
	Project workspace.ProjectID
	Role    string
}

// Entitlement is what a person's groups map to at one provider.
type Entitlement struct {
	// Grants are the roles in projects, each once, in the order of the
	// mappings that name them.
	Grants        []ProjectRole
	InstanceAdmin bool
}

// Entitle evaluates mappings for a person in groups: the union of what every
// mapping whose group the person is in grants. Groups match exactly, case
// included, as a directory names them.
func Entitle(mappings []GroupMapping, groups []string) Entitlement {
	var out Entitlement
	for _, m := range mappings {
		if !slices.Contains(groups, m.Group) {
			continue
		}
		if m.InstanceAdmin {
			out.InstanceAdmin = true
			continue
		}
		grant := ProjectRole{Project: m.Project, Role: m.Role}
		if !slices.Contains(out.Grants, grant) {
			out.Grants = append(out.Grants, grant)
		}
	}
	return out
}

// ParseGroupsClaim reads a groups claim as the identity provider sent it: a
// list of strings, empty or not. Anything else (an absent claim, a single
// string, a list holding another type) is not a groups claim, and ok is false.
func ParseGroupsClaim(claim any) (groups []string, ok bool) {
	switch list := claim.(type) {
	case []string:
		return append([]string{}, list...), true
	case []any:
		groups = make([]string, 0, len(list))
		for _, item := range list {
			s, isString := item.(string)
			if !isString {
				return nil, false
			}
			groups = append(groups, s)
		}
		return groups, true
	}
	return nil, false
}
