package testkit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Reply is how a Receiver answers one call.
type Reply struct {
	// Status is the status answered; 200 when zero.
	Status int
	Body   string
	// Delay holds the answer this long, so a recorded latency or a timeout
	// shows.
	Delay time.Duration
	// Redirect, when set, is the Location answered with a 3xx Status.
	Redirect string
	// Drop closes the connection without an answer.
	Drop bool
	// Release, when set, holds the answer until it is closed or the call is
	// abandoned, so a test can act while an attempt is in flight.
	Release <-chan struct{}
}

// ReceivedCall is a call a Receiver was sent.
type ReceivedCall struct {
	Header http.Header
	Body   []byte
}

// Verifies reports whether the call's signature holds under secret, checked
// as a receiver written from the documentation checks it: one of the comma
// separated `v1=` values of X-Flagnetic-Signature is the lower-case hex of
// HMAC-SHA256 over "{X-Flagnetic-Timestamp}.{body}" keyed by the secret's
// bytes.
func (c ReceivedCall) Verifies(secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(c.Header.Get("X-Flagnetic-Timestamp") + "."))
	mac.Write(c.Body)
	want := "v1=" + hex.EncodeToString(mac.Sum(nil))
	for v := range strings.SplitSeq(c.Header.Get("X-Flagnetic-Signature"), ",") {
		if hmac.Equal([]byte(strings.TrimSpace(v)), []byte(want)) {
			return true
		}
	}
	return false
}

// Envelope returns the call's body decoded.
func (c ReceivedCall) Envelope(t testing.TB) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(c.Body, &out); err != nil {
		t.Fatalf("a webhook body is not a JSON object: %v: %s", err, c.Body)
	}
	return out
}

// Receiver is a webhook receiver in the test: a plain http server on
// loopback, so the server under test must allow http and private networks
// (WithWebhookTargets), that records every call and answers each with the
// next reply scripted, the last one repeating.
type Receiver struct {
	// URL is where the receiver listens, its path /hook.
	URL string

	mu      sync.Mutex
	replies []Reply
	calls   []ReceivedCall
	arrived chan struct{}
}

// StartReceiver starts a receiver answering replies in turn, the last one
// repeating; 200 to every call when there are none.
func StartReceiver(t testing.TB, replies ...Reply) *Receiver {
	t.Helper()
	if len(replies) == 0 {
		replies = []Reply{{}}
	}
	r := &Receiver{replies: replies, arrived: make(chan struct{}, 1024)}
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(srv.Close)
	r.URL = srv.URL + "/hook"
	return r
}

func (r *Receiver) serve(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	reply := r.replies[min(len(r.calls), len(r.replies)-1)]
	r.calls = append(r.calls, ReceivedCall{Header: req.Header.Clone(), Body: body})
	r.mu.Unlock()
	r.arrived <- struct{}{}
	if reply.Release != nil {
		select {
		case <-reply.Release:
		case <-req.Context().Done():
			return
		}
	}
	if reply.Delay > 0 {
		time.Sleep(reply.Delay)
	}
	if reply.Drop {
		if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
			_ = conn.Close()
		}
		return
	}
	if reply.Redirect != "" {
		w.Header().Set("Location", reply.Redirect)
	}
	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, reply.Body)
}

// Calls returns the calls received so far, in the order they arrived.
func (r *Receiver) Calls() []ReceivedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ReceivedCall(nil), r.calls...)
}

// AwaitCall waits for the next call to arrive, failing the test after ten
// seconds.
func (r *Receiver) AwaitCall(t testing.TB) {
	t.Helper()
	select {
	case <-r.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("no call reached the webhook receiver")
	}
}
