package sdk

import (
	"net/http"
	"strings"
)

// The headers of cross-origin resource sharing every answer of the SDK API
// carries, so that a browser SDK can call it from a page of any origin.
const (
	allowedMethods = "GET, POST, OPTIONS"
	allowedHeaders = "Authorization, Content-Type, Content-Encoding, Flagnetic-Schema-Version, Last-Event-ID"
	// exposedHeaders are what a browser SDK reads of an answer: the
	// ruleset's version, how long to wait, and its rate-limit budget.
	exposedHeaders = "ETag, Retry-After, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset"
	// preflightMaxAge lets a browser keep a preflight for a day, so a page
	// pays it once rather than before every request.
	preflightMaxAge = "86400"
)

// cors lets browsers call the SDK API. Every answer, problems included,
// allows any origin and exposes the headers a browser SDK reads, until the
// guard narrows it for a client key that lists its origins. Credentials are
// never allowed: an SDK key travels in Authorization or the query, and no
// cookie of the console must ride along. A preflight of a path the API
// serves is answered at once, without a key, since a browser sends none
// with it; any other path or method goes on to be answered 404 or 405.
func cors(routed func(path string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Expose-Headers", exposedHeaders)
			if r.Method == http.MethodOptions && routed(r.URL.Path) {
				h.Set("Access-Control-Allow-Methods", allowedMethods)
				h.Set("Access-Control-Allow-Headers", allowedHeaders)
				h.Set("Access-Control-Max-Age", preflightMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// admitOrigin applies the key's allowed origins to the answer: refused
// server_key_from_browser for a server key sent from a browser,
// origin_not_allowed for an origin a client key does not list. A key that
// lists origins makes the answer depend on the origin: a listed one is
// echoed in place of "*", and a request without Origin, which is no
// browser's, is answered without the header.
func admitOrigin(w http.ResponseWriter, r *http.Request, restricted func(origin string, sent bool) (bool, error)) error {
	origins, sent := r.Header["Origin"]
	origin := ""
	if sent && len(origins) > 0 {
		origin = strings.TrimSpace(origins[0])
	}
	isRestricted, err := restricted(origin, sent)
	if err != nil || !isRestricted {
		return err
	}
	h := w.Header()
	h.Add("Vary", "Origin")
	if sent {
		h.Set("Access-Control-Allow-Origin", origin)
	} else {
		h.Del("Access-Control-Allow-Origin")
	}
	return nil
}
