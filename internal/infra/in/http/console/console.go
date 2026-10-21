// Package console serves the web console the binary embeds: the build of
// web/apps/console, or a placeholder page when the binary was built without
// it, so `go build` never needs Node.
package console

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
)

// dist is where the console's build writes itself. It always exists, holding
// at least the marker file committed so that a fresh clone builds.
//
//go:embed all:dist
var dist embed.FS

//go:embed placeholder
var placeholder embed.FS

// entryDocument is the document every application route answers with; the
// console's router then renders the route in the browser.
const entryDocument = "index.html"

// assetsDir holds the build's content-hashed files. A file there never
// changes under its name, so it is cached forever; a name there that does not
// exist is refused rather than answered with the entry document, so a
// browser holding an old entry document gets an error, not HTML as script.
const assetsDir = "assets/"

// ContentSecurityPolicy keeps the console on its own origin: scripts, styles,
// images, fonts and requests from the origin only, no plugins, no framing, and
// forms posting nowhere else. Inline styles are allowed because the
// positioning of menus and popovers is computed in the browser and written as
// style attributes; inline scripts are not, which is why the theme applied
// before the first paint is a file.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; " +
	"form-action 'self'; frame-ancestors 'none'"

// Cache policies: hashed assets forever; other files, such as the script
// applying the theme, revalidated on every use; the entry document never
// stored, so a deploy reaches every browser on its next navigation.
const (
	cacheForever    = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
	cacheNever      = "no-store"
)

// defaultLocaleMeta is how the entry document tells the console which
// language to use when neither the member nor the browser names one it
// offers. The build writes it with English; the handler writes the
// configured language in its place.
const (
	localeMetaPrefix  = `<meta name="flagnetic-default-locale" content="`
	defaultLocaleMeta = localeMetaPrefix + `en"`
)

// errNoLocaleMeta reports an entry document without defaultLocaleMeta: the
// console would silently ignore FLAGNETIC_DEFAULT_LOCALE.
var errNoLocaleMeta = errors.New("the entry document has no " + defaultLocaleMeta + "> to write the default locale into")

// FS returns the embedded console and whether it is a build of the console
// rather than the placeholder.
func FS() (fs.FS, bool) {
	built, err := fs.Sub(dist, "dist")
	if err == nil {
		if _, err = fs.Stat(built, entryDocument); err == nil {
			return built, true
		}
	}
	sub, err := fs.Sub(placeholder, "placeholder")
	if err != nil {
		panic(err) // the embedded directory exists by construction
	}
	return sub, false
}

// Handler serves the console in fsys for GET and HEAD requests, with
// defaultLocale, one of config.ConsoleLocales, written into its entry
// document. The edge routes to it only the paths no API or probe owns.
func Handler(fsys fs.FS, defaultLocale string) (http.Handler, error) {
	// Only a language the console offers is written into the document, so
	// nothing a caller passes can change the markup around it.
	if !slices.Contains(config.ConsoleLocales, defaultLocale) {
		return nil, fmt.Errorf("default locale %q is not a language the console offers", defaultLocale)
	}
	doc, err := fs.ReadFile(fsys, entryDocument)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(doc, []byte(defaultLocaleMeta)) {
		return nil, errNoLocaleMeta
	}
	doc = bytes.Replace(doc, []byte(defaultLocaleMeta),
		[]byte(localeMetaPrefix+defaultLocale+`"`), 1)
	return &handler{fsys: fsys, doc: doc}, nil
}

type handler struct {
	fsys fs.FS
	doc  []byte
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Security-Policy", ContentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "same-origin")

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == entryDocument {
		h.serveDocument(w, r)
		return
	}
	f, err := h.fsys.Open(name)
	if err == nil {
		defer func() { _ = f.Close() }()
		var info fs.FileInfo
		if info, err = f.Stat(); err == nil && !info.IsDir() {
			h.serveFile(w, r, name, f)
			return
		}
	}
	switch {
	case strings.HasPrefix(name, assetsDir):
		header.Set("Cache-Control", cacheNever)
		http.Error(w, "404 page not found", http.StatusNotFound)
	case err == nil || errors.Is(err, fs.ErrNotExist):
		// An application route, or a directory: the router in the browser
		// renders it, its own not-found page included.
		h.serveDocument(w, r)
	default:
		unavailable(w)
	}
}

// serveFile answers with one file of the build. Every file of an embedded
// file system can seek, which ranges and HEAD need; one that cannot is a
// file system the console was not built for, refused rather than guessed at.
func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, name string, f fs.File) {
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		unavailable(w)
		return
	}
	if strings.HasPrefix(name, assetsDir) {
		w.Header().Set("Cache-Control", cacheForever)
	} else {
		w.Header().Set("Cache-Control", cacheRevalidate)
	}
	// A zero time sends no Last-Modified: the embedded files carry none, and
	// the hash or revalidation already decides freshness.
	http.ServeContent(w, r, name, time.Time{}, rs)
}

// unavailable answers a file the console holds but cannot serve.
func unavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", cacheNever)
	http.Error(w, "the console is not available", http.StatusInternalServerError)
}

func (h *handler) serveDocument(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheNever)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, entryDocument, time.Time{}, bytes.NewReader(h.doc))
}
