package testkit

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/compose"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// MemberPassword is the password of every member NewMember creates.
const MemberPassword = "tangerine-violin-orbit-1987"

// Member is a member a test created, with what they sign in with.
type Member struct {
	ID       access.MemberID
	Email    string
	Password string
}

// MemberOption changes the member NewMember creates.
type MemberOption func(*access.Member)

// AsInstanceAdmin makes the member an instance admin.
func AsInstanceAdmin() MemberOption { return func(m *access.Member) { m.InstanceAdmin = true } }

// WithStatus gives the member status instead of active.
func WithStatus(s access.MemberStatus) MemberOption { return func(m *access.Member) { m.Status = s } }

// WithEmail gives the member email instead of a random one.
func WithEmail(email string) MemberOption { return func(m *access.Member) { m.Email = email } }

// WithLocale gives the member the console language locale.
func WithLocale(locale string) MemberOption { return func(m *access.Member) { m.Locale = locale } }

// WithDisplayName gives the member name instead of "Test Member".
func WithDisplayName(name string) MemberOption {
	return func(m *access.Member) { m.DisplayName = name }
}

// NewMember creates an active member on pool, who holds no grant and signs
// in with MemberPassword. An invited member holds no password, as one who
// has not accepted their invitation.
func NewMember(t testing.TB, pool *pgxpool.Pool, opts ...MemberOption) Member {
	t.Helper()
	m := access.Member{
		ID: id.New[access.Member](id.NewSource(Clock())), Email: strings.ToLower(rand.Text()) + "@example.com",
		DisplayName: "Test Member", Status: access.MemberActive, CreatedAt: Clock().Now(),
	}
	for _, opt := range opts {
		opt(&m)
	}
	RecordSecret(MemberPassword)
	var hash string
	if m.Status != access.MemberInvited {
		var err error
		if hash, err = compose.Hasher().Hash(t.Context(), secret.New(MemberPassword)); err != nil {
			t.Fatal(err)
		}
	}
	if err := pgaccess.NewStore(pool).CreateMember(t.Context(), m, hash); err != nil {
		t.Fatal(err)
	}
	return Member{ID: m.ID, Email: m.Email, Password: MemberPassword}
}

// Browser is a client of the management API as the console is: it holds
// cookies, and sends the fetch metadata a browser would.
type Browser struct {
	// Client calls the management API with the browser's cookies.
	*management.ClientWithResponses
	// HTTP is the browser's own HTTP client, for requests the generated
	// client cannot shape.
	HTTP *http.Client
	// Header is sent with every request, such as Origin or Sec-Fetch-Site;
	// empty, the browser sends neither, as a client that is no browser.
	Header http.Header

	url string
	jar *cookiejar.Jar
}

// NewBrowser returns a browser of the console served at baseURL, such as an
// in-process Server's URL, holding no cookie.
func NewBrowser(t testing.TB, baseURL string) *Browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &Browser{Header: http.Header{}, url: baseURL, jar: jar}
	b.HTTP = &http.Client{Jar: jar, Transport: headerTransport{b: b}}
	c, err := management.NewClientWithResponses(baseURL+management.BaseURL, management.WithHTTPClient(b.HTTP))
	if err != nil {
		t.Fatal(err)
	}
	b.ClientWithResponses = c
	return b
}

// headerTransport adds the browser's headers to each request.
type headerTransport struct{ b *Browser }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	for k, vs := range h.b.Header {
		r.Header[k] = vs
	}
	return recordingTransport{}.RoundTrip(r)
}

// SignIn signs in as email with password, failing the test unless the
// session opens.
func (b *Browser) SignIn(t testing.TB, email, password string) *management.Me {
	t.Helper()
	RecordSecret(password)
	res, err := b.SignInWithResponse(t.Context(), management.SignInRequest{Email: email, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("sign-in as %s = %d: %s", email, res.StatusCode(), res.Body)
	}
	return res.JSON200
}

// Cookie returns the value of the browser's cookie called name, "" when it
// holds none.
func (b *Browser) Cookie(name string) string {
	u, _ := url.Parse(b.url)
	for _, c := range b.jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// SetCookie makes the browser hold the cookie name with value, such as a
// forged or an old session cookie.
func (b *Browser) SetCookie(name, value string) {
	u, _ := url.Parse(b.url)
	// A cookie as the server sets it on plain HTTP localhost, where tests run.
	b.jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}}) //nolint:gosec // no TLS in tests
}

// Do sends a request of method to path under the server's URL with body
// as contentType, and returns the response with its body read.
func (b *Browser) Do(t testing.TB, method, path, contentType, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, b.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := b.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return res, string(got)
}
