package http

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// problemType prefixes a problem's code to form its type URI.
const problemType = "urn:flagnetic:problem:"

// kinds is the one mapping from an error kind to what every API answers. The
// title is fixed per kind; the code says which problem it is.
var kinds = map[apperrors.Kind]struct {
	status int
	title  string
}{
	apperrors.KindInvalid:            {http.StatusUnprocessableEntity, "Invalid"},
	apperrors.KindNotFound:           {http.StatusNotFound, "Not found"},
	apperrors.KindConflict:           {http.StatusConflict, "Conflict"},
	apperrors.KindForbidden:          {http.StatusForbidden, "Forbidden"},
	apperrors.KindUnauthenticated:    {http.StatusUnauthorized, "Unauthenticated"},
	apperrors.KindPreconditionFailed: {http.StatusPreconditionFailed, "Precondition failed"},
	apperrors.KindGone:               {http.StatusGone, "Gone"},
	apperrors.KindRateLimited:        {http.StatusTooManyRequests, "Too many requests"},
	apperrors.KindUnavailable:        {http.StatusServiceUnavailable, "Unavailable"},
}

// problem is a problem document before it is written.
type problem struct {
	status     int
	code       string
	title      string
	detail     string
	violations []apperrors.Violation
	extensions map[string]any
	// retryAfter, when positive, is written as Retry-After and retry_after.
	retryAfter time.Duration
}

// adapterProblem is a problem only the HTTP adapter answers, about the HTTP
// request itself rather than what it asks for.
type adapterProblem struct{ problem }

func (p *adapterProblem) Error() string { return p.code + ": " + p.detail }

// InvalidRequest is answered 400 invalid_request: a request the adapter cannot
// decode, such as a body that is not JSON.
func InvalidRequest(detail string) error {
	return &adapterProblem{problem{status: http.StatusBadRequest, code: "invalid_request", title: "Invalid request", detail: detail}}
}

// UnsupportedMediaType is answered 415 unsupported_media_type: a body sent
// as a media type the operation does not accept.
func UnsupportedMediaType(detail string) error {
	return &adapterProblem{problem{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", title: "Unsupported media type", detail: detail}}
}

// PreconditionRequired is answered 428 precondition_required: a write that
// must name the version it read and does not.
func PreconditionRequired(detail string) error {
	return &adapterProblem{problem{status: http.StatusPreconditionRequired, code: "precondition_required", title: "Precondition required", detail: detail}}
}

func notFoundProblem() problem {
	return problem{status: http.StatusNotFound, code: "not_found", title: "Not found", detail: "no route matches this path"}
}

func methodNotAllowedProblem() problem {
	return problem{status: http.StatusMethodNotAllowed, code: "method_not_allowed", title: "Method not allowed", detail: "this path does not accept this method"}
}

// internalProblem tells the client nothing but that the server failed; the
// cause is in the log under the request ID.
func internalProblem() problem {
	return problem{status: http.StatusInternalServerError, code: "internal", title: "Internal error"}
}

func requestTooLargeProblem(limit int64) problem {
	return problem{status: http.StatusRequestEntityTooLarge, code: "request_too_large", title: "Request too large",
		detail: "the request body is larger than " + strconv.FormatInt(limit, 10) + " bytes"}
}

func requestTimeoutProblem() problem {
	return problem{status: http.StatusRequestTimeout, code: "request_timeout", title: "Request timeout",
		detail: "the request body did not arrive in time"}
}

// problemOf resolves err into the problem every API answers it with. A body
// read past its limit or its deadline is the adapter's own problem, whichever
// handler hands the error on, unless the error has a kind of its own.
func problemOf(err error) problem {
	var (
		ap          *adapterProblem
		tooLarge    *http.MaxBytesError
		bodyTimeout *bodyTimeoutError
	)
	if errors.As(err, &ap) {
		return ap.problem
	}
	if e, ok := apperrors.From(err); ok {
		if k, ok := kinds[e.Kind]; ok {
			p := problem{
				status: k.status, code: e.Code, title: k.title, detail: e.Detail,
				violations: e.Violations, extensions: e.Extensions,
			}
			if e.Kind == apperrors.KindRateLimited || e.Kind == apperrors.KindUnavailable {
				p.retryAfter = e.RetryAfter
			}
			return p
		}
	}
	switch {
	case errors.As(err, &tooLarge):
		return requestTooLargeProblem(tooLarge.Limit)
	case errors.As(err, &bodyTimeout):
		return requestTimeoutProblem()
	}
	return internalProblem()
}

// statusClientClosedRequest is the status the access log and the metrics
// record for a request its client canceled before it was answered. No client
// ever receives it: it is the conventional status proxies log for such a
// request, outside the range of a server failure.
const statusClientClosedRequest = 499

// canceledByClient reports whether err is r ending because its client went
// away: err is a cancellation, and the context net/http served r under was
// canceled with no cause of its own, which is how net/http ends the context
// of a request whose connection closed. The context r carries now is not
// judged, since a handler may have derived it and cancelled it itself. A
// server deadline, a handler's own cancel, and the replica ending its
// requests when it stops, which gives a cause, are failures of the server
// instead.
func canceledByClient(r *http.Request, err error) bool {
	st := stateOf(r.Context())
	return st != nil && st.served != nil && errors.Is(err, context.Canceled) &&
		context.Cause(st.served) == context.Canceled //nolint:errorlint // a cause of its own may wrap Canceled and still be the server's
}

// WriteError answers err as an RFC 9457 problem document. An error of a kind
// is answered with the kind's status and the error's code, detail, field
// violations and extensions; any other error is 500 internal, with no detail
// for the client. Every 5xx is logged at error with its cause, so the request
// ID a client reports leads to it.
//
// A request its client canceled is answered nothing, since nobody is left to
// read it, and is no failure of the server: it is logged at debug and
// recorded as 499, so that neither the error log nor the 5xx count operators
// alert on grows with every browser that navigates away.
func (s *Server) WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if canceledByClient(r, err) {
		if st := stateOf(r.Context()); st != nil {
			st.canceled = true
		}
		s.log.DebugContext(r.Context(), "request canceled by the client", "error", err.Error())
		return
	}
	p := problemOf(err)
	if p.status >= http.StatusInternalServerError {
		s.log.ErrorContext(r.Context(), "request failed", "status", p.status, "error", err.Error())
	}
	s.writeProblem(w, r, p)
}

type problemViolation struct {
	Pointer string         `json:"pointer"`
	Code    string         `json:"code"`
	Detail  string         `json:"detail"`
	Params  map[string]any `json:"params,omitempty"`
}

func (s *Server) writeProblem(w http.ResponseWriter, r *http.Request, p problem) {
	// Extensions go first so that no extension can replace a standard member.
	members := make(map[string]any, len(p.extensions)+8)
	for k, v := range p.extensions {
		members[k] = v
	}
	members["type"] = problemType + p.code
	members["title"] = p.title
	members["status"] = p.status
	members["code"] = p.code
	members["request_id"] = RequestIDOf(r.Context())
	if p.detail != "" {
		members["detail"] = p.detail
	}
	if len(p.violations) > 0 {
		vs := make([]problemViolation, len(p.violations))
		for i, v := range p.violations {
			vs[i] = problemViolation(v)
		}
		members["errors"] = vs
	}
	if p.retryAfter > 0 {
		// Whole seconds, rounded up, so a client never retries early.
		seconds := int(math.Ceil(p.retryAfter.Seconds()))
		members["retry_after"] = seconds
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	if err := json.NewEncoder(w).Encode(members); err != nil {
		s.log.WarnContext(r.Context(), "writing a problem failed", "error", err.Error())
	}
}

// serveUnmatched answers a request no mounted route matches: 405 with the
// methods the path accepts when the path exists, 404 when it does not, and
// the mux's own redirect when the path only needs cleaning.
func (s *Server) serveUnmatched(w http.ResponseWriter, r *http.Request) {
	probe := &headerProbe{header: http.Header{}}
	s.mux.ServeHTTP(probe, r)
	switch probe.status {
	case http.StatusMethodNotAllowed:
		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		s.writeProblem(w, r, methodNotAllowedProblem())
	case http.StatusNotFound:
		s.writeProblem(w, r, notFoundProblem())
	default:
		for k, v := range probe.header {
			w.Header()[k] = v
		}
		w.WriteHeader(probe.status)
	}
}

// headerProbe records the status and headers the mux answers an unmatched
// request with, and discards its plain-text body.
type headerProbe struct {
	header http.Header
	status int
}

func (p *headerProbe) Header() http.Header { return p.header }

func (p *headerProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}

func (p *headerProbe) Write(b []byte) (int, error) {
	p.WriteHeader(http.StatusOK)
	return len(b), nil
}
