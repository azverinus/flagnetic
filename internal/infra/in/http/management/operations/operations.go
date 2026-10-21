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

// methods are the operation keys of an OpenAPI path item, in the order the
// table lists them.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Read reads what each operation of an OpenAPI document requires,
// keyed by its route pattern, such as "GET /me". It refuses an operation
// without an identifier, without x-permission or naming a permission the
// catalog does not hold, so no operation is ever open by accident.
func Read(doc []byte) (map[string]access.Operation, error) {
	var d struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("reading the document: %w", err)
	}
	ops := map[string]access.Operation{}
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
			case !access.IsRequirement(o.Permission):
				errs = append(errs, fmt.Errorf("%s (%s): x-permission %q is neither a permission of the catalog nor public, member or instance_admin",
					where, o.OperationID, o.Permission))
				continue
			}
			read := method == "get" || method == "head"
			ops[where] = access.Operation{
				Name: o.OperationID, Requirement: o.Permission, SessionRequired: o.SessionRequired,
				Read: read, Scope: access.ScopeOf(o.Permission, read),
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
func render(ops map[string]access.Operation) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`// Code generated from api/management.yaml by ` + "`task generate`" + `. DO NOT EDIT.

package operations

import "github.com/azverinus/flagnetic/internal/domain/access"

// Table is what each operation requires, keyed by its route pattern.
var Table = map[string]access.Operation{
`)
	patterns := make([]string, 0, len(ops))
	for p := range ops {
		patterns = append(patterns, p)
	}
	slices.Sort(patterns)
	for _, p := range patterns {
		o := ops[p]
		fmt.Fprintf(&b, "\t%q: {Name: %q, Requirement: %q, SessionRequired: %t, Read: %t, Scope: %q},\n",
			p, o.Name, o.Requirement, o.SessionRequired, o.Read, string(o.Scope))
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}
