// Package operations reads what each operation of the management API
// requires from its OpenAPI document, and holds the table generated from it
// that the permission seam reads.
package operations

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// whoAmI is the route of the one operation outside a project that a project
// token reaches: reading which token it is.
const whoAmI = "GET /me"

// methods are the operation keys of an OpenAPI path item, in the order the
// table lists them.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Entry is what the management API needs to know of one operation before
// its handler runs: what it requires of the caller, and how its request is
// read.
type Entry struct {
	access.Operation
	// OpensSession operations (x-opens-session) accept only a JSON body: they
	// are public, so no session guards them against another site, and a form
	// on another site cannot send JSON without the browser asking first.
	OpensSession bool
	// ChecksPassword operations (x-checks-password) accept a body of at most
	// PasswordBodyLimit bytes, so a huge one is refused before anything,
	// let alone a hash, is spent on it.
	ChecksPassword bool
	// MaxBodyBytes (x-max-body-bytes), when not 0, is the operation's own
	// body limit, for the few that take bodies larger than the server's
	// limit allows, such as a long list of unit IDs.
	MaxBodyBytes int64
}

// PasswordBodyLimit caps the bodies of the operations that check a
// password, which hold an email and passwords at most 128 characters long.
const PasswordBodyLimit = 8 << 10

// Read reads what each operation of an OpenAPI document requires,
// keyed by its route pattern, such as "GET /me". It refuses an operation
// without an identifier, without x-permission or naming a permission the
// catalog does not hold, so no operation is ever open by accident.
func Read(doc []byte) (map[string]Entry, error) {
	var d struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("reading the document: %w", err)
	}
	ops := map[string]Entry{}
	var errs []error
	for path, item := range d.Paths {
		for method, node := range item {
			if !slices.Contains(methods, method) {
				continue // parameters, summary and the like
			}
			var o struct {
				OperationID     string `yaml:"operationId"`
				Permission      string `yaml:"x-permission"`
				SessionRequired bool   `yaml:"x-session-required"`
				OpensSession    bool   `yaml:"x-opens-session"`
				ChecksPassword  bool   `yaml:"x-checks-password"`
				MaxBodyBytes    int64  `yaml:"x-max-body-bytes"`
			}
			if err := node.Decode(&o); err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", method, path, err))
				continue
			}
			where := strings.ToUpper(method) + " " + path
			switch {
			case o.OperationID == "":
				errs = append(errs, fmt.Errorf("%s: no operationId", where))
				continue
			case o.Permission == "":
				errs = append(errs, fmt.Errorf("%s (%s): no x-permission", where, o.OperationID))
				continue
			case !access.IsRequirement(access.Requirement(o.Permission)):
				errs = append(errs, fmt.Errorf("%s (%s): x-permission %q is neither a permission of the catalog nor public, member or instance_admin",
					where, o.OperationID, o.Permission))
				continue
			}
			read := method == "get" || method == "head"
			requirement := access.Requirement(o.Permission)
			ops[where] = Entry{
				Operation: access.Operation{
					Name: o.OperationID, Requirement: requirement, SessionRequired: o.SessionRequired,
					Read: read, Scope: access.ScopeOf(requirement, read), OpenToProjectTokens: where == whoAmI,
				},
				OpensSession: o.OpensSession, ChecksPassword: o.ChecksPassword, MaxBodyBytes: o.MaxBodyBytes,
			}
		}
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
		return nil, errors.Join(errs...)
	}
	return ops, nil
}

// render returns the Go source of the operation table.
func render(ops map[string]Entry) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`// Code generated from api/management.yaml by ` + "`task generate`" + `. DO NOT EDIT.

package operations

import "github.com/azverinus/flagnetic/internal/domain/access"

// Table is what each operation requires, keyed by its route pattern.
var Table = map[string]Entry{
`)
	patterns := make([]string, 0, len(ops))
	for p := range ops {
		patterns = append(patterns, p)
	}
	slices.Sort(patterns)
	for _, p := range patterns {
		o := ops[p]
		fmt.Fprintf(&b, "\t%q: {Operation: access.Operation{Name: %q, Requirement: %q, SessionRequired: %t, Read: %t, Scope: %q, OpenToProjectTokens: %t}, OpensSession: %t, ChecksPassword: %t, MaxBodyBytes: %d},\n",
			p, o.Name, string(o.Requirement), o.SessionRequired, o.Read, string(o.Scope), o.OpenToProjectTokens, o.OpensSession, o.ChecksPassword, o.MaxBodyBytes)
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}
