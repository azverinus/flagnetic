// Package deprecation announces deprecated operations on the wire. An
// operation of an OpenAPI document marked `deprecated: true` with the date of
// its deprecation in `x-deprecated-since`, and optionally the date it may
// disappear in `x-sunset`, is answered with a Deprecation header (RFC 9745)
// and a Sunset header (RFC 8594), so a client learns of it from the
// operation itself and nobody writes code for it.
//
// The tables are generated from the documents (`task generate`), one per API,
// and a test keeps them in step.
package deprecation

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// methods are the operation keys of an OpenAPI path item.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Notice is what a deprecated operation announces.
type Notice struct {
	// Since is when the operation was deprecated, at midnight UTC.
	Since time.Time
	// Sunset is when the operation may stop answering, at midnight UTC; the
	// zero time sets none.
	Sunset time.Time
}

// Table holds the deprecated operations of one API, keyed by route pattern
// relative to the API's base path, such as "GET /flags".
type Table map[string]Notice

// Read reads the deprecated operations of an OpenAPI document. It refuses a
// deprecated operation without a valid x-deprecated-since, an x-sunset
// before it, and either annotation on an operation not deprecated, so a
// deprecation is never half announced.
func Read(doc []byte) (Table, error) {
	var d struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("reading the document: %w", err)
	}
	t := Table{}
	var errs []error
	for path, item := range d.Paths {
		for method, node := range item {
			if !slices.Contains(methods, method) {
				continue // parameters, summary and the like
			}
			var o struct {
				OperationID string `yaml:"operationId"`
				Deprecated  bool   `yaml:"deprecated"`
				Since       string `yaml:"x-deprecated-since"`
				Sunset      string `yaml:"x-sunset"`
			}
			where := strings.ToUpper(method) + " " + path
			if err := node.Decode(&o); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", where, err))
				continue
			}
			if !o.Deprecated {
				if o.Since != "" || o.Sunset != "" {
					errs = append(errs, fmt.Errorf("%s (%s): x-deprecated-since or x-sunset on an operation that is not deprecated: true", where, o.OperationID))
				}
				continue
			}
			n, err := notice(o.Since, o.Sunset)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s (%s): %w", where, o.OperationID, err))
				continue
			}
			t[where] = n
		}
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
		return nil, errors.Join(errs...)
	}
	return t, nil
}

func notice(since, sunset string) (Notice, error) {
	if since == "" {
		return Notice{}, errors.New("deprecated without x-deprecated-since, the date it was deprecated (YYYY-MM-DD)")
	}
	var n Notice
	var err error
	if n.Since, err = time.Parse(time.DateOnly, since); err != nil {
		return Notice{}, fmt.Errorf("x-deprecated-since %q is not a date (YYYY-MM-DD)", since)
	}
	if sunset == "" {
		return n, nil
	}
	if n.Sunset, err = time.Parse(time.DateOnly, sunset); err != nil {
		return Notice{}, fmt.Errorf("x-sunset %q is not a date (YYYY-MM-DD)", sunset)
	}
	if n.Sunset.Before(n.Since) {
		return Notice{}, fmt.Errorf("x-sunset %s is before x-deprecated-since %s", sunset, since)
	}
	return n, nil
}

// Render returns the Go source, in package pkg, of the variable Deprecated
// holding t.
func Render(pkg, document string, t Table) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated from api/%s by `task generate`. DO NOT EDIT.\n\npackage %s\n\n", document, pkg)
	if len(t) > 0 {
		b.WriteString("import (\n\t\"time\"\n\n\t\"github.com/azverinus/flagnetic/internal/infra/in/http/deprecation\"\n)\n\n")
	} else {
		b.WriteString("import \"github.com/azverinus/flagnetic/internal/infra/in/http/deprecation\"\n\n")
	}
	b.WriteString("// Deprecated holds the operations the document marks deprecated, keyed by route pattern.\n")
	b.WriteString("var Deprecated = deprecation.Table{\n")
	patterns := make([]string, 0, len(t))
	for p := range t {
		patterns = append(patterns, p)
	}
	slices.Sort(patterns)
	for _, p := range patterns {
		fmt.Fprintf(&b, "\t%q: {Since: %s", p, date(t[p].Since))
		if !t[p].Sunset.IsZero() {
			fmt.Fprintf(&b, ", Sunset: %s", date(t[p].Sunset))
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

func date(t time.Time) string {
	return fmt.Sprintf("time.Date(%d, %d, %d, 0, 0, 0, 0, time.UTC)", t.Year(), int(t.Month()), t.Day())
}

// DeprecationValue is the Deprecation header value: the Unix time of the
// deprecation as a structured-field date, `@1767225600`.
func (n Notice) DeprecationValue() string { return "@" + strconv.FormatInt(n.Since.Unix(), 10) }

// SunsetValue is the Sunset header value, an HTTP date; empty when none.
func (n Notice) SunsetValue() string {
	if n.Sunset.IsZero() {
		return ""
	}
	return n.Sunset.UTC().Format(http.TimeFormat)
}

// Middleware answers every request to a deprecated operation of an API
// served under base, such as /api/v1, with its Deprecation and Sunset
// headers; every other request and every other part of the answer is left
// as it is. A table with no operation yields a middleware that does nothing.
//
// routes are the patterns of the API's other operations, as the table writes
// its own ("GET /flags/{flag}"). The middleware matches a request in a mux of
// its own, which must decide as the API's does: were only the deprecated
// patterns in it, a literal route beside a deprecated wildcard one
// ("GET /flags/export" beside "GET /flags/{flag}") would match the wildcard
// there and be announced as deprecated though it is not.
func Middleware(base string, t Table, routes ...string) func(http.Handler) http.Handler {
	if len(t) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	mux := http.NewServeMux()
	register := func(pattern string) {
		method, path, _ := strings.Cut(pattern, " ")
		full := method + " " + strings.TrimSuffix(base, "/") + path
		mux.Handle(full, http.NotFoundHandler())
	}
	for pattern := range t {
		register(pattern)
	}
	for _, pattern := range routes {
		if _, deprecated := t[pattern]; !deprecated {
			register(pattern)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, matched := mux.Handler(r); matched != "" {
				method, path, _ := strings.Cut(matched, " ")
				if n, deprecated := t[method+" "+strings.TrimPrefix(path, strings.TrimSuffix(base, "/"))]; deprecated {
					w.Header().Set("Deprecation", n.DeprecationValue())
					if s := n.SunsetValue(); s != "" {
						w.Header().Set("Sunset", s)
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
