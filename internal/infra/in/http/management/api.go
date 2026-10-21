// Package management is the management API, served under /api/v1: the
// server generated from api/management.yaml, the permission seam in front of
// every operation, and the handlers translating requests into the
// application's commands and queries.
package management

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// BaseURL is where the management API is served.
const BaseURL = "/api/v1"

// Handlers are the use cases the management API serves.
type Handlers struct {
	AuthenticateToken  *appaccess.AuthenticateToken
	ProjectPermissions *appaccess.ProjectPermissions
	WorkingProjects    *appaccess.WorkingProjects

	FindProject   *appworkspace.FindProject
	ListProjects  *appworkspace.ListProjects
	CreateProject *appworkspace.CreateProject
	UpdateProject *appworkspace.UpdateProject

	ListEnvironments  *appworkspace.List[workspace.Environment]
	GetEnvironment    *appworkspace.Get[workspace.Environment]
	CreateEnvironment *appworkspace.CreateEnvironment
	UpdateEnvironment *appworkspace.UpdateEnvironment
	DeleteEnvironment *appworkspace.DeleteEnvironment

	ListUnitTypes  *appworkspace.List[workspace.UnitType]
	GetUnitType    *appworkspace.Get[workspace.UnitType]
	CreateUnitType *appworkspace.CreateUnitType
	UpdateUnitType *appworkspace.UpdateUnitType
	DeleteUnitType *appworkspace.DeleteUnitType

	ListAttributes  *appworkspace.List[workspace.Attribute]
	GetAttribute    *appworkspace.Get[workspace.Attribute]
	CreateAttribute *appworkspace.CreateAttribute
	UpdateAttribute *appworkspace.UpdateAttribute
	DeleteAttribute *appworkspace.DeleteAttribute

	ListAuditEntries *appgovernance.ListAuditEntries
	GetAuditEntry    *appgovernance.GetAuditEntry
}

// api serves every operation of the document.
type api struct {
	edge     *flaghttp.Server
	h        Handlers
	refusals *prometheus.CounterVec
}

var _ StrictServerInterface = (*api)(nil)

// Mount serves the management API on edge, each operation behind the
// permission seam, and registers its metrics on reg.
func Mount(edge *flaghttp.Server, h Handlers, reg prometheus.Registerer) {
	a := &api{edge: edge, h: h, refusals: newRefusals(reg)}
	invalidRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		edge.WriteError(w, r, flaghttp.InvalidRequest(err.Error()))
	}
	strict := NewStrictHandlerWithOptions(a, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  invalidRequest,
		ResponseErrorHandlerFunc: edge.WriteError,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       router{a},
		ErrorHandlerFunc: invalidRequest,
	})
}

// newRefusals returns the counter of the seam's refusals, registered on reg.
func newRefusals(reg prometheus.Registerer) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "management", Name: "refusals_total",
		Help: "Requests the permission seam refused, by operation and problem code.",
	}, []string{"operation", "code"})
	reg.MustRegister(c)
	return c
}

// router mounts each generated route on the edge behind the seam of its
// operation, so the seam runs before any parameter or body is read, and the
// edge logs and counts each operation by its own pattern.
type router struct{ a *api }

func (rt router) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	method, path, _ := strings.Cut(pattern, " ")
	op, ok := operations.Table[method+" "+strings.TrimPrefix(path, BaseURL)]
	if !ok {
		// The table is generated from the document the routes are, so a
		// route without an entry is a stale build, refused at start.
		panic("management: no operation table entry for " + pattern + "; run task generate")
	}
	rt.a.edge.Handle(pattern, rt.a.seam(op, http.HandlerFunc(h)))
}

func (rt router) ServeHTTP(w http.ResponseWriter, r *http.Request) { rt.a.edge.ServeHTTP(w, r) }

type (
	callerKey  struct{}
	projectKey struct{}
)

// callerOf returns the caller the seam authenticated for the request ctx
// belongs to.
func callerOf(ctx context.Context) access.Caller {
	c, _ := ctx.Value(callerKey{}).(access.Caller)
	return c
}

// addressed is the project an operation addresses, as the seam found it,
// with what the caller holds there.
type addressed struct {
	project     workspace.Project
	permissions []access.Permission
}

// projectOf returns the project addressed by the request ctx belongs to, as
// the seam found it before judging the request.
func projectOf(ctx context.Context) addressed {
	p, _ := ctx.Value(projectKey{}).(addressed)
	return p
}

// requestOf returns who makes the change a request ctx belongs to asks for,
// for its audit entries.
func requestOf(ctx context.Context) governance.Request {
	return governance.Request{Actor: appaccess.ActorOf(callerOf(ctx)), ID: flaghttp.RequestIDOf(ctx)}
}

// seam judges a request to op before its handler runs: it authenticates the
// caller and authorizes them for op, and refuses the request otherwise. The
// caller then travels in the request's context. A credential is
// authenticated even for a public operation, so a bad one is refused there
// as everywhere; only the absence of any credential is how anyone calls it.
func (a *api) seam(op access.Operation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		caller, err := a.authenticate(r)
		var in *addressed
		switch {
		case err == nil && op.InProject():
			in, err = a.address(ctx, caller, r.PathValue("project"))
			if err == nil {
				err = access.Authorize(caller, op, &access.ProjectAccess{Project: in.project.ID, Permissions: in.permissions})
			}
		case err == nil:
			err = access.Authorize(caller, op, nil)
		case errors.Is(err, errUnauthenticated) && op.Requirement == access.RequirePublic:
			err = nil
		}
		if err != nil {
			a.refuse(w, r, op, err)
			return
		}
		ctx = context.WithValue(ctx, callerKey{}, caller)
		if in != nil {
			ctx = context.WithValue(ctx, projectKey{}, *in)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// address finds the project with key and what caller holds there; an
// unknown project is the zero one, holding nothing.
func (a *api) address(ctx context.Context, caller access.Caller, key string) (*addressed, error) {
	p, err := a.h.FindProject.Handle(ctx, key)
	if errors.Is(err, appworkspace.ErrNotFound) {
		return &addressed{}, nil
	}
	if err != nil {
		return nil, err
	}
	perms, err := a.h.ProjectPermissions.Handle(ctx, caller, p.ID)
	if err != nil {
		return nil, err
	}
	return &addressed{project: p, permissions: perms}, nil
}

// errUnauthenticated answers a request that presents no credential.
var errUnauthenticated = apperrors.Unauthenticated("unauthenticated",
	"authentication is required: an API token in the Authorization header as Bearer")

// authenticate resolves the request's credential. Any Authorization header
// is an attempt at an API token presented as Bearer, so a malformed one is refused rather than
// ignored; a token anywhere else, such as the query string, is never read,
// since URLs end up in logs and browser histories.
func (a *api) authenticate(r *http.Request) (access.Caller, error) {
	if _, present := r.Header["Authorization"]; !present {
		return access.Caller{}, errUnauthenticated
	}
	scheme, value, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	value = strings.TrimSpace(value)
	if !strings.EqualFold(scheme, "Bearer") || value == "" {
		return access.Caller{}, access.ErrInvalidToken
	}
	return a.h.AuthenticateToken.Handle(r.Context(), value)
}

// refuse answers a request the seam turns away, and counts a refusal by the
// operation and its code. A refused credential is named in WWW-Authenticate,
// so tooling tells an expired token from a revoked one without the body.
func (a *api) refuse(w http.ResponseWriter, r *http.Request, op access.Operation, err error) {
	if e, ok := apperrors.From(err); ok && (e.Kind == apperrors.KindUnauthenticated || e.Kind == apperrors.KindForbidden ||
		e.Kind == apperrors.KindNotFound) {
		if e.Kind == apperrors.KindUnauthenticated {
			challenge := "Bearer"
			if e.Code != errUnauthenticated.Code {
				challenge = `Bearer error="invalid_token", error_description="` + e.Code + `"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
		}
		a.refusals.WithLabelValues(op.Name, e.Code).Inc()
	}
	a.edge.WriteError(w, r, err)
}
