package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// The bounds of a page of flags.
const (
	defaultFlagLimit = 50
	maxFlagLimit     = 200
)

// ListFlags answers a page of the project's flags, filtered and sorted
// as the request asks.
func (a *api) ListFlags(ctx context.Context, req ListFlagsRequestObject) (ListFlagsResponseObject, error) {
	page, err := flagPageOf(req.Params)
	if err != nil {
		return nil, err
	}
	fs, next, err := a.h.ListFlags.Handle(ctx, projectID(ctx), page)
	if err != nil {
		return nil, err
	}
	out := ListFlags200JSONResponse{Items: each(fs, flagListItemOut)}
	if next != nil {
		cursor := encodeFlagCursor(page.Sort, *next)
		out.NextCursor = &cursor
	}
	return out, nil
}

// flagPageOf reads the page params ask for, refusing a request whose
// filters or cursor do not make one.
func flagPageOf(params ListFlagsParams) (appflags.FlagPage, error) {
	page := appflags.FlagPage{
		Limit: defaultFlagLimit, Sort: appflags.SortByKey, Text: valueOf(params.Q), Tag: valueOf(params.Tag),
		Archived: valueOr(params.Archived, false), Environment: valueOf(params.Environment),
	}
	if l := params.Limit; l != nil {
		if *l < 1 || *l > maxFlagLimit {
			return page, flaghttp.InvalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxFlagLimit))
		}
		page.Limit = *l
	}
	if t := params.Type; t != nil {
		if !slices.Contains(flags.Types(), flags.Type(*t)) {
			return page, flaghttp.InvalidRequest("type must be one of boolean, string, number and json")
		}
		page.Type = flags.Type(*t)
	}
	if o := params.Owner; o != nil {
		m := id.FromBytes[access.Member](*o)
		page.Owner = &m
	}
	if st := params.State; st != nil {
		page.State = appflags.StateFilter(*st)
		if !slices.Contains([]appflags.StateFilter{appflags.StateOn, appflags.StateOff, appflags.StateRules, appflags.StateRollout, appflags.StateExperiment}, page.State) {
			return page, flaghttp.InvalidRequest("state must be one of on, off, rules, rollout and experiment")
		}
		if page.Environment == "" {
			return page, flaghttp.InvalidRequest("state is judged in the environment named by environment")
		}
	}
	if d := params.DiffersBetween; d != nil {
		if len(*d) != 2 || (*d)[0] == (*d)[1] {
			return page, flaghttp.InvalidRequest("differs_between names two different environments")
		}
		page.DiffersBetween = &[2]string{(*d)[0], (*d)[1]}
	}
	if so := params.Sort; so != nil {
		page.Sort = appflags.FlagSort(*so)
		if !slices.Contains([]appflags.FlagSort{appflags.SortByKey, appflags.SortByName, appflags.SortByUpdated}, page.Sort) {
			return page, flaghttp.InvalidRequest("sort must be one of key, name and updated")
		}
	}
	if c := params.Cursor; c != nil {
		after, err := decodeFlagCursor(page.Sort, *c)
		if err != nil {
			return page, err
		}
		page.After = &after
	}
	return page, nil
}

// flagCursor is a page's cursor as a client holds it: the sort it pages
// and the values of the last flag of the page that sort compares. It is
// opaque, so the order a page follows can change without breaking a
// client that only passes it back.
type flagCursor struct {
	Sort      appflags.FlagSort `json:"s"`
	Key       string            `json:"k"`
	Name      string            `json:"n,omitempty"`
	UpdatedAt *time.Time        `json:"u,omitempty"`
}

func encodeFlagCursor(sort appflags.FlagSort, c appflags.FlagCursor) string {
	fc := flagCursor{Sort: sort, Key: c.Key}
	switch sort {
	case appflags.SortByName:
		fc.Name = c.Name
	case appflags.SortByUpdated:
		fc.UpdatedAt = &c.UpdatedAt
	}
	b, err := json.Marshal(fc)
	if err != nil {
		// A struct of strings and a time always encodes.
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeFlagCursor reads cursor, refusing one this API did not return for
// sort.
func decodeFlagCursor(sort appflags.FlagSort, cursor string) (appflags.FlagCursor, error) {
	refused := flaghttp.InvalidRequest("cursor is not a next_cursor this API returned for this sort")
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return appflags.FlagCursor{}, refused
	}
	var fc flagCursor
	if json.Unmarshal(b, &fc) != nil || fc.Sort != sort || fc.Key == "" || (sort == appflags.SortByUpdated) != (fc.UpdatedAt != nil) {
		return appflags.FlagCursor{}, refused
	}
	out := appflags.FlagCursor{Key: fc.Key, Name: fc.Name}
	if fc.UpdatedAt != nil {
		out.UpdatedAt = *fc.UpdatedAt
	}
	return out, nil
}

// CreateFlag creates a flag, switched off in every environment.
func (a *api) CreateFlag(ctx context.Context, req CreateFlagRequestObject) (CreateFlagResponseObject, error) {
	b := req.Body
	cmd := appflags.CreateFlagCommand{
		Project: projectID(ctx), Key: b.Key, Type: flags.Type(b.Type), Name: b.Name, Description: valueOf(b.Description),
		Owner: ownerOf(b.Owner), Caller: callerMember(ctx), Tags: valueOr(b.Tags, nil), ExpectedRemovalAt: b.ExpectedRemovalAt,
		AutomaticExposure: valueOr(b.AutomaticExposure, false), Variations: variationsIn(b.Variations), Request: requestOf(ctx),
	}
	if b.Lifetime != nil {
		cmd.Lifetime = flags.Lifetime(*b.Lifetime)
	}
	v, err := a.h.CreateFlag.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return CreateFlag201JSONResponse{Body: flagOut(v), Headers: CreateFlag201ResponseHeaders{ETag: etag(v.Flag.Version)}}, nil
}

// GetFlag answers a flag with its configuration in every environment.
func (a *api) GetFlag(ctx context.Context, req GetFlagRequestObject) (GetFlagResponseObject, error) {
	v, err := a.h.GetFlag.Handle(ctx, projectID(ctx), req.Flag)
	if err != nil {
		return nil, err
	}
	return GetFlag200JSONResponse{Body: flagOut(v), Headers: GetFlag200ResponseHeaders{ETag: etag(v.Flag.Version)}}, nil
}

// UpdateFlag changes a flag's definition.
func (a *api) UpdateFlag(ctx context.Context, req UpdateFlagRequestObject) (UpdateFlagResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	change := flags.FlagChange{
		Key: b.Key, Name: b.Name, Description: b.Description, Tags: b.Tags, ExpectedRemovalAt: b.ExpectedRemovalAt,
		AutomaticExposure: b.AutomaticExposure, Variations: variationsIn(b.Variations),
	}
	if b.Type != nil {
		t := flags.Type(*b.Type)
		change.Type = &t
	}
	if b.Lifetime != nil {
		l := flags.Lifetime(*b.Lifetime)
		change.Lifetime = &l
	}
	v, err := a.h.UpdateFlag.Handle(ctx, appflags.UpdateFlagCommand{
		Project: projectID(ctx), Key: req.Flag, Change: change, Owner: ownerOf(b.Owner), Expected: expected,
		Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateFlag200JSONResponse{Body: flagOut(v), Headers: UpdateFlag200ResponseHeaders{ETag: etag(v.Flag.Version)}}, nil
}

// ifMatch reads the version a write names in If-Match: none when the header
// is absent, which the handler refuses once the request is validated.
func ifMatch(header *IfMatch) (precondition.Version, error) {
	if header == nil {
		return precondition.None(), nil
	}
	v, err := precondition.Parse(*header)
	if err != nil {
		return v, flaghttp.InvalidRequest("If-Match names a version this API answered as an ETag, such as \"7\"")
	}
	return v, nil
}

// etag returns version v as the ETag a response carries.
func etag(v int64) *string {
	t := precondition.Tag(v)
	return &t
}

// callerMember returns the member the request ctx belongs to acts for, nil
// for a project token.
func callerMember(ctx context.Context) *access.MemberID {
	if m := callerOf(ctx).Member; m != nil {
		return &m.ID
	}
	return nil
}

func ownerOf(u *openapi_types.UUID) *access.MemberID {
	if u == nil {
		return nil
	}
	m := id.FromBytes[access.Member](*u)
	return &m
}

func valueOr[T any](v *T, otherwise T) T {
	if v == nil {
		return otherwise
	}
	return *v
}

func variationsIn(in *[]VariationInput) *[]flags.VariationInput {
	if in == nil {
		return nil
	}
	out := make([]flags.VariationInput, len(*in))
	for i, v := range *in {
		out[i] = flags.VariationInput{Key: v.Key, Value: v.Value, Name: v.Name, Description: v.Description}
	}
	return &out
}

func flagListItemOut(l appflags.ListedFlag) FlagListItem {
	d := flagDefinitionOut(l.Flag)
	return FlagListItem{
		Id: d.Id, Key: d.Key, Type: d.Type, Name: d.Name, Description: d.Description, Owner: d.Owner, Tags: d.Tags,
		Lifetime: d.Lifetime, ExpectedRemovalAt: d.ExpectedRemovalAt, AutomaticExposure: d.AutomaticExposure,
		Variations: d.Variations, ArchivedAt: d.ArchivedAt, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt, Version: d.Version,
		States: each(l.States, func(s appflags.FlagState) FlagState {
			return FlagState{
				Environment: s.Environment, On: s.On, HasRules: s.HasRules, HasRollout: s.HasRollout, HasExperiment: s.HasExperiment,
				LastEvaluatedAt: s.LastEvaluatedAt,
			}
		}),
	}
}

func flagDefinitionOut(f flags.Flag) FlagDefinition {
	out := FlagDefinition{
		Id: openapi_types.UUID(f.ID.Bytes()), Key: f.Key, Type: FlagType(f.Type), Name: f.Name, Description: f.Description,
		Owner: openapi_types.UUID(f.Owner.Bytes()), Tags: f.Tags, Lifetime: Lifetime(f.Lifetime),
		ExpectedRemovalAt: f.ExpectedRemovalAt, AutomaticExposure: f.AutomaticExposure, ArchivedAt: f.ArchivedAt,
		CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt, Version: f.Version,
		Variations: each(f.Variations, func(v flags.Variation) Variation {
			return Variation{Key: v.Key, Value: v.Value, Name: v.Name, Description: v.Description}
		}),
	}
	if f.CreatedBy != nil {
		m := openapi_types.UUID(f.CreatedBy.Bytes())
		out.CreatedBy = &m
	}
	return out
}

func flagOut(v appflags.FlagView) Flag {
	d := flagDefinitionOut(v.Flag)
	return Flag{
		Id: d.Id, Key: d.Key, Type: d.Type, Name: d.Name, Description: d.Description, Owner: d.Owner, Tags: d.Tags,
		Lifetime: d.Lifetime, ExpectedRemovalAt: d.ExpectedRemovalAt, AutomaticExposure: d.AutomaticExposure,
		Variations: d.Variations, ArchivedAt: d.ArchivedAt, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt, Version: d.Version,
		Environments:  each(v.Configurations, configurationOut),
		LastEvaluated: lastEvaluatedOut(v),
	}
}

// lastEvaluatedOut returns when v was last evaluated in each environment, by
// the environments' order.
func lastEvaluatedOut(v appflags.FlagView) []FlagLastEvaluated {
	return each(v.Configurations, func(c flags.EnvironmentConfiguration) FlagLastEvaluated {
		out := FlagLastEvaluated{Environment: c.Environment}
		if at, ok := v.LastEvaluated[c.Environment]; ok {
			out.LastEvaluatedAt = &at
		}
		return out
	})
}

// flagCommand returns the command naming flag of the request's project,
// based on the version header names.
func flagCommand(ctx context.Context, flag string, header *IfMatch) (appflags.FlagCommand, error) {
	expected, err := ifMatch(header)
	return appflags.FlagCommand{Project: projectID(ctx), Key: flag, Expected: expected, Request: requestOf(ctx)}, err
}

// ArchiveFlag hides a flag from SDKs in every environment.
func (a *api) ArchiveFlag(ctx context.Context, req ArchiveFlagRequestObject) (ArchiveFlagResponseObject, error) {
	cmd, err := flagCommand(ctx, req.Flag, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := a.h.ArchiveFlag.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return ArchiveFlag200JSONResponse{Body: flagOut(v), Headers: ArchiveFlag200ResponseHeaders{ETag: etag(v.Flag.Version)}}, nil
}

// RestoreFlag brings an archived flag back.
func (a *api) RestoreFlag(ctx context.Context, req RestoreFlagRequestObject) (RestoreFlagResponseObject, error) {
	cmd, err := flagCommand(ctx, req.Flag, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := a.h.RestoreFlag.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return RestoreFlag200JSONResponse{Body: flagOut(v), Headers: RestoreFlag200ResponseHeaders{ETag: etag(v.Flag.Version)}}, nil
}

// DeleteFlag deletes an archived flag.
func (a *api) DeleteFlag(ctx context.Context, req DeleteFlagRequestObject) (DeleteFlagResponseObject, error) {
	cmd, err := flagCommand(ctx, req.Flag, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.h.DeleteFlag.Handle(ctx, cmd); err != nil {
		return nil, err
	}
	return DeleteFlag204Response{}, nil
}
