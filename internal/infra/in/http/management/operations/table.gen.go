// Code generated from api/management.yaml by `task generate`. DO NOT EDIT.

package operations

import "github.com/azverinus/flagnetic/internal/domain/access"

// Table is what each operation requires, keyed by its route pattern.
var Table = map[string]access.Operation{
	"GET /me": {Name: "getMe", Requirement: "member", SessionRequired: false, Read: true, Scope: ""},
}
