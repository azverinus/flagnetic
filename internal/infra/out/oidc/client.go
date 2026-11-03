package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
)

const (
	// discoveryTTL is how long a replica keeps an issuer's discovery document:
	// long enough that a burst of sign-ins fetches it once, short enough that a
	// provider's change of keys or endpoints reaches the replica within a
	// minute.
	discoveryTTL = time.Minute
	// clockSkew is how far an ID token's times may be off the replica's clock.
	clockSkew = 2 * time.Minute
)

// Client signs people in through OIDC providers, built on go-oidc and
// golang.org/x/oauth2.
type Client struct {
	http *http.Client
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]discovered
}

type discovered struct {
	provider *gooidc.Provider
	fetched  time.Time
}

var _ appidentity.OIDCClient = (*Client)(nil)

// NewClient returns a client whose requests to providers time out, follow a
// few redirects at most, never downgrade to plain http away from a loopback
// address and read no more than a megabyte of an answer.
func NewClient() *Client {
	return &Client{http: newHTTPClient(), now: time.Now, cache: map[string]discovered{}}
}

// provider returns the discovered provider of issuer, from the replica's
// short-lived cache when it holds a fresh one. The endpoints the document
// names must be https too, except on a loopback address: they decide where
// the client secret and the authorization code are sent.
func (c *Client) provider(ctx context.Context, issuer string) (*gooidc.Provider, error) {
	c.mu.Lock()
	d, ok := c.cache[issuer]
	c.mu.Unlock()
	if ok && c.now().Sub(d.fetched) < discoveryTTL {
		return d.provider, nil
	}
	p, err := gooidc.NewProvider(gooidc.ClientContext(ctx, c.http), issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering %s: %w", issuer, err)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err = p.Claims(&doc); err != nil {
		return nil, fmt.Errorf("reading the discovery document of %s: %w", issuer, err)
	}
	endpoint := p.Endpoint()
	for name, address := range map[string]string{
		"authorization_endpoint": endpoint.AuthURL, "token_endpoint": endpoint.TokenURL, "jwks_uri": doc.JWKSURI,
		"userinfo_endpoint": p.UserInfoEndpoint(),
	} {
		if err = checkEndpoint(name, address); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.cache[issuer] = discovered{provider: p, fetched: c.now()}
	c.mu.Unlock()
	return p, nil
}

// checkEndpoint refuses an endpoint of a provider's discovery document that
// is not https, unless it is on a loopback address. An absent optional one,
// "", is fine.
func checkEndpoint(name, address string) error {
	if address == "" {
		return nil
	}
	u, err := url.Parse(address)
	if err == nil && (u.Scheme == "https" || (u.Scheme == "http" && identity.IsLoopbackHost(u.Hostname()))) {
		return nil
	}
	return fmt.Errorf("the discovery document's %s is not https", name)
}

func oauthConfig(cfg appidentity.OIDCConfig, p *gooidc.Provider) oauth2.Config {
	return oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret.Reveal(), Endpoint: p.Endpoint(), RedirectURL: cfg.RedirectURL,
		Scopes: cfg.Scopes,
	}
}

// AuthorizationURL returns where a browser is sent to authenticate: the
// authorization code flow with the state, the nonce and a PKCE S256
// challenge of codeVerifier.
func (c *Client) AuthorizationURL(ctx context.Context, cfg appidentity.OIDCConfig, state, nonce, codeVerifier string) (string, error) {
	p, err := c.provider(ctx, cfg.Issuer)
	if err != nil {
		return "", err
	}
	oc := oauthConfig(cfg, p)
	return oc.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(codeVerifier)), nil
}

// Exchange trades code for tokens and verifies the ID token: signature,
// issuer, audience and expiry through go-oidc, the nonce here. It reads the
// claims cfg names. The email is asked of UserInfo when the token holds
// none, and only when UserInfo answers for the same subject; its
// email_verified is judged on its own, never taken from the token.
func (c *Client) Exchange(ctx context.Context, cfg appidentity.OIDCConfig, code, codeVerifier, nonce string) (identity.VerifiedClaims, error) {
	ctx = gooidc.ClientContext(ctx, c.http)
	p, err := c.provider(ctx, cfg.Issuer)
	if err != nil {
		return identity.VerifiedClaims{}, err
	}
	oc := oauthConfig(cfg, p)
	token, err := oc.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return identity.VerifiedClaims{}, fmt.Errorf("exchanging the code: %w", err)
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return identity.VerifiedClaims{}, errors.New("the token response holds no ID token")
	}
	verifier := p.Verifier(&gooidc.Config{ClientID: cfg.ClientID, Now: func() time.Time { return c.now().Add(-clockSkew) }})
	idToken, err := verifier.Verify(ctx, raw)
	if err != nil {
		return identity.VerifiedClaims{}, fmt.Errorf("verifying the ID token: %w", err)
	}
	if idToken.Nonce != nonce {
		return identity.VerifiedClaims{}, errors.New("the ID token's nonce is not the attempt's")
	}
	if idToken.Subject == "" {
		return identity.VerifiedClaims{}, errors.New("the ID token has no subject")
	}
	var claims map[string]any
	if err = idToken.Claims(&claims); err != nil {
		return identity.VerifiedClaims{}, fmt.Errorf("reading the ID token's claims: %w", err)
	}
	out := readClaims(claims, cfg.Claims)
	out.Subject = idToken.Subject
	if out.Email == "" {
		if info, infoErr := c.userInfo(ctx, p, token, idToken.Subject); infoErr == nil {
			out.Email, out.EmailVerified = info.Email, info.EmailVerified
			if out.Name == "" {
				out.Name = info.Name
			}
		}
	}
	return out, nil
}

// userInfo reads the claims UserInfo holds for the token's subject; an error
// when it holds none or answers for another subject.
func (c *Client) userInfo(ctx context.Context, p *gooidc.Provider, token *oauth2.Token, subject string) (identity.VerifiedClaims, error) {
	if p.UserInfoEndpoint() == "" {
		return identity.VerifiedClaims{}, errors.New("the provider has no UserInfo endpoint")
	}
	info, err := p.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return identity.VerifiedClaims{}, err
	}
	if info.Subject != subject {
		return identity.VerifiedClaims{}, errors.New("UserInfo answers for another subject")
	}
	var claims map[string]any
	if err = info.Claims(&claims); err != nil {
		return identity.VerifiedClaims{}, err
	}
	return readClaims(claims, identityClaims), nil
}

// identityClaims are the names UserInfo's claims are read under: the provider
// configures the claim names of its tokens, and a UserInfo answer holds the
// standard ones.
var identityClaims = identity.Claims{Email: "email", Name: "name"}

// readClaims reads the email, its verification and the name from claims under
// the names c gives. A verification counts only as the JSON boolean true: a
// string, a number or a missing claim does not vouch for the address.
func readClaims(claims map[string]any, c identity.Claims) identity.VerifiedClaims {
	email, _ := claims[c.Email].(string)
	name, _ := claims[c.Name].(string)
	verified, _ := claims["email_verified"].(bool)
	return identity.VerifiedClaims{Email: email, EmailVerified: verified && email != "", Name: name}
}
