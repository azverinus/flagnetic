package testkit

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fault is what a ForgingIDP gets wrong in the one ID token it signs next.
type Fault int

// The mistakes a forging identity provider makes.
const (
	// SignedByOtherKey signs with a key the provider's JWKS does not publish.
	SignedByOtherKey Fault = iota + 1
	// WrongAudience addresses the token to another client.
	WrongAudience
	// WrongIssuer names another issuer.
	WrongIssuer
	// Expired dates the token ten minutes ago, beyond any clock skew.
	Expired
	// WrongNonce answers another nonce than the attempt's.
	WrongNonce
)

// ForgingIDP is an in-process OIDC provider that signs what a test asks, to
// produce what a real identity provider never sends: a token signed by
// another key, for another audience or issuer, expired, or answering another
// nonce. It fakes the outside party for refusals, never a repository.
type ForgingIDP struct {
	// Issuer is the provider's address, plain http on a loopback address.
	Issuer string
	// ClientID and ClientSecret are the only credentials it accepts.
	ClientID, ClientSecret string

	key, otherKey *rsa.PrivateKey
	srv           *httptest.Server

	mu       sync.Mutex
	codes    map[string]forgedCode
	fault    Fault
	claims   map[string]any
	userInfo map[string]any
	deny     string
}

type forgedCode struct {
	nonce, challenge, redirect string
}

// forgedRedirect returns where a forging provider sends a browser back to:
// the callback path and query under the one of the public addresses the tests
// serve an instance at that origin names, and false for any other origin. Each
// address is written out in the concatenation that builds the target, so
// nothing a request carries decides the host the browser is sent to.
func forgedRedirect(origin, path, query string) (string, bool) {
	path = strings.TrimPrefix(path, "/")
	switch origin {
	case "http://localhost:8080":
		return "http://localhost:8080/" + path + "?" + query, true
	case "https://flags.example.com":
		return "https://flags.example.com/" + path + "?" + query, true
	case "http://flags.example.com":
		return "http://flags.example.com/" + path + "?" + query, true
	}
	return "", false
}

// StartForgingIDP starts a forging provider that ends with the test.
func StartForgingIDP(t testing.TB) *ForgingIDP {
	t.Helper()
	f := &ForgingIDP{
		ClientID: "forged-client", ClientSecret: "forged-client-secret", codes: map[string]forgedCode{},
		key: newKey(t), otherKey: newKey(t),
	}
	RecordSecret(f.ClientSecret)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /jwks", f.jwks)
	mux.HandleFunc("GET /authorize", f.authorize)
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("GET /userinfo", f.userinfo)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.Issuer = f.srv.URL
	return f
}

func newKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// FailNext makes the provider get fault wrong in the next ID token it signs.
func (f *ForgingIDP) FailNext(fault Fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fault = fault
}

// NextClaims sets claims the next ID token holds over its defaults: the
// subject "subject-1", the email "ada@example.com" and email_verified true.
// A nil value removes the default claim.
func (f *ForgingIDP) NextClaims(claims map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = claims
}

// NextUserInfo sets what UserInfo answers for the next token, verbatim.
// While unset, UserInfo answers 404.
func (f *ForgingIDP) NextUserInfo(claims map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userInfo = claims
}

// DenyNext makes the next authorization end with the error code.
func (f *ForgingIDP) DenyNext(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deny = code
}

func (f *ForgingIDP) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *ForgingIDP) discovery(w http.ResponseWriter, _ *http.Request) {
	f.writeJSON(w, map[string]any{
		"issuer": f.Issuer, "authorization_endpoint": f.Issuer + "/authorize", "token_endpoint": f.Issuer + "/token",
		"jwks_uri": f.Issuer + "/jwks", "userinfo_endpoint": f.Issuer + "/userinfo",
		"id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"},
		"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
	})
}

func (f *ForgingIDP) jwks(w http.ResponseWriter, _ *http.Request) {
	f.writeJSON(w, map[string]any{"keys": []any{jwk(&f.key.PublicKey, "key-1")}})
}

func jwk(k *rsa.PublicKey, kid string) map[string]string {
	return map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
	}
}

// authorize sends the browser straight back with a code, as an identity
// provider does for a person already signed in.
func (f *ForgingIDP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || q.Get("client_id") != f.ClientID || back.User != nil ||
		!strings.HasPrefix(back.Path, "/sso/") || !strings.HasSuffix(back.Path, "/callback") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// An origin the tests do not serve is refused before the attempt's state
	// is taken, so a refused request leaves a denial or a code unspent.
	origin := back.Scheme + "://" + back.Host
	if _, ok := forgedRedirect(origin, back.Path, ""); !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	out := back.Query()
	out.Set("state", q.Get("state"))
	f.mu.Lock()
	deny := f.deny
	f.deny = ""
	f.mu.Unlock()
	if deny != "" {
		out.Set("error", deny)
	} else {
		code := rand.Text()
		f.mu.Lock()
		f.codes[code] = forgedCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
		f.mu.Unlock()
		out.Set("code", code)
	}
	target, _ := forgedRedirect(origin, back.EscapedPath(), out.Encode())
	http.Redirect(w, r, target, http.StatusFound)
}

func (f *ForgingIDP) token(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	if id, secret = mustUnescape(id), mustUnescape(secret); id != f.ClientID || secret != f.ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		f.writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	f.mu.Lock()
	issued, known := f.codes[r.PostFormValue("code")]
	delete(f.codes, r.PostFormValue("code"))
	fault, claims := f.fault, f.claims
	f.fault, f.claims = 0, nil
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
	if !known || issued.redirect != r.PostFormValue("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != issued.challenge {
		w.WriteHeader(http.StatusBadRequest)
		f.writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	f.writeJSON(w, map[string]any{
		"access_token": "access-" + rand.Text(), "token_type": "Bearer", "expires_in": 3600,
		"id_token": f.idToken(issued.nonce, fault, claims),
	})
}

func mustUnescape(s string) string {
	u, err := url.QueryUnescape(s)
	if err != nil {
		return s
	}
	return u
}

func (f *ForgingIDP) idToken(nonce string, fault Fault, overrides map[string]any) string {
	now := time.Now()
	claims := map[string]any{
		"iss": f.Issuer, "aud": f.ClientID, "sub": "subject-1", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
		"nonce": nonce, "email": "ada@example.com", "email_verified": true, "name": "Ada Lovelace",
	}
	maps.Copy(claims, overrides)
	for k, v := range claims {
		if v == nil {
			delete(claims, k)
		}
	}
	key := f.key
	switch fault {
	case SignedByOtherKey:
		key = f.otherKey
	case WrongAudience:
		claims["aud"] = "another-client"
	case WrongIssuer:
		claims["iss"] = "https://another-issuer.example.com"
	case Expired:
		claims["exp"], claims["iat"] = now.Add(-10*time.Minute).Unix(), now.Add(-20*time.Minute).Unix()
	case WrongNonce:
		claims["nonce"] = "another-" + nonce
	}
	return signJWT(key, claims)
}

func signJWT(key *rsa.PrivateKey, claims map[string]any) string {
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "key-1"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(fmt.Sprint("testkit: signing a token: ", err))
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *ForgingIDP) userinfo(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	claims := f.userInfo
	f.mu.Unlock()
	if claims == nil || r.Header.Get("Authorization") == "" {
		http.NotFound(w, r)
		return
	}
	f.writeJSON(w, claims)
}
