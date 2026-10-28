// Package governance holds the use cases of the governance context: recording
// the audit entries of every context and reading the audit log.
package governance

import (
	"context"
	"errors"
	"fmt"
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
	// ChangeRequest selects the entries the application of a change
	// request wrote.
	ChangeRequest governance.ChangeRequestID
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

// AuditExporter reads the audit log whole, for a download.
type AuditExporter interface {
	// CountEntries returns how many entries match f, or limit when more do.
	CountEntries(ctx context.Context, f AuditFilter, limit int) (int, error)
	// EachEntry calls fn with every entry matching f, newest first, as it is
	// read, and stops at the first error fn returns.
	EachEntry(ctx context.Context, f AuditFilter, fn func(governance.Entry) error) error
}

// Snapshotter runs a read in one consistent view of the database.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// ExportAuditEntries reads the entries of a download.
type ExportAuditEntries struct {
	snapshots Snapshotter
	store     AuditExporter
	max       int
}

// NewExportAuditEntries returns the handler, which refuses a download of
// more than limit entries.
func NewExportAuditEntries(snapshots Snapshotter, store AuditExporter, limit int) *ExportAuditEntries {
	return &ExportAuditEntries{snapshots: snapshots, store: store, max: limit}
}

// Handle counts the entries matching f and refuses ErrExportTooLarge above
// the cap. Otherwise it calls begin once, so the caller can start its
// answer, then each with every entry newest first. The count and the entries
// are read from one snapshot, so the entries streamed are the ones counted.
func (h *ExportAuditEntries) Handle(ctx context.Context, f AuditFilter, begin func() error, each func(governance.Entry) error) error {
	return h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		n, err := h.store.CountEntries(ctx, f, h.max+1)
		if err != nil {
			return err
		}
		if n > h.max {
			return apperrors.Invalid("export_too_large", fmt.Sprintf(
				"the filters match more than %d entries, the most one download holds; narrow them with from and to", h.max))
		}
		if err := begin(); err != nil {
			return err
		}
		return h.store.EachEntry(ctx, f, each)
	})
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
