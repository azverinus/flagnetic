package scim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// BaseURL is where the SCIM API is served.
const BaseURL = "/scim/v2"

// maxBody bounds a request's body: a provisioning resource carries no payload
// worth more.
const maxBody = 64 << 10

const (
	errorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"
	listSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	mediaType   = "application/scim+json"
	// defaultCount is how many resources a list answers when it is not asked
	// for fewer, and the most it ever answers.
	defaultCount = 100
)

// Handlers are the use cases the SCIM API serves.
type Handlers struct {
	Authenticate *appidentity.SCIMAuthenticator
	ListUsers    *appidentity.ListSCIMUsers
	GetUser      *appidentity.GetSCIMUser
	CreateUser   *appidentity.CreateSCIMUser
	ReplaceUser  *appidentity.ReplaceSCIMUser
	PatchUser    *appidentity.PatchSCIMUser
	DeleteUser   *appidentity.DeleteSCIMUser
	ListGroups   *appidentity.ListSCIMGroups
	GetGroup     *appidentity.GetSCIMGroup
	CreateGroup  *appidentity.CreateSCIMGroup
	ReplaceGroup *appidentity.ReplaceSCIMGroup
	PatchGroup   *appidentity.PatchSCIMGroup
	DeleteGroup  *appidentity.DeleteSCIMGroup
}

// api serves the operations of the SCIM API. Every request that reaches an
// operation carries an authenticated SCIM token in its context.
type api struct {
	h   Handlers
	log *slog.Logger
}

var _ ServerInterface = (*api)(nil)

// Mount serves the SCIM API on edge: every request under BaseURL, routed or
// not, answered as a SCIM error when it fails, and each operation behind the
// authentication of its SCIM token.
func Mount(edge *flaghttp.Server, h Handlers) {
	a := &api{h: h, log: edge.Logger()}
	mux := http.NewServeMux()
	HandlerWithOptions(a, StdHTTPServerOptions{BaseURL: BaseURL, BaseRouter: mux, ErrorHandlerFunc: a.parameterError})
	// The body limit is judged while it is read, so a SCIM client is told in
	// its own error document.
	edge.Handle(BaseURL+"/", a.guard(mux), flaghttp.MaxBodyBytes(maxBody), flaghttp.LimitBodyWhileReading())
}

type callerKey struct{}

// guard answers the paths and methods the API does not serve, authenticates
// the SCIM token, and hands the request to the routes. A panic is answered
// as an internal error in a SCIM document.
func (a *api) guard(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer a.recoverInternal(w, r)
		_, pattern := mux.Handler(r)
		if pattern == "" {
			if allowed := allowedMethods(r, mux); len(allowed) > 0 {
				w.Header().Set("Allow", strings.Join(allowed, ", "))
				writeError(w, http.StatusMethodNotAllowed, "", r.Method+" is not allowed here; allowed: "+strings.Join(allowed, ", "))
				return
			}
			writeError(w, http.StatusNotFound, "", "no SCIM resource at "+r.URL.Path)
			return
		}
		caller, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

// allowedMethods lists the methods the routes serve at r's path.
func allowedMethods(r *http.Request, mux *http.ServeMux) []string {
	var allowed []string
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "" {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

// authenticate resolves the request's bearer SCIM token, or answers and
// reports false. Anything presented counts against the address's throttle
// until it is found to be a token: a request with no bearer is one more guess
// at nothing, and answered alike.
func (a *api) authenticate(w http.ResponseWriter, r *http.Request) (appidentity.SCIMCaller, bool) {
	scheme, value, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		value = ""
	}
	caller, err := a.h.Authenticate.Handle(r.Context(), strings.TrimSpace(value), flaghttp.ClientOf(r.Context()).IP)
	if err != nil {
		a.fail(w, r, err)
		return appidentity.SCIMCaller{}, false
	}
	return caller, true
}

func callerOf(r *http.Request) appidentity.SCIMCaller {
	c, _ := r.Context().Value(callerKey{}).(appidentity.SCIMCaller)
	return c
}

func requestOf(r *http.Request) appidentity.SCIMRequest {
	return appidentity.SCIMRequest{Caller: callerOf(r), RequestID: flaghttp.RequestIDOf(r.Context())}
}

func (a *api) parameterError(w http.ResponseWriter, _ *http.Request, err error) {
	var format *InvalidParamFormatError
	if errors.As(err, &format) && format.ParamName == "id" {
		// An identifier that is none names no resource.
		writeError(w, http.StatusNotFound, "", "no resource of this provider has this id")
		return
	}
	writeError(w, http.StatusBadRequest, "invalidValue", err.Error())
}

func (a *api) recoverInternal(w http.ResponseWriter, r *http.Request) {
	v := recover()
	if v == nil {
		return
	}
	if v == http.ErrAbortHandler { //nolint:errorlint // re-panicked as net/http expects
		panic(v)
	}
	a.log.ErrorContext(r.Context(), "SCIM API handler panicked", "panic", v)
	writeError(w, http.StatusInternalServerError, "", "internal error")
}

// writeError answers with a SCIM error document. scimType is empty for a
// refusal RFC 7644 does not name (404, 405, 413, 429, 500, a conflict).
func writeError(w http.ResponseWriter, status int, scimType, detail string) {
	e := Error{Schemas: []string{errorSchema}, Status: strconv.Itoa(status)}
	if detail != "" {
		e.Detail = &detail
	}
	if scimType != "" {
		e.ScimType = &scimType
	}
	write(w, status, e)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", mediaType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// scimStatuses are the statuses RFC 7644 pairs with the scimTypes the
// instance answers.
var scimStatuses = map[string]int{
	"invalidFilter": http.StatusBadRequest, "invalidSyntax": http.StatusBadRequest, "invalidValue": http.StatusBadRequest,
	"mutability": http.StatusBadRequest, "noTarget": http.StatusBadRequest, "uniqueness": http.StatusConflict,
}

// fail answers err as the SCIM refusal it stands for, in one place.
func (a *api) fail(w http.ResponseWriter, r *http.Request, err error) {
	var (
		scimErr   *identity.ScimError
		tooLarge  *http.MaxBytesError
		appErr    *apperrors.Error
		isAppErr  = errors.As(err, &appErr)
		isScimErr = errors.As(err, &scimErr)
	)
	switch {
	case isScimErr:
		writeError(w, scimStatuses[scimErr.Type], scimErr.Type, scimErr.Detail)
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "", "the body has more than 64 KiB")
	case errors.Is(err, appidentity.ErrEmailTaken):
		writeError(w, http.StatusConflict, "uniqueness", "a member with this email exists and cannot be taken over by this provider")
	case errors.Is(err, appidentity.ErrExternalIDTaken):
		writeError(w, http.StatusConflict, "uniqueness", "another member of this provider holds this externalId")
	case errors.Is(err, appidentity.ErrGroupNameTaken), errors.Is(err, appidentity.ErrGroupExternalIDTaken):
		writeError(w, http.StatusConflict, "uniqueness", appErr.Detail)
	case !isAppErr:
		a.log.ErrorContext(r.Context(), "SCIM request failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "", "internal error")
	case appErr.Kind == apperrors.KindUnauthenticated:
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "invalidCredentials", "a valid SCIM token is required in Authorization: Bearer")
	case appErr.Kind == apperrors.KindRateLimited:
		w.Header().Set("Retry-After", strconv.Itoa(int(appErr.RetryAfter.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "", appErr.Detail)
	case appErr.Kind == apperrors.KindNotFound:
		writeError(w, http.StatusNotFound, "", appErr.Detail)
	case appErr.Kind == apperrors.KindConflict:
		writeError(w, http.StatusConflict, "", appErr.Detail)
	case appErr.Kind == apperrors.KindInvalid:
		writeError(w, http.StatusBadRequest, "invalidValue", appErr.Detail+violationDetails(appErr))
	default:
		a.log.ErrorContext(r.Context(), "SCIM request failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "", "internal error")
	}
}

// violationDetails words the fields an invalid input names, after its detail.
func violationDetails(e *apperrors.Error) string {
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Detail)
	}
	if len(out) == 0 {
		return ""
	}
	return ": " + strings.Join(out, "; ")
}

// readBody reads a request's body, which the edge bounds.
func readBody(r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, err
		}
		return nil, &identity.ScimError{Type: "invalidSyntax", Detail: "the body could not be read"}
	}
	return raw, nil
}

func decode(r *http.Request, into any) error {
	raw, err := readBody(r)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, into); err != nil {
		return &identity.ScimError{Type: "invalidSyntax", Detail: "the body is not valid: " + err.Error()}
	}
	return nil
}

func memberID(u UserID) identity.MemberID { return id.FromBytes[identity.Member]([16]byte(u)) }

// ----- Discovery.

func yes() *bool { return new(true) }
func no() *bool  { return new(false) }

// GetServiceProviderConfig answers what the server supports.
func (a *api) GetServiceProviderConfig(w http.ResponseWriter, _ *http.Request) {
	type supported = struct {
		Supported *bool `json:"supported,omitempty"`
	}
	write(w, http.StatusOK, ServiceProviderConfig{
		Schemas: &[]string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		Patch:   &supported{Supported: yes()},
		Filter: &struct {
			MaxResults *int  `json:"maxResults,omitempty"`
			Supported  *bool `json:"supported,omitempty"`
		}{Supported: yes(), MaxResults: new(defaultCount)},
		Bulk: &supported{Supported: no()}, ChangePassword: &supported{Supported: no()}, Sort: &supported{Supported: no()},
		AuthenticationSchemes: &[]struct {
			Description *string `json:"description,omitempty"`
			Name        *string `json:"name,omitempty"`
			Type        *string `json:"type,omitempty"`
		}{{Type: new("oauthbearertoken"), Name: new("SCIM token"), Description: new("A SCIM token in Authorization: Bearer")}},
	})
}

// ListResourceTypes answers the resource types served.
func (a *api) ListResourceTypes(w http.ResponseWriter, _ *http.Request) {
	resourceType := []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}
	resources := []ResourceType{{
		Schemas: &resourceType, Id: new("User"), Name: new("User"), Endpoint: new("/Users"), Schema: new(identity.UserSchema),
	}, {
		Schemas: &resourceType, Id: new("Group"), Name: new("Group"), Endpoint: new("/Groups"), Schema: new(identity.GroupSchema),
	}}
	write(w, http.StatusOK, ResourceTypeListResponse{
		Schemas: []string{listSchema}, TotalResults: len(resources), StartIndex: new(1), ItemsPerPage: new(len(resources)), Resources: resources,
	})
}

type schemaAttribute = struct {
	MultiValued *bool   `json:"multiValued,omitempty"`
	Mutability  *string `json:"mutability,omitempty"`
	Name        *string `json:"name,omitempty"`
	Type        *string `json:"type,omitempty"`
}

func attribute(name, typ string, multi bool, mutability string) schemaAttribute {
	return schemaAttribute{Name: &name, Type: &typ, MultiValued: &multi, Mutability: &mutability}
}

// ListSchemas answers the schemas of the resource types served.
func (a *api) ListSchemas(w http.ResponseWriter, _ *http.Request) {
	attrs := []schemaAttribute{
		attribute("userName", "string", false, "readWrite"), attribute("externalId", "string", false, "readWrite"),
		attribute("displayName", "string", false, "readWrite"), attribute("emails", "complex", true, "readWrite"),
		attribute("active", "boolean", false, "readWrite"), attribute("groups", "complex", true, "readOnly"),
	}
	groupAttrs := []schemaAttribute{
		attribute("displayName", "string", false, "readWrite"), attribute("externalId", "string", false, "readWrite"),
		attribute("members", "complex", true, "readWrite"),
	}
	resources := []Schema{{
		Id: new(identity.UserSchema), Name: new("User"), Description: new("A member of the instance, provisioned by SCIM."),
		Attributes: &attrs,
	}, {
		Id: new(identity.GroupSchema), Name: new("Group"), Description: new("A group of the provider, which its group mappings may map to access."),
		Attributes: &groupAttrs,
	}}
	write(w, http.StatusOK, SchemaListResponse{
		Schemas: []string{listSchema}, TotalResults: len(resources), StartIndex: new(1), ItemsPerPage: new(len(resources)), Resources: resources,
	})
}

// ----- Users.

// userOut is m as /Users answers it.
func userOut(m appidentity.ProvisionedMember) User {
	uid := uuid.UUID(m.ID.Bytes())
	out := User{
		Schemas: &[]string{identity.UserSchema}, Id: &uid, UserName: &m.Email, DisplayName: &m.DisplayName,
		Emails: &[]Email{{Value: &m.Email, Primary: yes()}}, Active: &m.Active, Groups: groupRefs(m.Groups),
		Meta: &Meta{ResourceType: new("User"), Created: &m.CreatedAt, Location: new(BaseURL + "/Users/" + uid.String())},
	}
	if m.ExternalID != "" {
		out.ExternalId = &m.ExternalID
	}
	return out
}

// attributesOf reads the writable attributes a User body states.
func attributesOf(u User) identity.UserAttributes {
	a := identity.UserAttributes{UserName: u.UserName, DisplayName: u.DisplayName, ExternalID: u.ExternalId, Active: u.Active}
	if u.Emails != nil {
		for _, e := range *u.Emails {
			if e.Value != nil {
				a.Emails = append(a.Emails, identity.Email{Value: *e.Value, Primary: e.Primary != nil && *e.Primary})
			}
		}
	}
	if u.Name != nil {
		a.GivenName, a.FamilyName = u.Name.GivenName, u.Name.FamilyName
	}
	return a
}

// ListUsers answers a page of the members the token's provider provisions.
func (a *api) ListUsers(w http.ResponseWriter, r *http.Request, params ListUsersParams) {
	startIndex, count := 1, defaultCount
	if params.StartIndex != nil {
		startIndex = max(*params.StartIndex, 1)
	}
	if params.Count != nil {
		count = min(max(*params.Count, 0), defaultCount)
	}
	raw := ""
	if params.Filter != nil {
		raw = *params.Filter
	}
	page, err := a.h.ListUsers.Handle(r.Context(), callerOf(r), raw, startIndex, count)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resources := make([]User, len(page.Members))
	for i, m := range page.Members {
		resources[i] = userOut(m)
	}
	write(w, http.StatusOK, UserListResponse{
		Schemas: []string{listSchema}, TotalResults: page.Total, StartIndex: &startIndex, ItemsPerPage: new(len(resources)),
		Resources: slices.Clip(resources),
	})
}

// GetUser answers one member.
func (a *api) GetUser(w http.ResponseWriter, r *http.Request, user UserID) {
	m, err := a.h.GetUser.Handle(r.Context(), callerOf(r), memberID(user))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, userOut(m))
}

// CreateUser provisions or adopts a member.
func (a *api) CreateUser(w http.ResponseWriter, r *http.Request) {
	var body User
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	m, err := a.h.CreateUser.Handle(r.Context(), requestOf(r), attributesOf(body))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", BaseURL+"/Users/"+uuid.UUID(m.ID.Bytes()).String())
	write(w, http.StatusCreated, userOut(m))
}

// ReplaceUser replaces a member's attributes.
func (a *api) ReplaceUser(w http.ResponseWriter, r *http.Request, user UserID) {
	var body User
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	m, err := a.h.ReplaceUser.Handle(r.Context(), requestOf(r), memberID(user), attributesOf(body))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, userOut(m))
}

// PatchUser applies a PatchOp to a member.
func (a *api) PatchUser(w http.ResponseWriter, r *http.Request, user UserID) {
	var body PatchOp
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	m, err := a.h.PatchUser.Handle(r.Context(), requestOf(r), memberID(user), patchOperations(body))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, userOut(m))
}

// patchOperations reads the operations of a PatchOp.
func patchOperations(body PatchOp) []identity.PatchOperation {
	ops := make([]identity.PatchOperation, len(body.Operations))
	for i, o := range body.Operations {
		ops[i] = identity.PatchOperation{Op: string(o.Op), Value: o.Value}
		if o.Path != nil {
			ops[i].Path = *o.Path
		}
	}
	return ops
}

// DeleteUser deactivates a member.
func (a *api) DeleteUser(w http.ResponseWriter, r *http.Request, user UserID) {
	if err := a.h.DeleteUser.Handle(r.Context(), requestOf(r), memberID(user)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
