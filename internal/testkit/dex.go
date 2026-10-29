package testkit

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

// dexImage is the Dex release the OIDC tests run against.
const dexImage = "dexidp/dex:v2.45.1"

// DexUser is a person Dex knows.
type DexUser struct {
	Email    string
	Password string
	Groups   []string
}

// Dex is a running Dex, an OpenID Connect provider, configured with one
// public client that uses PKCE and static users with groups.
type Dex struct {
	// Issuer is the address Dex's discovery document is served under; it is
	// plain http on a loopback address.
	Issuer string
	// ClientID is the public client: PKCE alone protects its code exchange, and
	// Dex accepts any loopback address as its redirect URI.
	ClientID string
	// Users are the people Dex knows, all with verified emails.
	Users []DexUser
}

// DexUsers are the people every Dex of the test kit knows.
var DexUsers = []DexUser{
	{Email: "ada@example.com", Password: "ada-password", Groups: []string{"engineering", "admins"}},
	{Email: "grace@example.com", Password: "grace-password", Groups: []string{"engineering"}},
	{Email: "alan@example.org", Password: "alan-password"},
}

// dexClientID is the id of the client Dex is configured with.
const dexClientID = "flagnetic"

var dex struct {
	once      sync.Once
	container testcontainers.Container
	value     *Dex
	err       error
}

// StartDex returns the package's Dex, starting it on first use: one container
// serves every test of the package, and ends with the package's tests.
func StartDex(t testing.TB) *Dex {
	t.Helper()
	dex.once.Do(func() { dex.value, dex.container, dex.err = runDex() })
	if dex.err != nil {
		t.Fatalf("starting Dex: %v", dex.err)
	}
	return dex.value
}

// stopDex ends the package's Dex, if the tests started one.
func stopDex() {
	if dex.container != nil {
		_ = testcontainers.TerminateContainer(dex.container)
	}
}

func runDex() (*Dex, testcontainers.Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const configPath = "/etc/dex/config.yaml"
	d := &Dex{ClientID: dexClientID, Users: DexUsers}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        dexImage,
			ExposedPorts: []string{"5556/tcp"},
			// Dex's issuer names the port Docker maps, known only once the
			// container runs, so Dex waits for its configuration to be copied in.
			Entrypoint: []string{"/bin/sh"},
			Cmd:        []string{"-c", "while [ ! -f " + configPath + " ]; do sleep 0.1; done; exec dex serve " + configPath},
			LifecycleHooks: []testcontainers.ContainerLifecycleHooks{{
				PostStarts: []testcontainers.ContainerHook{func(ctx context.Context, c testcontainers.Container) error {
					host, err := c.Host(ctx)
					if err != nil {
						return err
					}
					port, err := c.MappedPort(ctx, "5556/tcp")
					if err != nil {
						return err
					}
					d.Issuer = fmt.Sprintf("http://%s:%s/dex", host, port.Port())
					if err := c.CopyToContainer(ctx, []byte(dexConfig(d)), configPath, 0o644); err != nil {
						return err
					}
					return wait.ForHTTP("/dex/.well-known/openid-configuration").WithPort("5556/tcp").
						WithStartupTimeout(time.Minute).WaitUntilReady(ctx, c)
				}},
			}},
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, err
	}
	return d, c, nil
}

// dexConfig renders the configuration of d: in memory, with the password
// database on, and no approval screen.
func dexConfig(d *Dex) string {
	var b strings.Builder
	fmt.Fprintf(&b, "issuer: %s\nstorage:\n  type: memory\nweb:\n  http: 0.0.0.0:5556\nenablePasswordDB: true\n", d.Issuer)
	b.WriteString("oauth2:\n  skipApprovalScreen: true\n  passwordConnector: local\n")
	fmt.Fprintf(&b, "staticClients:\n  - id: %s\n    name: Flagnetic\n    public: true\n    redirectURIs:\n      - http://localhost/callback\n", d.ClientID)
	b.WriteString("staticPasswords:\n")
	for i, u := range d.Users {
		// The hash is bcrypt of the user's password, as Dex asks for.
		fmt.Fprintf(&b, "  - email: %q\n    hash: %q\n    username: %q\n    userID: %q\n", u.Email, dexHash(u.Password), u.Email, fmt.Sprintf("user-%d", i+1))
		if len(u.Groups) > 0 {
			fmt.Fprintf(&b, "    groups: [%s]\n", strings.Join(quoted(u.Groups), ", "))
		}
	}
	return b.String()
}

func quoted(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// dexHash returns the bcrypt hash of password at the default cost: Dex refuses
// to log a person in whose hash is cheaper.
func dexHash(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic("testkit: hashing a Dex password: " + err.Error()) // only a password over 72 bytes fails
	}
	return string(h)
}
