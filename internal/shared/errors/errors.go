// Package errors is the one way domain and application code says what went
// wrong: an error of a fixed kind with a stable code. Inbound adapters map the
// kind to their protocol in one place (HTTP answers a problem document), so no
// handler invents a status of its own and every API answers an error alike.
//
// An error that carries no kind is an unexpected failure: adapters answer it as
// internal, with no detail, and log its cause.
package errors

import (
	stderrors "errors"
	"maps"
	"slices"
	"time"
)

// Kind is the class of an error, the part an adapter maps to a status.
type Kind int

// The kinds. The zero value is no kind, so a forgotten field is never mistaken
// for one of them.
const (
	_ Kind = iota
	// KindInvalid: the input breaks a rule; field violations say where.
	KindInvalid
	// KindNotFound: the entity does not exist, or the caller may not know it does.
	KindNotFound
	// KindConflict: the change contradicts the current state, such as a key already taken.
	KindConflict
	// KindForbidden: the caller is known but may not do this.
	KindForbidden
	// KindUnauthenticated: the caller is not known.
	KindUnauthenticated
	// KindPreconditionFailed: the caller's view of the entity is out of date.
	KindPreconditionFailed
	// KindGone: the entity existed and no longer can be used, such as an expired invitation.
	KindGone
	// KindRateLimited: too many attempts; the error carries how long to wait.
	KindRateLimited
	// KindUnavailable: a dependency failed in a way worth retrying later.
	KindUnavailable
)

var kindNames = [...]string{
	KindInvalid:            "invalid",
	KindNotFound:           "not_found",
	KindConflict:           "conflict",
	KindForbidden:          "forbidden",
	KindUnauthenticated:    "unauthenticated",
	KindPreconditionFailed: "precondition_failed",
	KindGone:               "gone",
	KindRateLimited:        "rate_limited",
	KindUnavailable:        "unavailable",
}

// String returns the kind's snake_case name, or "none" for the zero value.
func (k Kind) String() string {
	if k > 0 && int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "none"
}

// Violation is one field of a request that breaks a rule.
type Violation struct {
	// Pointer is a JSON Pointer (RFC 6901) into the request body, such as
	// "/variations/1/value".
	Pointer string
	// Code is the stable snake_case name of the rule broken.
	Code string
	// Detail explains the violation to a human.
	Detail string
	// Params are the values of the rule broken, such as the bounds of a
	// length, so a client explains the violation in its own words and
	// language from the code and these; nil when the rule has none.
	Params map[string]any
}

// Error is an error of a kind. Its fields other than the cause are what a
// client is told; the cause is for the log alone.
type Error struct {
	Kind Kind
	// Code is stable and snake_case: clients branch on it, so it never changes
	// once released.
	Code string
	// Detail explains this occurrence to a human.
	Detail string
	// Violations are the fields that break a rule, for KindInvalid.
	Violations []Violation
	// Extensions are code-specific members a client may read, such as the
	// references that block a deletion.
	Extensions map[string]any
	// RetryAfter is how long to wait before retrying, for KindRateLimited
	// and, when known, KindUnavailable.
	RetryAfter time.Duration

	cause error
}

func newError(kind Kind, code, detail string) *Error {
	return &Error{Kind: kind, Code: code, Detail: detail}
}

// Invalid reports input that breaks a rule, with the fields that break it.
func Invalid(code, detail string, violations ...Violation) *Error {
	e := newError(KindInvalid, code, detail)
	e.Violations = violations
	return e
}

// NotFound reports an entity that does not exist or that the caller may not know of.
func NotFound(code, detail string) *Error { return newError(KindNotFound, code, detail) }

// Conflict reports a change that contradicts the current state.
func Conflict(code, detail string) *Error { return newError(KindConflict, code, detail) }

// Forbidden reports a known caller that may not do what it asked.
func Forbidden(code, detail string) *Error { return newError(KindForbidden, code, detail) }

// Unauthenticated reports a caller that is not known.
func Unauthenticated(code, detail string) *Error {
	return newError(KindUnauthenticated, code, detail)
}

// PreconditionFailed reports a write based on an out-of-date view of the entity.
func PreconditionFailed(code, detail string) *Error {
	return newError(KindPreconditionFailed, code, detail)
}

// Gone reports an entity that existed and can no longer be used.
func Gone(code, detail string) *Error { return newError(KindGone, code, detail) }

// RateLimited reports too many attempts, and how long to wait before the next.
func RateLimited(code, detail string, retryAfter time.Duration) *Error {
	e := newError(KindRateLimited, code, detail)
	e.RetryAfter = retryAfter
	return e
}

// Unavailable reports a dependency failure worth retrying later.
func Unavailable(code, detail string) *Error { return newError(KindUnavailable, code, detail) }

// WithRetryAfter returns a copy of e saying how long to wait before
// retrying, for an unavailable error whose wait is known, such as a busy
// resource.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	c := e.clone()
	c.RetryAfter = d
	return c
}

// clone copies e deeply enough that changing the copy never changes e: errors
// are often package-level values reused by every caller.
func (e *Error) clone() *Error {
	c := *e
	c.Violations = slices.Clone(e.Violations)
	c.Extensions = maps.Clone(e.Extensions)
	return &c
}

// WithCause returns a copy of e wrapping cause, which is logged and never sent
// to a client.
func (e *Error) WithCause(cause error) *Error {
	c := e.clone()
	c.cause = cause
	return c
}

// WithExtension returns a copy of e with one more extension member.
func (e *Error) WithExtension(key string, value any) *Error {
	c := e.clone()
	if c.Extensions == nil {
		c.Extensions = make(map[string]any, 1)
	}
	c.Extensions[key] = value
	return c
}

// Error describes the error for a log: kind, code, detail and cause.
func (e *Error) Error() string {
	s := e.Kind.String() + " " + e.Code + ": " + e.Detail
	if e.cause != nil {
		s += ": " + e.cause.Error()
	}
	return s
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.cause }

// From returns the first error of a kind in err's chain.
func From(err error) (*Error, bool) {
	var e *Error
	if stderrors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}

// IsKind reports whether err's chain holds an error of kind k.
func IsKind(err error, k Kind) bool {
	e, ok := From(err)
	return ok && e.Kind == k
}
