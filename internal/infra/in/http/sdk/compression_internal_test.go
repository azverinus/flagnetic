package sdk

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// brokenWriter is a response writer whose connection has gone away.
type brokenWriter struct {
	http.ResponseWriter
	err error
}

func (b brokenWriter) FlushError() error { return b.err }

func TestFlushOfAGzippedAnswerReportsTheBrokenConnection(t *testing.T) {
	broken := errors.New("broken pipe")
	var got error
	h := compression(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		got = http.NewResponseController(w).Flush()
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(brokenWriter{ResponseWriter: httptest.NewRecorder(), err: broken}, req)
	if !errors.Is(got, broken) {
		t.Errorf("Flush = %v, want the underlying writer's error", got)
	}
}
