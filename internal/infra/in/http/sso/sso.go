// Package sso is the browser edge of single sign-on: the addresses a browser
// is sent through to sign in at an auth provider, outside the management
// API because every step is a navigation, not a call. A failure never
// answers a JSON body to a navigation: it redirects to the sign-in page with
// a code.
package sso

import (
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

const (
	// secureCookie is the name of the attempt cookie wherever cookies are
	// Secure: the __Host- prefix makes a browser refuse one set by a sibling
	// subdomain or without Secure.
	secureCookie = "__Host-flagnetic_sso"
	// plainCookie is the name where cookies cannot be Secure, on localhost or
	// when an operator allowed insecure cookies for a trial, since a browser
	// refuses a __Host- cookie without Secure.
	plainCookie = "flagnetic_sso"
	// completePrefix starts the path of the address that completes an attempt;
	// it takes the place of a provider's slug, which therefore cannot be it.
	completePrefix = "complete"
)

// Handlers are the use cases the edge calls.
type Handlers struct {
	Start    *appidentity.StartSignIn
	Callback *appidentity.CompleteCallback
	Complete *appidentity.CompleteSignIn
}

// Options are how the edge treats browsers.
type Options struct {
	// SecureCookies reports whether cookies set to a request with this scheme
	// and host are Secure.
	SecureCookies func(scheme, host string) bool
	// SessionCookie returns the Set-Cookie value that carries a session
	// cookie's value.
	SessionCookie func(value string, secure bool) string
	// Metrics counts sign-ins; nil counts none.
	Metrics *Metrics
}

type edge struct {
	*flaghttp.Server
	h Handlers
	o Options
}

// Mount serves the single sign-on addresses on s:
//
//	GET /sso/{provider}/start?return_to=   sends the browser to the provider
//	GET /sso/{provider}/callback           takes the provider's answer
//	GET /sso/complete/{attempt}            completes the sign-in in the browser that started it
func Mount(s *flaghttp.Server, h Handlers, o Options) {
	e := &edge{Server: s, h: h, o: o}
	// One pattern serves them all: /sso/{provider}/start and
	// /sso/complete/{attempt} overlap on /sso/complete/start, which the router
	// cannot rank. The attempt in the address is a secret, never logged.
	s.Handle("GET /sso/{first}/{second}", http.HandlerFunc(e.route))
	s.SecretPath("/sso/" + completePrefix + "/{attempt}")
}

func (e *edge) route(w http.ResponseWriter, r *http.Request) {
	// A navigation carries a secret in its address or its query: it is not
	// cached, and its address is not sent on to the next page.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	first, second := r.PathValue("first"), r.PathValue("second")
	switch {
	case first == completePrefix:
		e.complete(w, r, second)
	case second == "start":
		e.start(w, r, first)
	case second == "callback":
		e.callback(w, r, first)
	default:
		e.WriteError(w, r, notFound)
	}
}

var notFound = apperrors.NotFound("not_found", "no such address")

// secure reports whether the cookies set to r are Secure.
func (e *edge) secure(r *http.Request) bool {
	return e.o.SecureCookies(flaghttp.ClientOf(r.Context()).Scheme, r.Host)
}

func attemptCookieName(secure bool) string {
	if secure {
		return secureCookie
	}
	return plainCookie
}

func (e *edge) start(w http.ResponseWriter, r *http.Request, slug string) {
	res, err := e.h.Start.Handle(r.Context(), appidentity.StartCommand{Slug: slug, ReturnTo: r.URL.Query().Get("return_to")})
	if err != nil {
		e.refuse(w, r, err)
		return
	}
	secure := e.secure(r)
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is decided per request, as for the session cookie
		Name: attemptCookieName(secure), Value: res.Cookie.Reveal(), Path: "/", MaxAge: int(res.Lifetime / time.Second),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, res.RedirectURL, http.StatusFound)
}

func (e *edge) callback(w http.ResponseWriter, r *http.Request, slug string) {
	q := r.URL.Query()
	token, err := e.h.Callback.Handle(r.Context(), appidentity.CallbackCommand{
		Slug: slug, State: q.Get("state"), Code: q.Get("code"), IDPError: q.Get("error"),
	})
	if err != nil {
		e.refuse(w, r, err)
		return
	}
	http.Redirect(w, r, "/sso/"+completePrefix+"/"+url.PathEscape(token), http.StatusSeeOther)
}

func (e *edge) complete(w http.ResponseWriter, r *http.Request, token string) {
	secure := e.secure(r)
	name := attemptCookieName(secure)
	var cookie string
	if c, err := r.Cookie(name); err == nil {
		cookie = c.Value
	}
	// The attempt ends here whatever the outcome: its cookie is of no use
	// after.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // clears the cookie, with the attributes it was set with
		Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	res, err := e.h.Complete.Handle(r.Context(), appidentity.CompleteCommand{
		Token: token, Cookie: cookie, ClientIP: clientIP(r), UserAgent: r.UserAgent(), RequestID: flaghttp.RequestIDOf(r.Context()),
	})
	if err != nil {
		e.refuse(w, r, err)
		return
	}
	e.o.Metrics.count("succeeded")
	w.Header().Add("Set-Cookie", e.o.SessionCookie(res.Cookie.Reveal(), secure))
	http.Redirect(w, r, res.ReturnTo, http.StatusSeeOther)
}

func clientIP(r *http.Request) netip.Addr { return flaghttp.ClientOf(r.Context()).IP }

// refuse ends a navigation that failed. An unknown or disabled provider is
// 404, as is any address that is not one; every other failure sends the
// browser to the sign-in page with the code that says why, and logs the
// cause for the operator.
func (e *edge) refuse(w http.ResponseWriter, r *http.Request, err error) {
	var failure *appidentity.SignInFailure
	switch {
	case errors.As(err, &failure):
		e.o.Metrics.count(string(failure.Code))
		e.Logger().WarnContext(r.Context(), "single sign-on failed", "code", failure.Code, "error", err)
		redirectToSignIn(w, r, failure.Code)
	case apperrors.IsKind(err, apperrors.KindNotFound):
		e.WriteError(w, r, err)
	default:
		e.o.Metrics.count(string(appidentity.CodeSSOFailed))
		e.Logger().ErrorContext(r.Context(), "single sign-on failed", "error", err)
		redirectToSignIn(w, r, appidentity.CodeSSOFailed)
	}
}

func redirectToSignIn(w http.ResponseWriter, r *http.Request, code appidentity.FailureCode) {
	http.Redirect(w, r, "/sign-in?error="+url.QueryEscape(string(code)), http.StatusSeeOther)
}
