package management

import (
	"context"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// SessionCookie is the name of the cookie a session is referred to by.
const SessionCookie = "flagnetic_session"

// Options are how the management API treats browsers.
type Options struct {
	// BaseURL is the console's public origin, nil when not configured: then
	// each request's own scheme and host stand in for it.
	BaseURL *url.URL
	// InsecureCookies lets the session cookie go without Secure over plain
	// HTTP off localhost.
	InsecureCookies bool
	// Clock times the change streams' heartbeats and lifetimes; nil for the
	// system's.
	Clock clock.Clock
}

func (o Options) clock() clock.Clock {
	if o.Clock == nil {
		return clock.System{}
	}
	return o.Clock
}

// errCrossOrigin refuses a request signed in by the session cookie that a
// browser says comes from another origin.
var errCrossOrigin = apperrors.Forbidden("cross_origin", "a request signed in by the session cookie must come from the console's own origin")

// errNotJSON refuses a body that is not JSON to an operation that opens a
// session, which is what a form on another site would send.
var errNotJSON = flaghttp.UnsupportedMediaType("the body must be sent as application/json")

// isJSON reports whether r's body is declared application/json.
func isJSON(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json"
}

// browser is what the seam learns about the browser behind a request for
// the handlers that set or clear its cookie.
type browser struct {
	secureCookies bool
	userAgent     string
	// console is the URL the console is reached at, without a trailing
	// slash, which links handed out for a browser to open start with.
	console string
}

type browserKey struct{}

func browserOf(ctx context.Context) browser {
	b, _ := ctx.Value(browserKey{}).(browser)
	return b
}

// secureCookies reports whether the session cookie of a request whose
// scheme and host are these is Secure: always over HTTPS, and over plain
// HTTP too unless the console is on localhost, where browsers keep a Secure
// cookie off plain HTTP, or insecure cookies were allowed for a trial. The
// base URL, when configured, is what the browser sees in front of any proxy,
// so it decides instead of the request. Without it the request's own host
// is the best witness of what the browser opened: a browser sets Host from
// the URL and lets no page change it, so only a client that is no browser
// can claim localhost, and that spares only its own cookie the Secure flag.
// The listen address would say less, as it usually names no host at all.
func (o Options) secureCookies(scheme, host string) bool {
	if o.BaseURL != nil {
		scheme, host = o.BaseURL.Scheme, o.BaseURL.Host
	}
	return scheme == "https" || (!o.InsecureCookies && !isLocalhost(host))
}

// SecureCookies is secureCookies for the single sign-on edge, which sets the
// session cookie under the same rule as signing in with a password.
func (o Options) SecureCookies(scheme, host string) bool { return o.secureCookies(scheme, host) }

// console returns the URL the console is reached at, for a request whose
// scheme and host are these: the base URL when configured, since a proxy in
// front may rewrite what the request says, and the request's own origin
// otherwise.
func (o Options) console(scheme, host string) string {
	if o.BaseURL != nil {
		return strings.TrimSuffix(o.BaseURL.String(), "/")
	}
	return scheme + "://" + host
}

// isLocalhost reports whether host, with or without a port, names this
// machine: localhost, a name under it, or a loopback address.
func isLocalhost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

// sessionCookie returns the Set-Cookie value carrying value, or clearing
// the cookie when value is "".
func sessionCookie(ctx context.Context, value string) string {
	return SessionCookieHeader(value, browserOf(ctx).secureCookies)
}

// SessionCookieHeader returns the Set-Cookie value carrying value as the
// session cookie, or clearing the cookie when value is "". Secure is decided
// per request: off only on localhost, or when an operator allowed it for a
// trial without TLS.
func SessionCookieHeader(value string, secure bool) string {
	c := http.Cookie{ //nolint:gosec // see above
		Name: SessionCookie, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: secure,
	}
	if value == "" {
		c.MaxAge = -1
	}
	return c.String()
}

// SignIn opens a session for the member whose email and password the body
// names and sets its cookie.
func (a *api) SignIn(ctx context.Context, req SignInRequestObject) (SignInResponseObject, error) {
	res, err := a.h.SignIn.Handle(ctx, appaccess.SignInCommand{
		Email: req.Body.Email, Password: secret.New(req.Body.Password),
		ClientIP: flaghttp.ClientOf(ctx).IP, UserAgent: browserOf(ctx).userAgent, RequestID: flaghttp.RequestIDOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	me, err := a.meOf(ctx, res.Caller)
	if err != nil {
		return nil, err
	}
	cookie := sessionCookie(ctx, res.Cookie.Reveal())
	return SignIn200JSONResponse{Body: me, Headers: SignIn200ResponseHeaders{SetCookie: &cookie}}, nil
}

// SignOut ends the caller's session and clears its cookie.
func (a *api) SignOut(ctx context.Context, _ SignOutRequestObject) (SignOutResponseObject, error) {
	if err := a.h.SignOut.Handle(ctx, callerOf(ctx), flaghttp.RequestIDOf(ctx)); err != nil {
		return nil, err
	}
	cleared := sessionCookie(ctx, "")
	return SignOut204Response{Headers: SignOut204ResponseHeaders{SetCookie: &cleared}}, nil
}

// GetAuthMethods answers how members sign in: with their email and password,
// and through each enabled auth provider.
func (a *api) GetAuthMethods(ctx context.Context, _ GetAuthMethodsRequestObject) (GetAuthMethodsResponseObject, error) {
	list, err := a.h.ListEnabledAuthProviders.Handle(ctx)
	if err != nil {
		return nil, err
	}
	resetAvailable, err := a.h.ResetAvailable.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return GetAuthMethods200JSONResponse{
		LocalLoginEnabled: true, PasswordResetAvailable: resetAvailable,
		Providers: each(list, func(p identity.Provider) AuthMethodProvider {
			return AuthMethodProvider{Slug: p.Slug, DisplayName: p.DisplayName, Kind: AuthProviderKind(p.Kind)}
		}),
	}, nil
}

// GetInvitation answers what an invitation's link opens.
func (a *api) GetInvitation(ctx context.Context, req GetInvitationRequestObject) (GetInvitationResponseObject, error) {
	v, err := a.h.GetInvitation.Handle(ctx, secret.New(req.Secret))
	if err != nil {
		return nil, err
	}
	out := GetInvitation200JSONResponse{Email: v.MaskedEmail, DisplayName: v.DisplayName, ExpiresAt: v.ExpiresAt}
	if v.InvitedBy != "" {
		out.InvitedBy = &v.InvitedBy
	}
	if g := v.Grant; g != nil {
		role := string(g.Role)
		out.Project, out.Role = &InvitationProject{Key: g.ProjectKey, Name: g.ProjectName}, &role
	}
	return out, nil
}

// AcceptInvitation sets the invited member's first password, opens a
// session and sets its cookie.
func (a *api) AcceptInvitation(ctx context.Context, req AcceptInvitationRequestObject) (AcceptInvitationResponseObject, error) {
	res, err := a.h.AcceptInvitation.Handle(ctx, appaccess.AcceptInvitationCommand{
		Secret: secret.New(req.Secret), Password: secret.New(req.Body.Password),
		ClientIP: flaghttp.ClientOf(ctx).IP, UserAgent: browserOf(ctx).userAgent, RequestID: flaghttp.RequestIDOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	me, err := a.meOf(ctx, res.Caller)
	if err != nil {
		return nil, err
	}
	cookie := sessionCookie(ctx, res.Cookie.Reveal())
	return AcceptInvitation200JSONResponse{Body: me, Headers: AcceptInvitation200ResponseHeaders{SetCookie: &cookie}}, nil
}

// RequestPasswordReset answers 202 at once, whoever the email belongs to; a
// password reset is issued and mailed after the answer, when there is one to
// issue.
func (a *api) RequestPasswordReset(ctx context.Context, req RequestPasswordResetRequestObject) (RequestPasswordResetResponseObject, error) {
	a.h.RequestReset.Handle(ctx, appaccess.RequestPasswordResetCommand{Email: req.Body.Email, RequestID: flaghttp.RequestIDOf(ctx)})
	return RequestPasswordReset202Response{}, nil
}

// GetPasswordReset answers what a password reset's link opens.
func (a *api) GetPasswordReset(ctx context.Context, req GetPasswordResetRequestObject) (GetPasswordResetResponseObject, error) {
	v, err := a.h.GetReset.Handle(ctx, secret.New(req.Secret))
	if err != nil {
		return nil, err
	}
	return GetPasswordReset200JSONResponse{Email: v.MaskedEmail, DisplayName: v.DisplayName, ExpiresAt: v.ExpiresAt}, nil
}

// CompletePasswordReset sets the member's new password, opens a session and
// sets its cookie.
func (a *api) CompletePasswordReset(ctx context.Context, req CompletePasswordResetRequestObject) (CompletePasswordResetResponseObject, error) {
	res, err := a.h.CompleteReset.Handle(ctx, appaccess.CompletePasswordResetCommand{
		Secret: secret.New(req.Secret), Password: secret.New(req.Body.Password),
		ClientIP: flaghttp.ClientOf(ctx).IP, UserAgent: browserOf(ctx).userAgent, RequestID: flaghttp.RequestIDOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	me, err := a.meOf(ctx, res.Caller)
	if err != nil {
		return nil, err
	}
	cookie := sessionCookie(ctx, res.Cookie.Reveal())
	return CompletePasswordReset200JSONResponse{Body: me, Headers: CompletePasswordReset200ResponseHeaders{SetCookie: &cookie}}, nil
}
