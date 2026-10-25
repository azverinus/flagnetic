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
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appevents "github.com/azverinus/flagnetic/internal/app/events"
	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// BaseURL is where the management API is served.
const BaseURL = "/api/v1"

// Handlers are the use cases the management API serves.
type Handlers struct {
	AuthenticateToken   *appaccess.AuthenticateToken
	AuthenticateSession *appaccess.AuthenticateSession
	SignIn              *appaccess.SignIn
	SignOut             *appaccess.SignOut
	ListSessions        *appaccess.ListSessions
	RevokeSessions      *appaccess.RevokeSessions
	GetAuthSettings     *appaccess.GetAuthSettings
	UpdateAuthSettings  *appaccess.UpdateAuthSettings
	ListMembers         *appaccess.ListMembers
	GetMember           *appaccess.GetMember
	UpdateMember        *appaccess.UpdateMember
	UpdateMe            *appaccess.UpdateMe
	ChangeMyPassword    *appaccess.ChangeMyPassword
	DeactivateMember    *appaccess.DeactivateMember
	ReactivateMember    *appaccess.ReactivateMember
	InviteMember        *appaccess.InviteMember
	ReissueInvitation   *appaccess.ReissueInvitation
	GetInvitation       *appaccess.GetInvitation
	AcceptInvitation    *appaccess.AcceptInvitation
	ListPersonalTokens  *appaccess.ListPersonalTokens
	CreatePersonalToken *appaccess.CreatePersonalToken
	RevokePersonalToken *appaccess.RevokePersonalToken
	ListProjectMembers  *appaccess.ListProjectMembers
	CountProjectMembers *appaccess.CountProjectMembers
	ListGrants          *appaccess.ListGrants
	CreateGrant         *appaccess.CreateGrant
	DeleteGrant         *appaccess.DeleteGrant
	ListProjectTokens   *appaccess.ListProjectTokens
	CreateProjectToken  *appaccess.CreateProjectToken
	RevokeProjectToken  *appaccess.RevokeProjectToken
	ProjectPermissions  *appaccess.ProjectPermissions
	WorkingProjects     *appaccess.WorkingProjects

	FindProject   *appworkspace.FindProject
	ListProjects  *appworkspace.ListProjects
	CreateProject *appworkspace.CreateProject
	UpdateProject *appworkspace.UpdateProject

	ListEnvironments         *appworkspace.List[workspace.Environment]
	GetEnvironment           *appworkspace.Get[workspace.Environment]
	CreateEnvironment        *appworkspace.CreateEnvironment
	UpdateEnvironment        *appworkspace.UpdateEnvironment
	SetEnvironmentProtection *appworkspace.SetEnvironmentProtection
	DeleteEnvironment        *appworkspace.DeleteEnvironment

	ListUnitTypes  *appworkspace.ListUsed[workspace.UnitType]
	GetUnitType    *appworkspace.Get[workspace.UnitType]
	CreateUnitType *appworkspace.CreateUnitType
	UpdateUnitType *appworkspace.UpdateUnitType
	DeleteUnitType *appworkspace.DeleteUnitType

	ListAttributes  *appworkspace.ListUsed[workspace.Attribute]
	GetAttribute    *appworkspace.Get[workspace.Attribute]
	CreateAttribute *appworkspace.CreateAttribute
	UpdateAttribute *appworkspace.UpdateAttribute
	DeleteAttribute *appworkspace.DeleteAttribute

	ListFlags   *appflags.ListFlags
	CreateFlag  *appflags.CreateFlag
	GetFlag     *appflags.GetFlag
	UpdateFlag  *appflags.UpdateFlag
	ArchiveFlag *appflags.ArchiveFlag
	RestoreFlag *appflags.RestoreFlag
	DeleteFlag  *appflags.DeleteFlag

	GetFlagConfiguration *appflags.GetFlagConfiguration
	ReplaceConfiguration *appflags.ReplaceConfiguration
	KillFlag             *appflags.KillFlag
	SetOverride          *appflags.SetOverride
	RemoveOverride       *appflags.RemoveOverride
	RegenerateSalt       *appflags.RegenerateSalt
	RevertConfiguration  *appflags.RevertConfiguration
	CopyConfiguration    *appflags.CopyConfiguration
	Evaluate             *appdelivery.Evaluate
	ReadRuleset          *appdelivery.ReadRuleset
	ListSDKInventory     *appdelivery.ListSDKInventory

	ListDiscoveredAttributes   *appdelivery.ListDiscoveredAttributes
	DismissDiscoveredAttribute *appdelivery.DismissDiscoveredAttribute
	DeclareDiscoveredAttribute *appdelivery.DeclareDiscoveredAttribute

	ListSDKKeys  *appdelivery.ListSDKKeys
	CreateSDKKey *appdelivery.CreateSDKKey
	UpdateSDKKey *appdelivery.UpdateSDKKey
	RotateSDKKey *appdelivery.RotateSDKKey
	RevokeSDKKey *appdelivery.RevokeSDKKey

	ListExperiments           *appexperiments.ListExperiments
	CreateExperiment          *appexperiments.CreateExperiment
	GetExperiment             *appexperiments.GetExperiment
	UpdateExperiment          *appexperiments.UpdateExperiment
	DeleteExperiment          *appexperiments.DeleteExperiment
	RegenerateExperimentSalts *appexperiments.RegenerateSalts
	CloneExperiment           *appexperiments.CloneExperiment
	GetExperimentAssignment   *appexperiments.GetAssignment
	CheckHashUniformity       *appexperiments.CheckUniformity
	StartExperiment           *appexperiments.StartExperiment
	StopExperiment            *appexperiments.StopExperiment
	ArchiveExperiment         *appexperiments.ArchiveExperiment
	SetExperimentDecisionNote *appexperiments.SetDecisionNote

	GetExperimentExposures *appevents.GetExperimentExposures
	GetExposureTimeline    *appevents.GetExposureTimeline
	GetConflicts           *appevents.GetConflicts
	GetFlagEvaluations     *appevents.GetFlagEvaluations
	GetEventVolume         *appevents.GetEventVolume

	ListChangeRequests   *appgovernance.ListChangeRequests
	CreateChangeRequest  *appgovernance.CreateChangeRequest
	GetChangeRequest     *appgovernance.GetChangeRequest
	UpdateChangeRequest  *appgovernance.UpdateChangeRequest
	OpenChangeRequest    *appgovernance.OpenChangeRequest
	RebaseChangeRequest  *appgovernance.RebaseChangeRequest
	CancelChangeRequest  *appgovernance.CancelChangeRequest
	ApproveChangeRequest *appgovernance.ApproveChangeRequest
	RejectChangeRequest  *appgovernance.RejectChangeRequest
	ApplyChangeRequest   *appgovernance.ApplyChangeRequest
	CopyChangeRequest    *appgovernance.CopyChangeRequest
	CountWaiting         *appgovernance.CountWaitingChangeRequests

	ListWebhookEventTypes *appgovernance.ListWebhookEventTypes
	ListWebhooks          *appgovernance.ListWebhooks
	CreateWebhook         *appgovernance.CreateWebhook
	GetWebhook            *appgovernance.GetWebhook
	UpdateWebhook         *appgovernance.UpdateWebhook
	DeleteWebhook         *appgovernance.DeleteWebhook

	ListSegments      *appflags.ListSegments
	GetSegment        *appflags.GetSegment
	CreateSegment     *appflags.CreateSegment
	UpdateSegment     *appflags.UpdateSegment
	DeleteSegment     *appflags.DeleteSegment
	ListSegmentUsages *appflags.ListSegmentUsages

	ListAuditEntries *appgovernance.ListAuditEntries
	GetAuditEntry    *appgovernance.GetAuditEntry
}

// api serves every operation of the document.
type api struct {
	edge     *flaghttp.Server
	h        Handlers
	opts     Options
	origins  *http.CrossOriginProtection
	refusals *prometheus.CounterVec
}

var _ StrictServerInterface = (*api)(nil)

// Mount serves the management API on edge, each operation behind the
// permission seam, and registers its metrics on reg.
func Mount(edge *flaghttp.Server, h Handlers, reg prometheus.Registerer, opts Options) {
	a := newAPI(edge, h, newRefusals(reg), opts)
	invalidRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		edge.WriteError(w, r, flaghttp.InvalidRequest(err.Error()))
	}
	strict := NewStrictHandlerWithOptions(a, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  invalidRequest,
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { edge.WriteError(w, r, httpError(err)) },
	})
	// The links invitations hand out carry their secret in the path the
	// browser opens.
	edge.SecretPath(invitationLinkPath + "{secret}")
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       router{a},
		ErrorHandlerFunc: invalidRequest,
	})
}

// errPreconditionRequired answers a write of a versioned entity that names
// no version: a problem of the HTTP request, which carries the version in
// If-Match.
var errPreconditionRequired = flaghttp.PreconditionRequired(
	"send If-Match with the version of the entity this write is based on, as its ETag answered it")

// httpError returns err as the edge answers it: a write naming no version,
// which the application refuses once the request is validated, is the HTTP
// adapter's own 428.
func httpError(err error) error {
	if errors.Is(err, precondition.ErrRequired) {
		return errPreconditionRequired
	}
	return err
}

func newAPI(edge *flaghttp.Server, h Handlers, refusals *prometheus.CounterVec, opts Options) *api {
	origins := http.NewCrossOriginProtection()
	if opts.BaseURL != nil {
		// A proxy in front may rewrite Host, so the console's own origin is
		// trusted by name rather than judged against the request's host.
		if err := origins.AddTrustedOrigin(opts.BaseURL.Scheme + "://" + opts.BaseURL.Host); err != nil {
			panic("management: trusting the base URL's origin: " + err.Error())
		}
	}
	return &api{edge: edge, h: h, opts: opts, origins: origins, refusals: refusals}
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
	var opts []flaghttp.RouteOption
	if op.ChecksPassword {
		opts = append(opts, flaghttp.MaxBodyBytes(operations.PasswordBodyLimit))
	}
	if op.MaxBodyBytes > 0 {
		opts = append(opts, flaghttp.MaxBodyBytes(op.MaxBodyBytes))
	}
	// A path parameter called secret holds one, such as an invitation's.
	if strings.Contains(path, "{secret}") {
		opts = append(opts, flaghttp.SecretInPath())
	}
	rt.a.edge.Handle(pattern, rt.a.seam(op, http.HandlerFunc(h)), opts...)
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
	grants      []access.Grant
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
// caller, refuses a request signed in by the session cookie that comes from
// another origin, and authorizes the caller for op, and refuses the request
// otherwise. The caller then travels in the request's context. A bearer
// credential is authenticated even for a public operation, so a bad one is
// refused there as everywhere; only the absence of any credential is how
// anyone calls it.
func (a *api) seam(op operations.Entry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := flaghttp.ClientOf(r.Context()).Scheme
		ctx := context.WithValue(r.Context(), browserKey{}, browser{
			secureCookies: a.opts.secureCookies(scheme, r.Host),
			userAgent:     r.UserAgent(),
			console:       a.opts.console(scheme, r.Host),
		})
		r = r.WithContext(ctx)
		caller, err := a.authenticate(r, op.Operation)
		var in *addressed
		switch {
		case err == nil && caller.Method == access.MethodSession && a.crossOrigin(r):
			err = errCrossOrigin
		case err == nil && op.InProject():
			in, err = a.address(ctx, caller, r.PathValue("project"))
			if err == nil {
				err = access.Authorize(caller, op.Operation, &access.ProjectAccess{Project: in.project.ID, Permissions: in.permissions})
			}
		case err == nil:
			err = access.Authorize(caller, op.Operation, nil)
		case errors.Is(err, errUnauthenticated) && op.Requirement == access.RequirePublic:
			err = nil
		}
		if err == nil && op.OpensSession && !isJSON(r) {
			err = errNotJSON
		}
		if err != nil {
			a.refuse(w, r, op.Operation, err)
			return
		}
		ctx = context.WithValue(ctx, callerKey{}, caller)
		if in != nil {
			ctx = context.WithValue(ctx, projectKey{}, *in)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// crossOrigin reports whether the browser says r comes from an origin other
// than the console's. The standard check lets safe methods through, since a
// read changes nothing; a session is only ever used by the console on its
// own origin, though, so a read from another origin is refused too: what it
// answers is no other page's business. The check is therefore made as if r
// were a write.
func (a *api) crossOrigin(r *http.Request) bool {
	asWrite := r.WithContext(r.Context()) // a shallow copy, whose method alone changes
	asWrite.Method = http.MethodPost
	return a.origins.Check(asWrite) != nil
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
	held, err := a.h.ProjectPermissions.Handle(ctx, caller, p.ID)
	if err != nil {
		return nil, err
	}
	return &addressed{project: p, grants: held.Grants, permissions: held.Permissions}, nil
}

// errUnauthenticated answers a request that presents no credential.
var errUnauthenticated = apperrors.Unauthenticated("unauthenticated",
	"authentication is required: an API token in the Authorization header as Bearer, or a signed-in session")

// authenticate resolves the request's credential. Any Authorization header
// is an attempt at an API token presented as Bearer, so a malformed one is
// refused rather than ignored, and it is judged instead of any session
// cookie; a token anywhere else, such as the query string, is never read,
// since URLs end up in logs and browser histories. Without one, the session
// cookie is the credential, except for a public operation: an ended session
// must not stand between a member and signing in again. A cookie the server
// did not issue is no credential at all.
func (a *api) authenticate(r *http.Request, op access.Operation) (access.Caller, error) {
	if _, present := r.Header["Authorization"]; present {
		scheme, value, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		value = strings.TrimSpace(value)
		if !strings.EqualFold(scheme, "Bearer") || value == "" {
			return access.Caller{}, access.ErrInvalidToken
		}
		return a.h.AuthenticateToken.Handle(r.Context(), value, flaghttp.ClientOf(r.Context()).IP)
	}
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || op.Requirement == access.RequirePublic {
		return access.Caller{}, errUnauthenticated
	}
	caller, err := a.h.AuthenticateSession.Handle(r.Context(), cookie.Value)
	if errors.Is(err, appaccess.ErrNoSession) {
		return access.Caller{}, errUnauthenticated
	}
	return caller, err
}

// refuse answers a request the seam turns away, and counts a refusal by the
// operation and its code. A refused credential is named in WWW-Authenticate,
// so tooling tells an expired token from a revoked one without the body.
func (a *api) refuse(w http.ResponseWriter, r *http.Request, op access.Operation, err error) {
	if e, ok := apperrors.From(err); ok && (e.Kind == apperrors.KindUnauthenticated || e.Kind == apperrors.KindForbidden ||
		e.Kind == apperrors.KindNotFound) {
		switch {
		case e.Code == access.ErrSessionExpired.Code:
			// The browser drops the cookie of a session that has ended, so
			// it stops sending what can no longer sign anyone in.
			w.Header().Set("Set-Cookie", sessionCookie(r.Context(), ""))
			w.Header().Set("WWW-Authenticate", "Bearer")
		case e.Code == errUnauthenticated.Code:
			w.Header().Set("WWW-Authenticate", "Bearer")
		case e.Kind == apperrors.KindUnauthenticated:
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="`+e.Code+`"`)
		}
		a.refusals.WithLabelValues(op.Name, e.Code).Inc()
	}
	a.edge.WriteError(w, r, err)
}
