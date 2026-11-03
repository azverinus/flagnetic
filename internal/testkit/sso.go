package testkit

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// VisitOptions shape how a Browser follows an address.
type VisitOptions struct {
	// PublicURL is the address the instance believes it is served at, such as
	// http://localhost:8080; addresses on its origin are fetched from the
	// in-process server instead, as a reverse proxy would send them.
	PublicURL string
	// Login answers an identity provider's login page: the address to post to,
	// relative to the page's, and the form fields. A nil Login, or one
	// answering ok false, ends the visit at the page.
	Login func(page string) (action string, form url.Values, ok bool)
	// Stop ends the visit before the address u is fetched when it returns true.
	Stop func(u *url.URL) bool
}

// Visit is where a browser ended up.
type Visit struct {
	// URL is the address last reached: the last fetched, or the one Stop held.
	URL *url.URL
	// Status and Body are the last response's; zero and empty when Stop held
	// the address before any fetch of it.
	Status int
	Body   string
	// Header is the last response's.
	Header http.Header
}

// Query returns the value of the query parameter name of where the visit
// ended.
func (v Visit) Query(name string) string { return v.URL.Query().Get(name) }

// maxHops bounds the redirects one visit follows.
const maxHops = 20

// Visit follows rawURL as a browser does, hop by hop, carrying the cookies it
// holds, and returns where it ended: on a response that is not a redirect or a
// login page opts.Login does not answer, or before an address opts.Stop
// holds.
func (b *Browser) Visit(t testing.TB, rawURL string, opts VisitOptions) Visit {
	t.Helper()
	return b.visit(t, http.MethodGet, rawURL, nil, opts)
}

// Post submits form to rawURL as a page an identity provider served would,
// then follows the redirects it answers like Visit.
func (b *Browser) Post(t testing.TB, rawURL string, form url.Values, opts VisitOptions) Visit {
	t.Helper()
	return b.visit(t, http.MethodPost, rawURL, form, opts)
}

func (b *Browser) visit(t testing.TB, method, rawURL string, form url.Values, opts VisitOptions) Visit {
	t.Helper()
	client := &http.Client{
		Jar: b.jar, Transport: b.HTTP.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	u := b.resolve(t, rawURL, nil, opts)
	for range maxHops {
		if opts.Stop != nil && opts.Stop(u) {
			return Visit{URL: u}
		}
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(t.Context(), method, u.String(), body)
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		end := Visit{URL: u, Status: res.StatusCode, Body: string(got), Header: res.Header}
		if loc := res.Header.Get("Location"); loc != "" && res.StatusCode >= 300 && res.StatusCode < 400 {
			u, method, form = b.resolve(t, loc, u, opts), http.MethodGet, nil
			continue
		}
		if opts.Login == nil || res.StatusCode != http.StatusOK {
			return end
		}
		action, fields, ok := opts.Login(end.Body)
		if !ok {
			return end
		}
		u, method, form = b.resolve(t, action, u, opts), http.MethodPost, fields
	}
	t.Fatalf("a visit to %s followed more than %d redirects", rawURL, maxHops)
	return Visit{}
}

// resolve returns ref relative to base, an address on the instance's public
// origin moved to the in-process server.
func (b *Browser) resolve(t testing.TB, ref string, base *url.URL, opts VisitOptions) *url.URL {
	t.Helper()
	u, err := url.Parse(ref)
	if err != nil {
		t.Fatal(err)
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if opts.PublicURL != "" {
		public, perr := url.Parse(opts.PublicURL)
		own, oerr := url.Parse(b.url)
		if perr != nil || oerr != nil {
			t.Fatal(perr, oerr)
		}
		if u.Scheme == public.Scheme && u.Host == public.Host {
			u.Scheme, u.Host = own.Scheme, own.Host
		}
	}
	return u
}

var loginForm = regexp.MustCompile(`<form[^>]*\saction="([^"]+)"`)

// DexLogin answers Dex's login page as user.
func DexLogin(user DexUser) func(page string) (string, url.Values, bool) {
	return func(page string) (string, url.Values, bool) {
		m := loginForm.FindStringSubmatch(page)
		if m == nil {
			return "", nil, false
		}
		return html.UnescapeString(m[1]), url.Values{"login": {user.Email}, "password": {user.Password}}, true
	}
}
