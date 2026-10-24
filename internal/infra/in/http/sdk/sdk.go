package sdk

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// BaseURL is where the SDK API is served.
const BaseURL = "/sdk/v1"

// Handlers are the use cases the SDK API serves.
type Handlers struct {
	Authenticate *appdelivery.AuthenticateSDKKey
	ServeRuleset *appdelivery.ServeRuleset
	WatchRuleset *appdelivery.WatchRuleset
	Evaluate     *appdelivery.EvaluateRemotely
	Ingest       *appevents.IngestEvents
	// Inventory gathers the SDKs each request comes from.
	Inventory *appdelivery.SDKInventory
}

// Options are what the SDK API runs on besides its use cases.
type Options struct {
	// Clock times the streams and the rate limits' windows.
	Clock clock.Clock
	// RateLimits are the API's limits.
	RateLimits config.RateLimits
	// Registerer receives the API's metrics.
	Registerer prometheus.Registerer
}

// operation is how the adapter guards one operation of the contract.
type operation struct {
	// endpoint is what the rate limits count the operation's requests as.
	endpoint endpoint
	// kinds are the key kinds the operation serves.
	kinds []delivery.SDKKeyKind
	// queryKey lets a client key be presented as the query parameter
	// sdk_key, for what a browser sends without headers: an EventSource,
	// and navigator.sendBeacon from a closing page.
	queryKey bool
	// oneKey refuses a request presenting a key both in Authorization and
	// in the query, rather than choosing one of two keys that may belong to
	// different environments.
	oneKey bool
	// stream marks a long-lived operation.
	stream bool
	// maxBodyBytes is the operation's body limit, after decompression; the
	// edge's default when 0.
	maxBodyBytes int64
}

// operations are the operations served so far, by the pattern the
// generated router mounts them on. An operation of the contract missing
// here is not mounted, and its path answers 404 like any unknown one.
var operations = map[string]operation{
	"GET " + BaseURL + "/ruleset": {endpoint: endpointRuleset, kinds: []delivery.SDKKeyKind{delivery.SDKKeyServer}},
	"GET " + BaseURL + "/stream": {
		endpoint: endpointStream, kinds: []delivery.SDKKeyKind{delivery.SDKKeyServer, delivery.SDKKeyClient},
		queryKey: true, stream: true,
	},
	"POST " + BaseURL + "/evaluate": {
		endpoint: endpointEvaluate, kinds: []delivery.SDKKeyKind{delivery.SDKKeyServer, delivery.SDKKeyClient},
		maxBodyBytes: maxEvaluateBody,
	},
	"POST " + BaseURL + "/events": {
		endpoint: endpointEvents, kinds: []delivery.SDKKeyKind{delivery.SDKKeyServer, delivery.SDKKeyClient},
		queryKey: true, oneKey: true, maxBodyBytes: events.MaxBatchBytes,
	},
}

// api serves the operations of the SDK API.
type api struct {
	// paths are the paths of the operations mounted, which a preflight is
	// answered on.
	paths  map[string]bool
	edge   *flaghttp.Server
	h      Handlers
	clock  clock.Clock
	limits *limiter
	events eventMetrics
}

// Mount serves the SDK API on edge: every request under BaseURL, routed or
// not, through the API's compression, and each operation behind the
// authentication of its SDK key.
func Mount(edge *flaghttp.Server, h Handlers, o Options) {
	a := &api{paths: map[string]bool{}, edge: edge, h: h, clock: o.Clock, limits: newLimiter(o.RateLimits, o.Clock, o.Registerer),
		events: newEventMetrics(o.Registerer)}
	routed := func(path string) bool { return a.paths[path] }
	edge.Wrap(BaseURL+"/", func(next http.Handler) http.Handler { return compression(edge)(cors(routed)(next)) })
	HandlerWithOptions(a, StdHTTPServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       router{a},
		ErrorHandlerFunc: a.parameterError,
	})
}

// router mounts each implemented operation on the edge behind its guard,
// so the key is judged before any parameter is read.
type router struct{ a *api }

func (rt router) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	op, ok := operations[pattern]
	if !ok {
		return
	}
	_, path, _ := strings.Cut(pattern, " ")
	if !rt.a.paths[path] {
		// cors answers a preflight on every path before it is routed; the
		// path is registered for OPTIONS as well so that a 405 lists it in
		// Allow, as a browser SDK may send it.
		rt.a.edge.Handle("OPTIONS "+path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	rt.a.paths[path] = true
	var opts []flaghttp.RouteOption
	if op.stream {
		opts = append(opts, flaghttp.Stream())
	}
	if op.maxBodyBytes > 0 {
		opts = append(opts, flaghttp.MaxBodyBytes(op.maxBodyBytes), flaghttp.LimitBodyWhileReading())
	}
	rt.a.edge.Handle(pattern, rt.a.guard(op, http.HandlerFunc(h)), opts...)
}

func (rt router) ServeHTTP(w http.ResponseWriter, r *http.Request) { rt.a.edge.ServeHTTP(w, r) }

type keyKey struct{}

// presented is the SDK key the guard authenticated for a request, with the
// value presented, which a stream checks again on each heartbeat.
type presented struct {
	key   delivery.SDKKey
	value string
}

// keyOf returns the SDK key the guard authenticated for the request ctx
// belongs to.
func keyOf(ctx context.Context) delivery.SDKKey { return presentedOf(ctx).key }

func presentedOf(ctx context.Context) presented {
	p, _ := ctx.Value(keyKey{}).(presented)
	return p
}

// guard admits a request to op: it refuses a client IP that presented too
// many refused keys before its key is looked up, authenticates the key,
// applies the key's allowed origins, refuses a key of a kind op does not
// serve, and counts the request against
// the key's limits, holding a stream slot for a stream until it ends. The
// key then travels in the request's context.
func (a *api) guard(op operation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := flaghttp.ClientOf(r.Context()).IP
		value, err := a.keyValue(op, r)
		if err == nil && value != "" {
			err = a.limits.guessing(w, op.endpoint, ip)
		}
		var key delivery.SDKKey
		if err == nil {
			key, err = a.h.Authenticate.Handle(r.Context(), value)
			if refusesPresentedKey(err) {
				a.limits.failed(ip)
			}
		}
		if err == nil {
			err = admitOrigin(w, r, key.AdmitOrigin)
		}
		if err == nil {
			err = key.CheckKind(op.kinds...)
		}
		release := func() {}
		if err == nil {
			release, err = a.limits.take(w, op.endpoint, key, ip)
		}
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		defer release()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyKey{}, presented{key: key, value: value})))
	})
}

// refusesPresentedKey reports whether err refuses a key that was presented:
// what guessing keys looks like, unlike sending none.
func refusesPresentedKey(err error) bool {
	e, ok := apperrors.From(err)
	return ok && e.Kind == apperrors.KindUnauthenticated && e.Code != delivery.ErrSDKKeyMissing.Code
}

// keyValue returns the SDK key value r presents: the bearer of its
// Authorization header, or for an operation taking one, the query
// parameter sdk_key when the header carries none. A server key in the query
// is refused whether or not the header carries a key, since it has leaked
// into a URL either way; then, for an operation taking one key only, a key
// in both places is refused.
func (a *api) keyValue(op operation, r *http.Request) (string, error) {
	value := bearer(r.Header.Get("Authorization"))
	if !op.queryKey {
		return value, nil
	}
	query := r.URL.Query().Get(queryKeyParameter)
	if err := delivery.CheckKeyInQuery(query); err != nil {
		return "", err
	}
	if op.oneKey && value != "" && query != "" {
		return "", errTwoKeys
	}
	if value == "" {
		value = query
	}
	return value, nil
}

// errTwoKeys answers a request presenting a key both in Authorization and
// in the query where only one is taken.
var errTwoKeys = flaghttp.InvalidRequest("the SDK key must be sent either in Authorization or as the query parameter " +
	queryKeyParameter + ", not both")

// queryKeyParameter is the query parameter a client key may be presented
// in.
const queryKeyParameter = "sdk_key"

// bearer returns the credential of an Authorization header of the Bearer
// scheme, or "" when the header carries none: another scheme carries no
// SDK key either.
func bearer(header string) string {
	scheme, credential, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(credential)
}

// writeError answers err as a problem. A refused key also carries the
// challenge of RFC 6750: a bare one when no key was sent, and
// invalid_token, described by the problem's code, for a key that is not
// accepted.
func (a *api) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperrors.From(err); ok && e.Kind == apperrors.KindUnauthenticated {
		challenge := "Bearer"
		if e.Code != delivery.ErrSDKKeyMissing.Code {
			challenge = `Bearer error="invalid_token", error_description="` + e.Code + `"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	a.edge.WriteError(w, r, err)
}

// schemaVersionHeader is the header naming the highest document schema an
// SDK reads.
const schemaVersionHeader = "Flagnetic-Schema-Version"

// errSchemaVersionUnsupported answers a schema the server cannot serve.
var errSchemaVersionUnsupported = flaghttp.NotAcceptable("schema_version_unsupported",
	"Flagnetic-Schema-Version must be an integer of 1 or more: the highest document schema the SDK reads")

// parameterError answers a parameter the generated router could not bind:
// a schema version sent more than once is no version the server can
// serve; anything else is a request it cannot read.
func (a *api) parameterError(w http.ResponseWriter, r *http.Request, err error) {
	var many *TooManyValuesForParamError
	if errors.As(err, &many) && many.ParamName == schemaVersionHeader {
		a.writeError(w, r, errSchemaVersionUnsupported)
		return
	}
	a.writeError(w, r, flaghttp.InvalidRequest(err.Error()))
}

// positiveInteger is a whole number of 1 or more, however many digits.
var positiveInteger = regexp.MustCompile(`^0*[1-9][0-9]*$`)

// checkSchemaVersion refuses a requested schema below 1 or unreadable. Any
// schema from 1 up is served schema 1, the only one there is: an SDK
// reading newer schemas reads the older ones too.
func checkSchemaVersion(requested *string) error {
	if requested == nil || positiveInteger.MatchString(strings.TrimSpace(*requested)) {
		return nil
	}
	return errSchemaVersionUnsupported
}

// GetRuleset answers a server key its environment's ruleset, or 304 when
// If-None-Match names its current version.
func (a *api) GetRuleset(w http.ResponseWriter, r *http.Request, params GetRulesetParams) {
	if err := checkSchemaVersion(params.FlagneticSchemaVersion); err != nil {
		a.writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), appdelivery.RulesetWait)
	defer cancel()
	rs, err := a.h.ServeRuleset.Handle(ctx, keyOf(r.Context()))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.h.Inventory.Note(keyOf(r.Context()), delivery.ParseUserAgent(r.UserAgent()), "", rs.Version)
	writeRuleset(w, r, rs)
}

// writeRuleset answers rs with its version as a strong ETag, or 304 when
// If-None-Match names it. No shared cache may hold it, since it belongs to
// one key's environment, and an SDK revalidates it on every poll.
func writeRuleset(w http.ResponseWriter, r *http.Request, rs appdelivery.Ruleset) {
	version := strconv.FormatInt(rs.Version, 10)
	h := w.Header()
	h.Set("ETag", `"`+version+`"`)
	h.Set("Cache-Control", "private, no-cache")
	h.Add("Vary", "Authorization, Accept-Encoding")
	if noneMatch(r.Header.Values("If-None-Match"), version) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rs.Document)
}

// noneMatch reports whether If-None-Match, as the values of its headers,
// names version: compared weakly (RFC 9110), so a W/ tag a proxy made of
// the strong one still matches, with * matching any version and each value
// a list of tags.
func noneMatch(values []string, version string) bool {
	for _, value := range values {
		for tag := range strings.SplitSeq(value, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" {
				return true
			}
			tag = strings.TrimPrefix(tag, "W/")
			if len(tag) >= 2 && tag[0] == '"' && tag[len(tag)-1] == '"' && tag[1:len(tag)-1] == version {
				return true
			}
		}
	}
	return false
}
