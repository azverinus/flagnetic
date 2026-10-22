package sdk

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// BaseURL is where the SDK API is served.
const BaseURL = "/sdk/v1"

// Handlers are the use cases the SDK API serves.
type Handlers struct {
	Authenticate *appdelivery.AuthenticateSDKKey
	ServeRuleset *appdelivery.ServeRuleset
}

// operation is how the adapter guards one operation of the contract.
type operation struct {
	// kinds are the key kinds the operation serves.
	kinds []delivery.SDKKeyKind
}

// operations are the operations served so far, by the pattern the
// generated router mounts them on. An operation of the contract missing
// here is not mounted, and its path answers 404 like any unknown one.
var operations = map[string]operation{
	"GET " + BaseURL + "/ruleset": {kinds: []delivery.SDKKeyKind{delivery.SDKKeyServer}},
}

// api serves the operations of the SDK API.
type api struct {
	edge *flaghttp.Server
	h    Handlers
}

// Mount serves the SDK API on edge: every request under BaseURL, routed or
// not, through the API's compression, and each operation behind the
// authentication of its SDK key.
func Mount(edge *flaghttp.Server, h Handlers) {
	a := &api{edge: edge, h: h}
	edge.Wrap(BaseURL+"/", compression(edge))
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
	rt.a.edge.Handle(pattern, rt.a.guard(op, http.HandlerFunc(h)))
}

func (rt router) ServeHTTP(w http.ResponseWriter, r *http.Request) { rt.a.edge.ServeHTTP(w, r) }

type keyKey struct{}

// keyOf returns the SDK key the guard authenticated for the request ctx
// belongs to.
func keyOf(ctx context.Context) delivery.SDKKey {
	k, _ := ctx.Value(keyKey{}).(delivery.SDKKey)
	return k
}

// guard authenticates the request's SDK key and refuses a key of a kind op
// does not serve; the key then travels in the request's context.
func (a *api) guard(op operation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := a.h.Authenticate.Handle(r.Context(), bearer(r.Header.Get("Authorization")))
		if err == nil {
			err = key.CheckKind(op.kinds...)
		}
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyKey{}, key)))
	})
}

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
	rs, err := a.h.ServeRuleset.Handle(r.Context(), keyOf(r.Context()))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
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
	h.Set("Vary", "Authorization, Accept-Encoding")
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

// The operations of the contract still to be served.

func (a *api) Evaluate(w http.ResponseWriter, r *http.Request, _ EvaluateParams) {
	a.edge.WriteError(w, r, errors.New("sdk: evaluate is not served yet"))
}

func (a *api) PostEvents(w http.ResponseWriter, r *http.Request, _ PostEventsParams) {
	a.edge.WriteError(w, r, errors.New("sdk: events are not served yet"))
}

func (a *api) StreamRuleset(w http.ResponseWriter, r *http.Request, _ StreamRulesetParams) {
	a.edge.WriteError(w, r, errors.New("sdk: the stream is not served yet"))
}
