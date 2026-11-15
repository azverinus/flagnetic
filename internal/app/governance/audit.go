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

// AuditExporter counts the audit log's entries for a download, which then
// reads them a page at a time through the AuditReader.
type AuditExporter interface {
	// CountEntries returns how many entries match f, or limit when more do.
	CountEntries(ctx context.Context, f AuditFilter, limit int) (int, error)
}

// exportPageSize is how many entries one statement of a download reads. A
// download holds a connection only for the length of one such statement, never
// while it waits for a slow client to take what it was sent, so a client that
// stops reading cannot keep a connection, or a snapshot that holds vacuum
// back, for as long as it likes.
const exportPageSize = 1000

// exportRetry is what a download refused for lack of a slot is told to wait.
const exportRetry = 5 * time.Second

// ErrTooManyExports answers a download asked while the replica serves the
// most downloads it allows.
var ErrTooManyExports = apperrors.RateLimited("too_many_exports",
	"the server is serving the most audit downloads it allows at once; retry after Retry-After seconds", exportRetry)

// ExportAuditEntries reads the entries of a download.
type ExportAuditEntries struct {
	counter AuditExporter
	reader  AuditReader
	max     int
	slots   chan struct{}
}

// NewExportAuditEntries returns the handler, which refuses a download of
// more than limit entries and serves at most concurrent downloads at once.
func NewExportAuditEntries(counter AuditExporter, reader AuditReader, limit, concurrent int) *ExportAuditEntries {
	return &ExportAuditEntries{counter: counter, reader: reader, max: limit, slots: make(chan struct{}, concurrent)}
}

// Handle refuses ErrTooManyExports when every slot is taken, counts the
// entries matching f and refuses export_too_large above the cap. Otherwise it
// calls begin once, so the caller can start its answer, then each with every
// entry newest first, read a page at a time from the entry the last page
// ended at. An entry written meanwhile is newer than where the download
// started, so the entries streamed are the ones counted.
func (h *ExportAuditEntries) Handle(ctx context.Context, f AuditFilter, begin func() error, each func(governance.Entry) error) error {
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		return ErrTooManyExports
	}
	n, err := h.counter.CountEntries(ctx, f, h.max+1)
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
	var after governance.EntryID
	for {
		page, err := h.reader.Entries(ctx, f, after, exportPageSize)
		if err != nil {
			return err
		}
		for _, e := range page {
			if err := each(e); err != nil {
				return err
			}
		}
		if len(page) < exportPageSize {
			return nil
		}
		after = page[len(page)-1].ID
	}
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
