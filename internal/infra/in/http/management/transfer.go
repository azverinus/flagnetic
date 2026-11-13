package management

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	apptransfer "github.com/azverinus/flagnetic/internal/app/transfer"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/transfer/firebase"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/google/uuid"
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

// rawBodies serves the operations whose body is a project document, which
// it reads itself: the generated server would decode JSON into a type that
// forgets the unknown fields, repeated keys and exact numbers a strict
// reading of the document needs, and hands YAML over without the media
// type. The seam has judged the request before it gets here, as for every
// other operation.
type rawBodies struct {
	ServerInterface
	a *api
}

func (s rawBodies) ImportNewProject(w http.ResponseWriter, r *http.Request, params ImportNewProjectParams) {
	res, err := s.a.importNewProject(r, params)
	if err == nil {
		err = res.VisitImportNewProjectResponse(w)
	}
	if err != nil {
		s.a.edge.WriteError(w, r, httpError(err))
	}
}

func (s rawBodies) ImportProject(w http.ResponseWriter, r *http.Request, _ ProjectKey, params ImportProjectParams) {
	res, err := s.a.importProject(r, params)
	if err == nil {
		err = res.VisitImportProjectResponse(w)
	}
	if err != nil {
		s.a.edge.WriteError(w, r, httpError(err))
	}
}

func (s rawBodies) ImportFirebaseTemplate(w http.ResponseWriter, r *http.Request, _ ProjectKey, params ImportFirebaseTemplateParams) {
	res, err := s.a.importFirebaseTemplate(r, params)
	if err == nil {
		err = res.VisitImportFirebaseTemplateResponse(w)
	}
	if err != nil {
		s.a.edge.WriteError(w, r, httpError(err))
	}
}

// ImportFirebaseTemplate is never routed to: rawBodies serves the operation.
func (a *api) ImportFirebaseTemplate(context.Context, ImportFirebaseTemplateRequestObject) (ImportFirebaseTemplateResponseObject, error) {
	return nil, errors.New("management: importFirebaseTemplate reads its own body and is served by the raw route")
}

// ImportProject is never routed to: rawBodies serves the operation.
func (a *api) ImportProject(context.Context, ImportProjectRequestObject) (ImportProjectResponseObject, error) {
	return nil, errors.New("management: importProject reads its own body and is served by the raw route")
}

// ImportNewProject is never routed to: rawBodies serves the operation.
func (a *api) ImportNewProject(context.Context, ImportNewProjectRequestObject) (ImportNewProjectResponseObject, error) {
	return nil, errors.New("management: importNewProject reads its own body and is served by the raw route")
}

// readDocument reads the project document the request sends, in the format
// its media type names: another media type is 415, a body that is no
// document in the format 400, a document that breaks a rule 422 with a
// pointer into it per fault.
func readDocument(r *http.Request) (transfer.Document, error) {
	format, ok := documentFormat(r.Header.Get("Content-Type"))
	if !ok {
		return transfer.Document{}, flaghttp.UnsupportedMediaType("the document must be sent as application/json or application/yaml")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return transfer.Document{}, err
	}
	d, err := transfer.Decode(format, body)
	var malformed *transfer.MalformedError
	if errors.As(err, &malformed) {
		return transfer.Document{}, flaghttp.InvalidRequest(malformed.Detail)
	}
	return d, err
}

// documentFormat returns the format a media type names, and whether it names
// one.
func documentFormat(contentType string) (transfer.Format, bool) {
	media, _, err := mime.ParseMediaType(contentType)
	switch {
	case err != nil:
		return "", false
	case media == "application/json":
		return transfer.JSON, true
	case media == "application/yaml":
		return transfer.YAML, true
	}
	return "", false
}

func (a *api) importNewProject(r *http.Request, params ImportNewProjectParams) (ImportNewProjectResponseObject, error) {
	ctx := r.Context()
	d, err := readDocument(r)
	if err != nil {
		return nil, err
	}
	res, err := a.h.ImportNewProject.Handle(ctx, apptransfer.ImportNewProjectCommand{
		Document: d, Key: params.Key, Name: params.Name, DryRun: params.DryRun != nil && *params.DryRun,
		Caller: callerMember(ctx), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	if res.DryRun {
		return ImportNewProject200JSONResponse(importResultOut(res)), nil
	}
	return ImportNewProject201JSONResponse(importResultOut(res)), nil
}

func (a *api) importProject(r *http.Request, params ImportProjectParams) (ImportProjectResponseObject, error) {
	ctx := r.Context()
	// The generated route does not check the enum; a mode this server does
	// not offer must not run as a merge.
	if !params.Mode.Valid() {
		return nil, flaghttp.InvalidRequest("mode must be merge or replace")
	}
	d, err := readDocument(r)
	if err != nil {
		return nil, err
	}
	cmd := apptransfer.ImportIntoProjectCommand{
		Project: projectID(ctx), Mode: transfer.Mode(params.Mode), Document: d, DryRun: params.DryRun != nil && *params.DryRun,
		Caller: callerMember(ctx), Request: requestOf(ctx),
	}
	if params.IfMatch != nil {
		tag := strings.Trim(strings.TrimSpace(*params.IfMatch), `"`)
		cmd.IfMatch = &tag
	}
	res, err := a.h.ImportIntoProject.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	out := ImportProject200JSONResponse{Body: importResultOut(res)}
	if res.DryRun && !res.Latest.IsZero() {
		etag := `"` + res.Latest.String() + `"`
		out.Headers.ETag = &etag
	}
	return out, nil
}

func importResultOut(r apptransfer.ImportResult) ImportResult {
	description := r.Project.Description
	out := ImportResult{
		Project: DocumentProject{Key: r.Project.Key, Name: r.Project.Name, Description: &description},
		Mode:    ImportResultMode(r.Mode), Source: ImportResultSource(r.Source), DryRun: r.DryRun,
		Changes: ImportChanges{
			Environments: kindChangesOut(r.Changes.Environments), UnitTypes: kindChangesOut(r.Changes.UnitTypes),
			Attributes: kindChangesOut(r.Changes.Attributes), Segments: kindChangesOut(r.Changes.Segments),
			Flags: kindChangesOut(r.Changes.Flags), Experiments: kindChangesOut(r.Changes.Experiments),
		},
		Warnings: make([]ImportWarning, len(r.Warnings)),
	}
	for i, w := range r.Warnings {
		out.Warnings[i] = ImportWarning{Code: w.Code, Pointer: w.Pointer, Detail: w.Detail}
		if w.Params != nil {
			params := w.Params
			out.Warnings[i].Params = &params
		}
	}
	return out
}

func kindChangesOut(k transfer.KindChanges) ImportKindChanges {
	out := ImportKindChanges{Created: orEmpty(k.Created), Deleted: orEmpty(k.Deleted), Updated: make([]ImportUpdate, len(k.Updated))}
	for i, u := range k.Updated {
		patch := make([]JsonPatchOperation, len(u.Patch))
		for j, op := range u.Patch {
			patch[j] = JsonPatchOperation{Op: JsonPatchOperationOp(op.Op), Path: op.Path, Value: op.Value}
			if op.From != "" {
				from := op.From
				patch[j].From = &from
			}
		}
		out.Updated[i] = ImportUpdate{Key: u.Key, Patch: patch}
	}
	return out
}

// orEmpty returns keys, never nil, which a list in an answer must not be.
func orEmpty(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

// importFirebaseTemplate reads the template the request sends, which must be
// JSON, leniently: a template from a newer Firebase holds fields this server
// does not know. A body that is no template is 422 with the fault pointing
// into it, as every other fault of a template is.
func (a *api) importFirebaseTemplate(r *http.Request, params ImportFirebaseTemplateParams) (ImportFirebaseTemplateResponseObject, error) {
	ctx := r.Context()
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		return nil, flaghttp.UnsupportedMediaType("the template must be sent as application/json")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	template, err := firebase.Decode(body)
	if err != nil {
		return nil, err
	}
	cmd := apptransfer.ImportFirebaseTemplateCommand{
		Project: projectID(ctx), Template: template, Environment: params.Environment, DryRun: params.DryRun != nil && *params.DryRun,
		Caller: callerMember(ctx), Request: requestOf(ctx),
	}
	if params.UnitType != nil {
		cmd.UnitType = *params.UnitType
	}
	if params.IfMatch != nil {
		tag := strings.Trim(strings.TrimSpace(*params.IfMatch), `"`)
		cmd.IfMatch = &tag
	}
	res, err := a.h.ImportFirebaseTemplate.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	out := ImportFirebaseTemplate200JSONResponse{Body: firebaseResultOut(res)}
	if res.DryRun && !res.Latest.IsZero() {
		etag := `"` + res.Latest.String() + `"`
		out.Headers.ETag = &etag
	}
	return out, nil
}

func firebaseResultOut(r apptransfer.FirebaseImportResult) FirebaseImportResult {
	base := importResultOut(r.ImportResult)
	out := FirebaseImportResult{
		Project: base.Project, Mode: FirebaseImportResultMode(base.Mode), Source: FirebaseImportResultSource(base.Source),
		DryRun: base.DryRun, Changes: base.Changes, Warnings: base.Warnings,
		Flags: make([]FirebaseFlag, len(r.Flags)), Attributes: make([]FirebaseAttribute, len(r.Attributes)),
	}
	for i, f := range r.Flags {
		out.Flags[i] = FirebaseFlag{
			Key: f.Key, Type: string(f.Type), Description: f.Description, Tags: f.Tags, On: f.On, OffVariation: f.OffVariation,
			Fallthrough: f.Fallthrough, Variations: make([]DocumentVariation, len(f.Variations)), Rules: make([]DocumentRule, len(f.Rules)),
		}
		for j, v := range f.Variations {
			name, description := v.Name, v.Description
			out.Flags[i].Variations[j] = DocumentVariation{Key: v.Key, Value: v.Value, Name: &name, Description: &description}
		}
		for j, rule := range f.Rules {
			out.Flags[i].Rules[j] = documentRuleOut(rule)
		}
	}
	for i, a := range r.Attributes {
		out.Attributes[i] = FirebaseAttribute{Key: a.Key, Type: string(a.Type)}
	}
	return out
}

// documentRuleOut returns a rule as the document spells it. A serve writes
// only the fields of its kind, and an empty optional field is left out.
func documentRuleOut(r transfer.Rule) DocumentRule {
	out := DocumentRule{Description: &r.Description, Conditions: make([]DocumentCondition, len(r.Conditions))}
	if u, err := uuid.Parse(r.ID); err == nil {
		out.Id = &u
	}
	for i, c := range r.Conditions {
		out.Conditions[i] = DocumentCondition{
			Kind: c.Kind, Attribute: textOrNil(c.Attribute), Op: textOrNil(c.Op), Value: c.Value, Segment: textOrNil(c.Segment),
			Flag: textOrNil(c.Flag), Variation: textOrNil(c.Variation),
		}
	}
	out.Serve = DocumentServe{
		Kind: r.Serve.Kind, Variation: textOrNil(r.Serve.Variation), Experiment: textOrNil(r.Serve.Experiment),
		UnitType: textOrNil(r.Serve.UnitType), Percentage: r.Serve.Percentage, Salt: textOrNil(r.Serve.Salt),
	}
	return out
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
