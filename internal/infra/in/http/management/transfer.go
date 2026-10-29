package management

import (
	"context"
	"net/http"
	"strconv"

	apptransfer "github.com/azverinus/flagnetic/internal/app/transfer"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// ExportProject answers the project's configuration as a project document.
func (a *api) ExportProject(ctx context.Context, req ExportProjectRequestObject) (ExportProjectResponseObject, error) {
	format := transfer.JSON
	if asked := req.Params.Format; asked != nil {
		f, ok := transfer.ParseFormat(string(*asked))
		if !ok {
			return nil, flaghttp.InvalidRequest("format must be json or yaml")
		}
		format = f
	}
	exported, err := a.h.ExportProject.Handle(ctx, apptransfer.ExportQuery{Project: projectID(ctx), Format: format})
	if err != nil {
		return nil, err
	}
	d := &documentDownload{
		format: format, body: exported.Document,
		filename: exported.Project.Key + "-" + a.opts.clock().Now().UTC().Format("20060102T150405Z") + "." + format.Extension(),
	}
	if !exported.Latest.IsZero() {
		d.etag = `"` + exported.Latest.String() + `"`
	}
	return d, nil
}

// documentDownload is a project document sent as an attachment. Its bytes
// are the domain's, not the ones a generated response would encode, so that
// an export is byte-stable.
type documentDownload struct {
	format   transfer.Format
	body     []byte
	filename string
	etag     string
}

func (d *documentDownload) VisitExportProjectResponse(w http.ResponseWriter) error {
	contentType := "application/json"
	if d.format == transfer.YAML {
		contentType = "application/yaml"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.filename+`"`)
	if d.etag != "" {
		w.Header().Set("ETag", d.etag)
	}
	w.WriteHeader(http.StatusOK)
	// The body is JSON or YAML under a matching Content-Type and an
	// attachment disposition, never HTML a browser would render.
	_, err := w.Write(d.body) //nolint:gosec // G705: see above
	return err
}
