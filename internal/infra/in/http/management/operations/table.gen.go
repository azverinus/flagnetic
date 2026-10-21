// Code generated from api/management.yaml by `task generate`. DO NOT EDIT.

package operations

import "github.com/azverinus/flagnetic/internal/domain/access"

// Table is what each operation requires, keyed by its route pattern.
var Table = map[string]access.Operation{
	"DELETE /projects/{project}/attributes/{attribute}":     {Name: "deleteAttribute", Requirement: "declaration.manage", SessionRequired: false, Read: false, Scope: "flags:write", OpenToProjectTokens: false},
	"DELETE /projects/{project}/environments/{environment}": {Name: "deleteEnvironment", Requirement: "environment.manage", SessionRequired: false, Read: false, Scope: "project:admin", OpenToProjectTokens: false},
	"DELETE /projects/{project}/unit-types/{unit_type}":     {Name: "deleteUnitType", Requirement: "declaration.manage", SessionRequired: false, Read: false, Scope: "flags:write", OpenToProjectTokens: false},
	"GET /me":                            {Name: "getMe", Requirement: "member", SessionRequired: false, Read: true, Scope: "", OpenToProjectTokens: true},
	"GET /projects":                      {Name: "listProjects", Requirement: "member", SessionRequired: false, Read: true, Scope: "", OpenToProjectTokens: false},
	"GET /projects/{project}":            {Name: "getProject", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/attributes": {Name: "listAttributes", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/attributes/{attribute}":     {Name: "getAttribute", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/environments":               {Name: "listEnvironments", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/environments/{environment}": {Name: "getEnvironment", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/me":                         {Name: "getProjectMe", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/unit-types":                 {Name: "listUnitTypes", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /projects/{project}/unit-types/{unit_type}":     {Name: "getUnitType", Requirement: "project.read", SessionRequired: false, Read: true, Scope: "read", OpenToProjectTokens: false},
	"GET /roles":                {Name: "listRoles", Requirement: "member", SessionRequired: false, Read: true, Scope: "", OpenToProjectTokens: false},
	"PATCH /projects/{project}": {Name: "updateProject", Requirement: "project.update", SessionRequired: false, Read: false, Scope: "project:admin", OpenToProjectTokens: false},
	"PATCH /projects/{project}/attributes/{attribute}":     {Name: "updateAttribute", Requirement: "declaration.manage", SessionRequired: false, Read: false, Scope: "flags:write", OpenToProjectTokens: false},
	"PATCH /projects/{project}/environments/{environment}": {Name: "updateEnvironment", Requirement: "environment.manage", SessionRequired: false, Read: false, Scope: "project:admin", OpenToProjectTokens: false},
	"PATCH /projects/{project}/unit-types/{unit_type}":     {Name: "updateUnitType", Requirement: "declaration.manage", SessionRequired: false, Read: false, Scope: "flags:write", OpenToProjectTokens: false},
	"POST /projects":                        {Name: "createProject", Requirement: "instance_admin", SessionRequired: false, Read: false, Scope: "instance:admin", OpenToProjectTokens: false},
	"POST /projects/{project}/attributes":   {Name: "createAttribute", Requirement: "declaration.manage", SessionRequired: false, Read: false, Scope: "flags:write", OpenToProjectTokens: false},
	"POST /projects/{project}/environments": {Name: "createEnvironment", Requirement: "environment.manage", SessionRequired: false, Read: false, Scope: "project:admin", OpenToProjectTokens: false},
	"POST /projects/{project}/unit-types":   {Name: "createUnitType", Requirement: "declaration.manage", SessionRequired: false, Read: false, Scope: "flags:write", OpenToProjectTokens: false},
}
