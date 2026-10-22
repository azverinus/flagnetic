package sdk

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// compression answers in gzip a request that accepts it and decompresses a
// gzip request body, for every request of the SDK API: a ruleset is large
// and SDKs poll it, and SDK batches compress well. A body's limit counts it
// after decompression, since the edge applies the route's limit to the
// body handed on. A body in any other coding is refused 415.
func compression(edge *flaghttp.Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
				switch coding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); coding {
				case "", "identity":
				case "gzip":
					r.Body = &gunzipBody{src: r.Body}
					// Unknown once decompressed, so the route's limit is
					// judged on what is read, not on the compressed size.
					r.ContentLength = -1
					r.Header.Del("Content-Length")
				default:
					edge.WriteError(w, r, flaghttp.UnsupportedMediaType("the body's Content-Encoding must be gzip or identity"))
					return
				}
			}
			gw := &gzipWriter{ResponseWriter: w, accepted: acceptsGzip(r.Header.Values("Accept-Encoding")) && r.Method != http.MethodHead}
			defer gw.close()
			next.ServeHTTP(gw, r)
		})
	}
}

// acceptsGzip reports whether Accept-Encoding, as the values of its headers,
// accepts gzip with a quality above zero.
func acceptsGzip(values []string) bool {
	for _, value := range values {
		for coding := range strings.SplitSeq(value, ",") {
			name, params, _ := strings.Cut(coding, ";")
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "gzip" && name != "*" {
				continue
			}
			q := 1.0
			if k, v, ok := strings.Cut(strings.TrimSpace(params), "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
			return q > 0
		}
	}
	return false
}

// gzipWriter compresses an answer once its status says it has a body, and
// tells caches that every answer depends on Accept-Encoding.
type gzipWriter struct {
	http.ResponseWriter
	accepted bool
	wrote    bool
	zw       *gzip.Writer
}

func (g *gzipWriter) WriteHeader(status int) {
	if g.wrote || status < http.StatusOK {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	g.wrote = true
	h := g.Header()
	if !strings.Contains(strings.ToLower(strings.Join(h.Values("Vary"), ",")), "accept-encoding") {
		h.Add("Vary", "Accept-Encoding")
	}
	if g.accepted && status != http.StatusNoContent && status != http.StatusNotModified && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		g.zw = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	if g.zw != nil {
		return g.zw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Flush sends what is compressed so far, so a stream's frames leave at once.
func (g *gzipWriter) Flush() {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	_ = http.NewResponseController(g.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) close() {
	if g.zw != nil {
		_ = g.zw.Close()
	}
}

// gunzipBody decompresses a request body as it is read. A body that is not
// valid gzip is a request the adapter cannot decode, 400 invalid_request,
// whoever reads it.
type gunzipBody struct {
	src io.ReadCloser
	zr  *gzip.Reader
}

func (b *gunzipBody) Read(p []byte) (int, error) {
	if b.zr == nil {
		zr, err := gzip.NewReader(b.src)
		if err != nil {
			return 0, b.fault(err)
		}
		b.zr = zr
	}
	n, err := b.zr.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = b.fault(err)
	}
	return n, err
}

// fault returns err as the body's reader reports it: a body that stopped
// arriving in time stays the edge's to judge, anything else is gzip the
// adapter cannot decode.
func (b *gunzipBody) fault(err error) error {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	return flaghttp.InvalidRequest("the body is not valid gzip: " + err.Error())
}

func (b *gunzipBody) Close() error { return b.src.Close() }
