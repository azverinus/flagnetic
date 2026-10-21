// Package governance is the domain of how changes are governed. For now it
// holds the audit entry: who changed what, when, from which value to which.
package governance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// EntryID identifies an audit entry. Being time-ordered, it orders the log.
type EntryID = id.Of[Entry]

// Project is a project as the audit log refers to it: by identifier alone,
// since its entries outlive it.
type Project struct{}

// Environment is an environment as the audit log refers to it.
type Environment struct{}

// ActorKind is what kind of actor made a change.
type ActorKind string

// The actor kinds.
const (
	ActorMember ActorKind = "member"
	// ActorToken is an API token, with the member holding it for a personal
	// access token.
	ActorToken  ActorKind = "token"
	ActorCLI    ActorKind = "cli"
	ActorSystem ActorKind = "system"
)

// Actor is who an audit entry names as having made the change.
type Actor struct {
	Kind ActorKind
	// ID identifies the actor within its kind: a member's or a token's
	// identifier, or a fixed name for the CLI and the system.
	ID   string
	Name string
	// MemberID is the member a token acts for, or "" when none.
	MemberID string
}

// Request is the request a change is made in: its actor and its ID.
type Request struct {
	Actor Actor
	// ID is the request ID, or "" outside an HTTP request.
	ID string
}

type requestKey struct{}

// WithRequest returns ctx carrying req, so every entry recorded in ctx names
// its actor without each handler passing it along.
func WithRequest(ctx context.Context, req Request) context.Context {
	return context.WithValue(ctx, requestKey{}, req)
}

// RequestFrom returns the request ctx carries.
func RequestFrom(ctx context.Context) (Request, bool) {
	req, ok := ctx.Value(requestKey{}).(Request)
	return req, ok
}

// Entity is what a change was made to.
type Entity struct {
	// Kind is the entity's kind, such as member or api_token.
	Kind string
	// ID is the entity's identifier, by which its history is found, so a
	// reused key never mixes two entities' histories.
	ID string
	// Key is the entity's key or name as it was when the change was made.
	Key string
}

// Record is what a command handler records of one change.
type Record struct {
	// Project is the project the change belongs to; zero for the instance.
	Project id.Of[Project]
	// Environment is the environment addressed, if any.
	Environment id.Of[Environment]
	// Action is entity.verb, such as member.created.
	Action string
	Entity Entity
	// Before and After are the entity's values around the change, encoded as
	// JSON. They never hold a secret: a token appears by its hint.
	Before any
	After  any
}

// Entry is one audit entry.
type Entry struct {
	ID         EntryID
	OccurredAt time.Time
	Actor      Actor
	RequestID  string
	Record
}

// errNoActor is a change recorded outside a request naming its actor: every
// entry point sets one, so this is a programming error.
var errNoActor = errors.New("governance: an audit entry was recorded without an actor in its context")

// NewEntry returns the entry recording r at now in the request ctx carries.
func NewEntry(ctx context.Context, entryID EntryID, now time.Time, r Record) (Entry, error) {
	req, ok := RequestFrom(ctx)
	if !ok || req.Actor.Kind == "" {
		return Entry{}, errNoActor
	}
	entity, verb, _ := strings.Cut(r.Action, ".")
	if entity == "" || verb == "" || r.Entity.Kind == "" || r.Entity.ID == "" {
		return Entry{}, errors.New("governance: an audit entry needs an action entity.verb and an entity")
	}
	return Entry{ID: entryID, OccurredAt: now, Actor: req.Actor, RequestID: req.ID, Record: r}, nil
}
