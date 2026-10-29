package testkit_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestDexServesItsDiscoveryDocumentAndAuthenticatesItsUsersWithTheirGroups(t *testing.T) {
	dex := testkit.StartDex(t)
	res, err := http.Get(dex.Issuer + "/.well-known/openid-configuration") //nolint:noctx // a test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var doc struct{ Issuer string }
	if err = json.NewDecoder(res.Body).Decode(&doc); err != nil || doc.Issuer != dex.Issuer {
		t.Fatalf("discovery issuer = %q (%v), want %q", doc.Issuer, err, dex.Issuer)
	}
	// The password grant stands in for a browser here: it shows the users and
	// their groups are Dex's.
	ada := dex.Users[0]
	form := url.Values{"grant_type": {"password"}, "username": {ada.Email}, "password": {ada.Password},
		"scope": {"openid email groups"}, "client_id": {dex.ClientID}}
	tok, err := http.Post(dex.Issuer+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode())) //nolint:noctx // a test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tok.Body.Close() }()
	var body struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err = json.NewDecoder(tok.Body).Decode(&body); err != nil || body.IDToken == "" {
		t.Fatalf("token answer: %+v (%v), status %d", body, err, tok.StatusCode)
	}
	parts := strings.Split(body.IDToken, ".")
	if len(parts) != 3 {
		t.Fatalf("the ID token %q is not a JWT", body.IDToken)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		Groups        []string `json:"groups"`
	}
	if err = json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Email != ada.Email || !claims.EmailVerified || !slices.Equal(claims.Groups, ada.Groups) {
		t.Errorf("claims = %+v, want %s verified in %v", claims, ada.Email, ada.Groups)
	}
}
