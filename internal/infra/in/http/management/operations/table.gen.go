// Code generated from api/management.yaml by `task generate`. DO NOT EDIT.

package operations

import "github.com/azverinus/flagnetic/internal/domain/access"

// Table is what each operation requires, keyed by its route pattern.
var Table = map[string]access.Operation{
	"GET /me":                    {Name: "getMe", Requirement: "member", SessionRequired: false, Read: true, Scope: "", OpenToProjectTokens: true},
	"GET /projects":              {Name: "listProjects", Requirement: "member", SessionRequired: false, Read: true, Scope: "", OpenToProjectTokens: false},
	"GET /projects/{project}":    {Name: "getProject", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/me": {Name: "getProjectMe", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /roles":                 {Name: "listRoles", Requirement: "member", SessionRequired: false, Read: true, Scope: "", OpenToProjectTokens: false},
	"PATCH /projects/{project}":  {Name: "updateProject", Requirement: "project.update", SessionRequired: false, Read: false, Scope: "project:admin", OpenToProjectTokens: false},
	"POST /projects":             {Name: "createProject", Requirement: "instance_admin", SessionRequired: false, Read: false, Scope: "instance:admin", OpenToProjectTokens: false},
}
