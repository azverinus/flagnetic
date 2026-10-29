// Package oidc is the outbound adapter to OpenID Connect providers, built on
// go-oidc.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
)

const (
	// discoveryTimeout bounds one fetch of a discovery document.
	discoveryTimeout = 10 * time.Second
	// maxResponseBytes caps what a provider may answer with, so a provider
	// that never stops talking cannot fill the replica's memory.
	maxResponseBytes = 1 << 20
	maxRedirects     = 3
)

var errTooLarge = errors.New("the identity provider's answer is larger than 1 MiB")

// Discoverer reads OIDC providers' discovery documents.
type Discoverer struct{ client *http.Client }

var _ appidentity.OIDCDiscoverer = (*Discoverer)(nil)

// NewDiscoverer returns a discoverer whose requests time out, follow a few
// redirects at most, never downgrade to plain http away from a loopback
// address, and read no more than a megabyte of an answer. It connects to
// private networks: only an instance admin names a provider.
func NewDiscoverer() *Discoverer {
	return &Discoverer{client: &http.Client{
		Timeout: discoveryTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("the identity provider redirected too many times")
			}
			if req.URL.Scheme != "https" && !identity.IsLoopbackHost(req.URL.Hostname()) {
				return errors.New("the identity provider redirected to a plain http address")
			}
			return nil
		},
		Transport: capped{http.DefaultTransport},
	}}
}

// capped limits the bodies of the answers of the transport it wraps.
type capped struct{ http.RoundTripper }

func (c capped) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := c.RoundTripper.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	res.Body = &limitedBody{ReadCloser: res.Body, left: maxResponseBytes}
	return res, nil
}

type limitedBody struct {
	io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	return n, err
}

// Discover fetches the discovery document of issuer and checks it names
// issuer itself.
func (d *Discoverer) Discover(ctx context.Context, issuer string) (appidentity.OIDCDiscovery, error) {
	p, err := gooidc.NewProvider(gooidc.ClientContext(ctx, d.client), issuer)
	if err != nil {
		return appidentity.OIDCDiscovery{}, err
	}
	var doc struct {
		JWKSURI                       string   `json:"jwks_uri"`
		ScopesSupported               []string `json:"scopes_supported"`
		CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	}
	if err = p.Claims(&doc); err != nil {
		return appidentity.OIDCDiscovery{}, fmt.Errorf("reading the discovery document: %w", err)
	}
	endpoint := p.Endpoint()
	return appidentity.OIDCDiscovery{
		Issuer: issuer, AuthorizationEndpoint: endpoint.AuthURL, TokenEndpoint: endpoint.TokenURL, JWKSURI: doc.JWKSURI,
		UserinfoEndpoint: p.UserInfoEndpoint(), ScopesSupported: doc.ScopesSupported,
		CodeChallengeMethodsSupported: doc.CodeChallengeMethodsSupported,
	}, nil
}
