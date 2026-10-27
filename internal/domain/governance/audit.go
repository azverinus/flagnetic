// Package governance is the domain of how changes are governed. For now it
// holds the audit entry: who changed what, when, from which value to which.
package governance

import (
	"errors"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// EntryID identifies an audit entry. Being time-ordered, it orders the log.
type EntryID = id.Of[Entry]

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

// CLIActor returns the actor of a change made by a command of the command
// line, which runs with the database's own credentials and so acts for no
// member: command is the command run, such as "admin bootstrap".
func CLIActor(command string) Actor {
	return Actor{Kind: ActorCLI, ID: "cli", Name: "flagnetic " + command}
}

// Request is where a change is made: by which actor, in which request. Every
// entry point names it, and a command handler records each entry with it.
type Request struct {
	Actor Actor
	// ID is the request ID, or "" outside an HTTP request.
	ID string
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
	// The audit log keeps it by identifier alone, since its entries outlive
	// the project.
	Project workspace.ProjectID
	// Environment is the environment addressed, if any.
	Environment workspace.EnvironmentID
	// Action is entity.verb, such as member.created.
	Action string
	Entity Entity
	// Before and After are the entity's values around the change, encoded as
	// JSON. They never hold a secret: a token appears by its hint.
	Before any
	After  any
	// KillSwitch marks the record of a kill switch, which an auditor
	// reviews apart from every other change.
	KillSwitch bool
}

// Entry is one audit entry.
type Entry struct {
	ID         EntryID
	OccurredAt time.Time
	Actor      Actor
	RequestID  string
	Record
	// ChangeRequest is the change request whose application made the
	// change; zero for any other change.
	ChangeRequest ChangeRequestID
}

// The refusals of NewEntry carry no error kind on purpose: every entry point
// names its actor and every handler records a well-formed action, so either
// is a programming error, answered as an internal failure and rolling back
// the change it would have recorded.
var (
	errNoActor   = errors.New("governance: an audit entry was recorded without an actor")
	errMalformed = errors.New("governance: an audit entry needs an action entity.verb and an entity")
)

// NewEntry returns the entry recording r, made at now in req. The handler
// making the change passes the time it read once for the whole change, so the
// entry and what it records carry the same instant. The instant is kept to
// the microsecond the log stores, so the entry an `audit.entry` event carries
// equals the entry as the log reads it back on every platform's clock.
func NewEntry(entryID EntryID, now time.Time, req Request, r Record) (Entry, error) {
	if req.Actor.Kind == "" {
		return Entry{}, errNoActor
	}
	entity, verb, _ := strings.Cut(r.Action, ".")
	if entity == "" || verb == "" || r.Entity.Kind == "" || r.Entity.ID == "" {
		return Entry{}, errMalformed
	}
	return Entry{ID: entryID, OccurredAt: now.UTC().Truncate(time.Microsecond), Actor: req.Actor, RequestID: req.ID, Record: r}, nil
}
