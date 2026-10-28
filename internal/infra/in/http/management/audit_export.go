package management

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The formats of an audit download.
const (
	formatJSONL = "jsonl"
	formatCSV   = "csv"
)

// ExportProjectAuditEntries answers the download of the project's audit log.
func (a *api) ExportProjectAuditEntries(ctx context.Context, req ExportProjectAuditEntriesRequestObject) (ExportProjectAuditEntriesResponseObject, error) {
	q := req.Params
	format, err := exportFormat((*string)(q.Format))
	if err != nil {
		return nil, err
	}
	f := auditFilter(projectID(ctx), ListProjectAuditEntriesParams{
		Actor: q.Actor, ActorKind: q.ActorKind, Action: q.Action, EntityKind: q.EntityKind, EntityId: q.EntityId,
		Environment: q.Environment, From: q.From, To: q.To, Q: q.Q, KillSwitch: q.KillSwitch, ChangeRequest: q.ChangeRequest,
	})
	return a.download(ctx, f, projectOf(ctx).project.Key, format), nil
}

// ExportAuditEntries answers the download of the instance's audit log,
// narrowed to one project when the request names one.
func (a *api) ExportAuditEntries(ctx context.Context, req ExportAuditEntriesRequestObject) (ExportAuditEntriesResponseObject, error) {
	q := req.Params
	format, err := exportFormat((*string)(q.Format))
	if err != nil {
		return nil, err
	}
	var project workspace.ProjectID
	if q.Project != nil {
		project = id.FromBytes[workspace.Project](*q.Project)
	}
	f := auditFilter(project, ListProjectAuditEntriesParams{
		Actor: q.Actor, ActorKind: q.ActorKind, Action: q.Action, EntityKind: q.EntityKind, EntityId: q.EntityId,
		Environment: q.Environment, From: q.From, To: q.To, Q: q.Q, KillSwitch: q.KillSwitch, ChangeRequest: q.ChangeRequest,
	})
	return a.download(ctx, f, "instance", format), nil
}

// exportFormat returns the format a request asks for: JSON Lines unless it
// asks for another.
func exportFormat(asked *string) (string, error) {
	if asked == nil {
		return formatJSONL, nil
	}
	switch *asked {
	case formatJSONL, formatCSV:
		return *asked, nil
	}
	return "", flaghttp.InvalidRequest("format must be jsonl or csv")
}

// download returns the answer that streams the entries matching f, named
// after scope (a project's key or "instance") and the time it was asked.
func (a *api) download(ctx context.Context, f appgovernance.AuditFilter, scope, format string) *auditDownload {
	return &auditDownload{
		ctx: ctx, a: a, filter: f, csv: format == formatCSV,
		filename: fmt.Sprintf("audit-%s-%s.%s", scope, a.opts.clock().Now().UTC().Format("20060102T150405Z"), format),
	}
}

// auditDownload writes the entries of a filter to the response as they are
// read. The answer is a refusal until the first byte is sent; a failure after
// it aborts the connection, which a client reads as an incomplete download
// rather than a whole one.
type auditDownload struct {
	ctx      context.Context
	a        *api
	filter   appgovernance.AuditFilter
	csv      bool
	filename string
}

func (d *auditDownload) VisitExportProjectAuditEntriesResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d *auditDownload) VisitExportAuditEntriesResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d *auditDownload) write(w http.ResponseWriter) error {
	out := bufio.NewWriter(w)
	var rows *csv.Writer
	sent := false
	begin := func() error {
		contentType := "application/x-ndjson"
		if d.csv {
			contentType = "text/csv; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+d.filename+`"`)
		w.WriteHeader(http.StatusOK)
		sent = true
		if d.csv {
			rows = csv.NewWriter(out)
			return rows.Write(governance.CSVHeader)
		}
		return nil
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	each := func(e governance.Entry) error {
		if !d.csv {
			// Encode ends the line.
			return encoder.Encode(auditEntryOut(e))
		}
		row, err := governance.CSVRow(e)
		if err != nil {
			return err
		}
		return rows.Write(row)
	}
	err := d.a.h.ExportAuditEntries.Handle(d.ctx, d.filter, begin, each)
	if err == nil && rows != nil {
		rows.Flush()
		err = rows.Error()
	}
	if err == nil {
		err = out.Flush()
	}
	if err != nil && sent {
		d.a.edge.Logger().ErrorContext(d.ctx, "audit download cut short", "error", err.Error())
		panic(http.ErrAbortHandler)
	}
	return err
}
