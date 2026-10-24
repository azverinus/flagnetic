// Package governance holds the use cases of the governance context: recording
// the audit entries of every context and reading the audit log.
package governance

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ErrNotFound is what a store answers for an entry it does not hold.
var ErrNotFound = errors.New("governance: not found")

// AuditFilter selects audit entries; every field set narrows the selection.
type AuditFilter struct {
	// Project selects one project's entries; zero selects the entries of
	// every project and of none.
	Project workspace.ProjectID
	// Actor selects a member's changes, made directly or through their
	// personal access tokens.
	Actor       access.MemberID
	ActorKind   governance.ActorKind
	Action      string
	EntityKind  string
	EntityID    string
	Environment workspace.EnvironmentID
	// From and To bound the time of an entry, both included; zero for none.
	From, To time.Time
	// KillSwitch selects the entries of kill switches when true, every
	// other entry when false; nil selects both.
	KillSwitch *bool
	// Q is text found, whatever its case, in the action, the entity's key or
	// the actor's name.
	Q string
}

// AuditPage asks for one page of the log, newest first.
type AuditPage struct {
	// After is the last entry of the previous page, zero for the first.
	After governance.EntryID
	Limit int
}

// AuditReader reads the audit log.
type AuditReader interface {
	// Entries returns at most limit entries matching f, older than after
	// (unless zero), newest first.
	Entries(ctx context.Context, f AuditFilter, after governance.EntryID, limit int) ([]governance.Entry, error)
	// Entry returns the entry of project with entryID, or ErrNotFound.
	Entry(ctx context.Context, project workspace.ProjectID, entryID governance.EntryID) (governance.Entry, error)
}

// ListAuditEntries reads the audit log a page at a time.
type ListAuditEntries struct {
	store AuditReader
}

// NewListAuditEntries returns the handler.
func NewListAuditEntries(store AuditReader) *ListAuditEntries { return &ListAuditEntries{store: store} }

// Handle returns one page of the entries matching f, newest first, and the
// last entry of the page when another follows (zero otherwise). Paging by
// identifier, an entry written meanwhile never shifts a page.
func (h *ListAuditEntries) Handle(ctx context.Context, f AuditFilter, p AuditPage) ([]governance.Entry, governance.EntryID, error) {
	entries, err := h.store.Entries(ctx, f, p.After, p.Limit+1)
	if err != nil || len(entries) <= p.Limit {
		return entries, governance.EntryID{}, err
	}
	entries = entries[:p.Limit]
	return entries, entries[p.Limit-1].ID, nil
}

// GetAuditEntry reads one entry of a project's audit log.
type GetAuditEntry struct {
	store AuditReader
}

// NewGetAuditEntry returns the handler.
func NewGetAuditEntry(store AuditReader) *GetAuditEntry { return &GetAuditEntry{store: store} }

// Handle returns project's entry with entryID; an entry of another project
// or of none is not found.
func (h *GetAuditEntry) Handle(ctx context.Context, project workspace.ProjectID, entryID governance.EntryID) (governance.Entry, error) {
	e, err := h.store.Entry(ctx, project, entryID)
	if errors.Is(err, ErrNotFound) {
		return e, ErrEntryNotFound
	}
	return e, err
}

// ErrEntryNotFound answers an entry the project's log does not hold.
var ErrEntryNotFound = apperrors.NotFound("not_found", "the project's audit log has no entry with this identifier")
