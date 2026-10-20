package http

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"regexp"
)

// requestState is what the edge learns about a request as it is served,
// shared through the context with everything that logs while serving it.
type requestState struct {
	id string
	// route is the matched pattern, "unmatched" when none matches, or "" until
	// the request is routed.
	route  string
	client Client
}

type stateKey struct{}

func stateOf(ctx context.Context) *requestState {
	st, _ := ctx.Value(stateKey{}).(*requestState)
	return st
}

func requestIDOf(ctx context.Context) string {
	if st := stateOf(ctx); st != nil {
		return st.id
	}
	return ""
}

// wellFormedRequestID is what an incoming X-Request-ID must look like to be
// kept: short and free of anything a log or a header could misread.
var wellFormedRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// requestID keeps a well-formed incoming X-Request-ID, so a request stays
// traceable through the proxies in front, and mints one otherwise.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); wellFormedRequestID.MatchString(id) {
		return id
	}
	return rand.Text()
}

// newLogHandler wraps h so that every record logged with a request's context
// carries the request's ID and, once known, its route, at the top level
// whatever groups the logger was given: every line of one request can be
// found by its ID.
func newLogHandler(h slog.Handler) slog.Handler { return &logHandler{base: h} }

// logHandler keeps the attributes and groups it was given as steps to replay,
// so the request's attributes can be added before any group opens.
type logHandler struct {
	base  slog.Handler
	steps []func(slog.Handler) slog.Handler
}

func (h *logHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.base.Enabled(ctx, l)
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	next := h.base
	if st := stateOf(ctx); st != nil {
		attrs := []slog.Attr{slog.String("request_id", st.id)}
		if st.route != "" {
			attrs = append(attrs, slog.String("route", st.route))
		}
		next = next.WithAttrs(attrs)
	}
	for _, step := range h.steps {
		next = step(next)
	}
	return next.Handle(ctx, r)
}

func (h *logHandler) with(step func(slog.Handler) slog.Handler) slog.Handler {
	return &logHandler{base: h.base, steps: append(h.steps[:len(h.steps):len(h.steps)], step)}
}

func (h *logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return h.with(func(n slog.Handler) slog.Handler { return n.WithAttrs(as) })
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	return h.with(func(n slog.Handler) slog.Handler { return n.WithGroup(name) })
}

// recorder remembers the status and the size of a response for the access
// log, and whether anything was written, so a panic after the first byte is
// not answered a second time.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// WriteHeader records the final status; an informational 1xx is not one.
func (rw *recorder) WriteHeader(status int) {
	if rw.status == 0 && status >= 200 {
		rw.status = status
	}
	rw.ResponseWriter.WriteHeader(status)
}

// committed reports whether the status line has gone out, after which the
// response can no longer be replaced by a problem.
func (rw *recorder) committed() bool { return rw.status != 0 }

func (rw *recorder) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += int64(n)
	return n, err
}

// Flush lets streams flush through the recorder. Flushing commits the
// status, 200 when none was written.
func (rw *recorder) Flush() {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	_ = http.NewResponseController(rw.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the connection's own writer.
func (rw *recorder) Unwrap() http.ResponseWriter { return rw.ResponseWriter }
