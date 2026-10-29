package transfer

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ExportProjectDeps are what the export reads.
type ExportProjectDeps struct {
	Snapshots   Snapshotter
	Projects    Projects
	Workspace   Workspace
	Flags       Flags
	Experiments Experiments
	Members     Members
	History     History
}

// ExportProject writes a project's configuration as a project document.
type ExportProject struct{ d ExportProjectDeps }

// NewExportProject returns the handler.
func NewExportProject(d ExportProjectDeps) *ExportProject { return &ExportProject{d: d} }

// ExportQuery says which project to export, and in which encoding.
type ExportQuery struct {
	Project workspace.ProjectID
	Format  transfer.Format
}

// Exported is a project document with what identifies the state of the
// project it holds.
type Exported struct {
	Project workspace.Project
	// Document is the document written in the format asked for.
	Document []byte
	// Latest is the newest audit entry of the project when the document was
	// read, which any later change to the project moves on; the zero
	// identifier when the project has none.
	Latest governance.EntryID
}

// Handle returns the document of the project q names. Everything it holds is
// read in one snapshot, so it is the project as it was at one instant, and
// Latest is the newest entry of that instant: an import naming it as the
// state it was based on is refused when the project has moved on since.
// An unknown project is the not_found refusal.
func (h *ExportProject) Handle(ctx context.Context, q ExportQuery) (Exported, error) {
	var (
		out Exported
		s   transfer.Snapshot
	)
	err := h.d.Snapshots.Snapshot(ctx, func(ctx context.Context) error {
		ps, err := h.d.Projects.Projects(ctx, false, []workspace.ProjectID{q.Project})
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			return errProjectNotFound
		}
		s.Project = ps[0]
		if out.Latest, err = h.d.History.LatestProjectEntry(ctx, q.Project); err != nil {
			return err
		}
		return h.read(ctx, q.Project, &s)
	})
	if err != nil {
		return Exported{}, err
	}
	out.Project = s.Project
	out.Document, err = transfer.Encode(q.Format, transfer.FromSnapshot(s))
	return out, err
}

// read fills s with what project holds, apart from the project itself. The
// reads run in order and the first failure ends them.
func (h *ExportProject) read(ctx context.Context, project workspace.ProjectID, s *transfer.Snapshot) error {
	reads := []func() (err error){
		func() (err error) { s.Environments, err = h.d.Workspace.Environments(ctx, project); return },
		func() (err error) { s.UnitTypes, err = h.d.Workspace.UnitTypes(ctx, project); return },
		func() (err error) { s.Attributes, err = h.d.Workspace.Attributes(ctx, project); return },
		func() (err error) { s.Segments, err = h.d.Flags.Segments(ctx, project); return },
		func() (err error) { s.Flags, err = h.d.Flags.ProjectFlags(ctx, project); return },
		func() (err error) { s.Configurations, err = h.d.Flags.ProjectConfigurations(ctx, project); return },
		func() (err error) { s.Experiments, err = h.d.Experiments.ProjectExperiments(ctx, project); return },
	}
	for _, read := range reads {
		if err := read(); err != nil {
			return err
		}
	}
	owners := make([]access.MemberID, 0, len(s.Flags)+len(s.Experiments))
	for _, f := range s.Flags {
		owners = append(owners, f.Owner)
	}
	for _, e := range s.Experiments {
		owners = append(owners, e.Owner)
	}
	var err error
	s.Emails, err = h.d.Members.MemberEmails(ctx, owners)
	return err
}
