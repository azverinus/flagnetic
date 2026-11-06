package transfer

import (
	"context"
	"errors"
	"strings"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// errDryRun ends the unit of work of a dry run, which rolls it back.
var errDryRun = errors.New("transfer: a dry run ends in a rollback")

// run is what every import into a project shares: the project written to,
// what the import found to say of the document, and how it records itself.
type run struct {
	owners Owners
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	req    governance.Request
	caller *access.MemberID

	project  workspace.Project
	changes  transfer.Changes
	warnings []transfer.Warning
	faults   []apperrors.Violation
	// refused are the pointers of the faults the document check found: the
	// entities under them are not written, since writing them would only
	// break what the check already refused, such as a key being unique.
	refused []string
}

// refuse remembers the faults the document check found, so that the entities
// under them are not written and the faults are reported with the ones the
// contexts find.
func (r *run) refuse(err error) error {
	if err == nil {
		return nil
	}
	e, ok := apperrors.From(err)
	if !ok {
		return err
	}
	r.faults = append(r.faults, e.Violations...)
	for _, v := range e.Violations {
		r.refused = append(r.refused, v.Pointer)
	}
	return nil
}

// isRefused reports whether the document check refused the entity at
// pointer, or a part of it.
func (r *run) isRefused(pointer string) bool {
	for _, p := range r.refused {
		if p == pointer || strings.HasPrefix(p, pointer+"/") {
			return true
		}
	}
	return false
}

// fault records err, a refusal of the entity at pointer, and goes on with
// the rest of the document so that every fault is reported; it returns any
// other error, which ends the import. A refusal is a fault the document
// has: invalid, with its violations pointing into the entity.
func (r *run) fault(err error, pointer string) error {
	if err == nil {
		return nil
	}
	e, ok := apperrors.From(err)
	if ok && e.Kind == apperrors.KindConflict && len(e.Violations) > 0 {
		// A conflict that names the fields in conflict, such as frozen, ends
		// the import as it is, its fields pointing into the document.
		return e.Under(pointer)
	}
	if !ok || e.Kind != apperrors.KindInvalid {
		return err
	}
	if len(e.Violations) == 0 {
		r.faults = append(r.faults, apperrors.Violation{Pointer: pointer, Code: e.Code, Detail: e.Detail})
		return nil
	}
	r.faults = append(r.faults, e.Under(pointer).Violations...)
	return nil
}

// invalid is the refusal of the document, with every fault found.
func (r *run) invalid() error {
	return apperrors.Invalid("invalid_document", "the project document is not valid", r.faults...)
}

// ownerOf returns the member to own an entity whose owner the document
// names as email. Members do not travel, so the address resolves only to
// someone who may own in the project now; otherwise the importing member
// owns it, and the import says so. A document that names no owner is the
// importer's.
func (r *run) ownerOf(ctx context.Context, email, pointer string) (*access.MemberID, error) {
	if email != "" {
		member, ok, err := r.owners.OwnerByEmail(ctx, r.project.ID, email)
		if err != nil {
			return nil, err
		}
		if ok {
			return &member, nil
		}
		r.warnOwnerReplaced(email, pointer, "the importing member owns it")
	}
	return r.caller, nil
}

// warnOwnerReplaced says that the owner email, whom the document names at
// pointer, cannot own in the project, and what the import did instead.
func (r *run) warnOwnerReplaced(email, pointer, instead string) {
	r.warnings = append(r.warnings, transfer.Warning{
		Code: transfer.WarningOwnerReplaced, Pointer: pointer, Params: map[string]any{"email": email},
		Detail: "the owner " + email + " cannot own in this project, so " + instead,
	})
}

// record writes the entry saying what the import did, after the entries of
// each entity it wrote.
func (r *run) record(ctx context.Context, mode transfer.Mode) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](r.ids), r.clock.Now(), r.req, governance.Record{
		Project: r.project.ID, Action: "project.imported",
		Entity: governance.Entity{Kind: "project", ID: r.project.ID.String(), Key: r.project.Key},
		After:  map[string]any{"mode": mode, "source": transfer.SourceDocument, "counts": r.changes.Counts()},
	})
	if err != nil {
		return err
	}
	return r.audit.Record(ctx, entry)
}
