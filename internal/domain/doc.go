// Package domain is the root of the domain layer: one subpackage per bounded
// context, holding its entities, value objects and invariants.
//
// Domain code is plain Go. It imports only the standard library, the eval
// module and internal/shared; never a database driver, net/http or another
// layer. It performs no I/O and reads no clock: callers pass time and IDs in.
// The linter enforces these rules.
package domain
