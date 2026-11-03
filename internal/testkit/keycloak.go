package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// keycloakImage is the Keycloak release the SAML tests run against.
const keycloakImage = "quay.io/keycloak/keycloak:26.4"

// keycloakRealm is the name of the realm Keycloak is started with.
const keycloakRealm = "flagnetic"

// KeycloakUser is a person Keycloak knows.
type KeycloakUser struct {
	Username  string
	Email     string
	FirstName string
	Password  string
	Groups    []string
}

// KeycloakUsers are the people every Keycloak of the test kit knows, all with
// verified emails.
var KeycloakUsers = []KeycloakUser{
	{Username: "ada", Email: "ada@saml.example.com", FirstName: "Ada", Password: "ada-password", Groups: []string{"engineering", "admins"}},
	{Username: "grace", Email: "grace@saml.example.com", FirstName: "Grace", Password: "grace-password", Groups: []string{"engineering"}},
	{Username: "alan", Email: "alan@saml.example.org", FirstName: "Alan", Password: "alan-password"},
}

// Keycloak is a running Keycloak with a realm holding SAML clients, one for
// each slug KeycloakSlug hands out, and the people KeycloakUsers lists. A
// client's assertions are signed, carry the attributes email and name and the
// person's groups as the attribute groups, and name the person by username.
type Keycloak struct {
	// MetadataURL is the address of the realm's identity provider metadata,
	// plain http on a loopback address.
	MetadataURL string
	// Users are the people Keycloak knows.
	Users []KeycloakUser
}

// KeycloakPublicURL is the public address of the instance the SAML clients'
// entity IDs and ACS addresses are registered for.
const KeycloakPublicURL = "http://localhost:8080"

// keycloakSlugs is how many provider slugs Keycloak has a client registered
// for.
const keycloakSlugs = 32

var nextKeycloakSlug atomic.Int64

// KeycloakSlug returns a provider slug that Keycloak has a SAML client for,
// under KeycloakPublicURL, a new one at each call: a client is registered by
// its entity ID, which names the slug, and the container is started once per
// package.
func KeycloakSlug(t testing.TB) string {
	t.Helper()
	n := nextKeycloakSlug.Add(1)
	if n > keycloakSlugs {
		t.Fatalf("more than %d providers signed in through Keycloak in one package", keycloakSlugs)
	}
	return fmt.Sprintf("keycloak-%d", n)
}

var keycloak struct {
	once      sync.Once
	container testcontainers.Container
	value     *Keycloak
	err       error
}

// StartKeycloak returns the package's Keycloak, starting it on first use: one
// container serves every test of the package, and ends with the package's
// tests.
func StartKeycloak(t testing.TB) *Keycloak {
	t.Helper()
	keycloak.once.Do(func() { keycloak.value, keycloak.container, keycloak.err = runKeycloak() })
	if keycloak.err != nil {
		t.Fatalf("starting Keycloak: %v", keycloak.err)
	}
	return keycloak.value
}

// stopKeycloak ends the package's Keycloak, if the tests started one.
func stopKeycloak() {
	if keycloak.container != nil {
		_ = testcontainers.TerminateContainer(keycloak.container)
	}
}

func runKeycloak() (*Keycloak, testcontainers.Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	realm, err := json.Marshal(keycloakRealmJSON(KeycloakUsers))
	if err != nil {
		return nil, nil, err
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        keycloakImage,
			ExposedPorts: []string{"8080/tcp"},
			Cmd:          []string{"start-dev", "--import-realm"},
			Files: []testcontainers.ContainerFile{{
				Reader: bytes.NewReader(realm), ContainerFilePath: "/opt/keycloak/data/import/realm.json", FileMode: 0o644,
			}},
			WaitingFor: wait.ForHTTP("/realms/" + keycloakRealm + "/protocol/saml/descriptor").WithPort("8080/tcp").
				WithStartupTimeout(4 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, err
	}
	port, err := c.MappedPort(ctx, "8080/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, err
	}
	return &Keycloak{
		MetadataURL: fmt.Sprintf("http://%s:%s/realms/%s/protocol/saml/descriptor", host, port.Port(), keycloakRealm), Users: KeycloakUsers,
	}, c, nil
}

// keycloakRealmJSON is the realm Keycloak imports: the people, their groups
// and a SAML client for each slug.
func keycloakRealmJSON(users []KeycloakUser) map[string]any {
	groups := map[string]bool{}
	people := make([]map[string]any, len(users))
	for i, u := range users {
		paths := make([]string, len(u.Groups))
		for j, g := range u.Groups {
			groups[g] = true
			paths[j] = "/" + g
		}
		people[i] = map[string]any{
			"username": u.Username, "email": u.Email, "emailVerified": true, "enabled": true, "firstName": u.FirstName, "lastName": "Tester",
			"credentials": []map[string]any{{"type": "password", "value": u.Password, "temporary": false}},
			"groups":      paths,
		}
	}
	var groupList []map[string]any
	for g := range groups {
		groupList = append(groupList, map[string]any{"name": g, "path": "/" + g})
	}
	clients := make([]map[string]any, keycloakSlugs)
	for i := range clients {
		clients[i] = keycloakClient(fmt.Sprintf("keycloak-%d", i+1))
	}
	return map[string]any{
		"realm": keycloakRealm, "enabled": true, "sslRequired": "none", "users": people, "groups": groupList, "clients": clients,
	}
}

// keycloakClient is the SAML client for the provider with slug: the entity ID
// and the ACS the instance computes for it, assertions signed, the person's
// username as the NameID, and the attributes the tests read.
func keycloakClient(slug string) map[string]any {
	entity := KeycloakPublicURL + "/sso/" + slug + "/metadata"
	acs := KeycloakPublicURL + "/sso/" + slug + "/acs"
	mapper := func(name, property string) map[string]any {
		return map[string]any{
			"name": name, "protocol": "saml", "protocolMapper": "saml-user-property-mapper",
			"config": map[string]any{
				"user.attribute": property, "attribute.name": name, "attribute.nameformat": "Basic", "friendly.name": name,
			},
		}
	}
	return map[string]any{
		"clientId": entity, "protocol": "saml", "enabled": true, "name": slug,
		"redirectUris": []string{acs}, "baseUrl": acs,
		"attributes": map[string]any{
			"saml_assertion_consumer_url_post": acs,
			"saml_name_id_format":              "username",
			"saml.server.signature":            "false",
			"saml.assertion.signature":         "true",
			"saml.signature.algorithm":         "RSA_SHA256",
			"saml.client.signature":            "false",
			"saml.force.post.binding":          "true",
			"saml.authnstatement":              "true",
			"saml_force_name_id_format":        "false",
		},
		"protocolMappers": []map[string]any{
			mapper("email", "email"), mapper("name", "firstName"),
			{
				"name": "groups", "protocol": "saml", "protocolMapper": "saml-group-membership-mapper",
				"config": map[string]any{"attribute.name": "groups", "attribute.nameformat": "Basic", "single": "false", "full.path": "false"},
			},
		},
	}
}

var (
	htmlInput = regexp.MustCompile(`(?is)<input[^>]*?name="([^"]+)"[^>]*?value="([^"]*)"`)
	htmlForm  = regexp.MustCompile(`(?is)<form[^>]*?action="([^"]+)"`)
)

// KeycloakLogin answers Keycloak's pages as user: its login form with the
// person's credentials, and the page that hands the browser the SAML response
// to post to the ACS.
func KeycloakLogin(user KeycloakUser) func(page string) (string, url.Values, bool) {
	return func(page string) (string, url.Values, bool) {
		form := htmlForm.FindStringSubmatch(page)
		if form == nil {
			return "", nil, false
		}
		action := html.UnescapeString(form[1])
		fields := url.Values{}
		for _, m := range htmlInput.FindAllStringSubmatch(page, -1) {
			fields.Set(html.UnescapeString(m[1]), html.UnescapeString(m[2]))
		}
		if fields.Get("SAMLResponse") != "" {
			return action, fields, true
		}
		return action, url.Values{"username": {user.Username}, "password": {user.Password}}, true
	}
}
