package console_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/azverinus/flagnetic/internal/infra/in/http/console"
)

// consoleEntryDocument is the entry document the console's build starts
// from; the build adds its scripts and keeps the rest of the head as written.
const consoleEntryDocument = "../../../../../web/apps/console/index.html"

// serveDocument answers a request for the entry document from h.
func serveDocument(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sign-in", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// Every entry document the binary can carry, the console's and the
// placeholder's, names the configured language.
func TestConsoleWritesTheDefaultLocaleIntoEveryEntryDocument(t *testing.T) {
	source, err := os.ReadFile(consoleEntryDocument)
	if err != nil {
		t.Fatal(err)
	}
	embedded, _ := console.FS()
	for name, fsys := range map[string]fs.FS{
		"the console's": fstest.MapFS{"index.html": {Data: source}},
		"the embedded":  embedded,
	} {
		h, err := console.Handler(fsys, "ru")
		if err != nil {
			t.Fatalf("%s entry document: %v", name, err)
		}
		if body := serveDocument(t, h); !strings.Contains(body, `<meta name="flagnetic-default-locale" content="ru"`) {
			t.Errorf("%s entry document does not name ru as the default locale:\n%s", name, body)
		}
	}
}

// An entry document without the marker would leave FLAGNETIC_DEFAULT_LOCALE
// silently unused, so the console refuses to start on it.
func TestConsoleRefusesAnEntryDocumentWithoutTheDefaultLocale(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><html lang="en"><head></head></html>`)}}

	if _, err := console.Handler(fsys, "en"); err == nil {
		t.Fatal("Handler accepted an entry document with no default-locale meta element")
	}
}

// Only a language the console offers is written into the document.
func TestConsoleRefusesADefaultLocaleItDoesNotOffer(t *testing.T) {
	source, err := os.ReadFile(consoleEntryDocument)
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{"index.html": {Data: source}}

	for _, locale := range []string{"", "de", `ru"><script src="//evil.example/x.js"></script>`} {
		if _, err := console.Handler(fsys, locale); err == nil {
			t.Errorf("Handler accepted the default locale %q", locale)
		}
	}
}

// A file the console holds but cannot seek is refused, never answered with
// the entry document in its place.
func TestConsoleRefusesAFileItCannotServe(t *testing.T) {
	source, err := os.ReadFile(consoleEntryDocument)
	if err != nil {
		t.Fatal(err)
	}
	h, err := console.Handler(unseekable{fstest.MapFS{
		"index.html":          {Data: source},
		"assets/index-abc.js": {Data: []byte("export {}")},
	}}, "en")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/index-abc.js", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on a refusal", got)
	}
}

// unseekable hides the Seek of every file it opens.
type unseekable struct{ fsys fs.FS }

func (u unseekable) Open(name string) (fs.File, error) {
	f, err := u.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	return readOnly{f}, nil
}

// readOnly is a file that can only be read, as a stream.
type readOnly struct{ f fs.File }

func (r readOnly) Stat() (fs.FileInfo, error) { return r.f.Stat() }
func (r readOnly) Read(p []byte) (int, error) { return r.f.Read(p) }
func (r readOnly) Close() error               { return r.f.Close() }
