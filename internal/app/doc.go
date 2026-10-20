// Package app is the root of the application layer: one subpackage per
// bounded context, holding its command and query handlers.
//
// A handler is a struct with exactly the dependencies its use case needs and
// one method that runs it. It declares the ports it uses as small interfaces
// next to itself, owns the transaction boundary of a command, and maps its own
// command and result types to and from domain models. It imports domain and
// shared, never infra, a database driver or net/http.
package app
