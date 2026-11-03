// Package idphttp is the HTTP client every request to an identity provider is
// made with, whatever the protocol it speaks.
package idphttp

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

const (
	// timeout bounds one request to an identity provider.
	timeout = 10 * time.Second
	// MaxResponseBytes caps what a provider may answer with, so a provider
	// that never stops talking cannot fill the replica's memory.
	MaxResponseBytes = 1 << 20
	// maxRedirects is how many redirects one request follows; via in
	// CheckRedirect already holds the first request, so the redirect being
	// checked is the len(via)-th.
	maxRedirects = 3
)

// ErrTooLarge is what reading an answer over MaxResponseBytes fails with.
var ErrTooLarge = errors.New("the identity provider's answer is larger than 1 MiB")

// NewClient returns a client whose requests time out, follow a few redirects
// at most, never downgrade to plain http away from a loopback address, and
// read no more than a megabyte of an answer. It connects to private networks:
// only an instance admin names a provider.
func NewClient() *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("the identity provider redirected too many times")
			}
			if req.URL.Scheme != "https" && !identity.IsLoopbackHost(req.URL.Hostname()) {
				return errors.New("the identity provider redirected to a plain http address")
			}
			return nil
		},
		Transport: capped{http.DefaultTransport},
	}
}

// capped limits the bodies of the answers of the transport it wraps.
type capped struct{ http.RoundTripper }

func (c capped) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := c.RoundTripper.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	res.Body = &limitedBody{ReadCloser: res.Body, left: MaxResponseBytes}
	return res, nil
}

type limitedBody struct {
	io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	return n, err
}
