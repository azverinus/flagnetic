package testkit

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mailpit"
)

// mailpitImage is the Mailpit every mail test meets: a real SMTP server that
// keeps what it receives and answers for it over HTTP.
const mailpitImage = "axllent/mailpit:v1.32.0"

// MailpitMode is how a Mailpit receives mail.
type MailpitMode string

// The ways a Mailpit receives mail; each is a container of its own, started
// the first time a test of the package asks for it and stopped after the
// package's last test.
const (
	// MailpitOpen takes mail over a plain connection from anyone.
	MailpitOpen MailpitMode = "open"
	// MailpitAuth takes mail over a plain connection only from MailpitUser.
	MailpitAuth MailpitMode = "auth"
	// MailpitStartTLS takes mail only after STARTTLS, from MailpitUser.
	MailpitStartTLS MailpitMode = "starttls"
	// MailpitTLS takes mail only over TLS from the first byte, from
	// MailpitUser.
	MailpitTLS MailpitMode = "tls"
	// MailpitPicky takes mail over a plain connection only from MailpitUser,
	// and refuses every recipient but those at picky.example: the refusal
	// comes after a login the server accepted.
	MailpitPicky MailpitMode = "picky"
)

// The credentials of the Mailpit modes that ask for them.
const (
	MailpitUser = "relay"
	// The credentials guard a throwaway container.
	MailpitPassword = "mailpit-relay-pass-8812" //nolint:gosec // a test credential
)

// MailpitAllowedDomain is the only domain a MailpitPicky takes mail for.
const MailpitAllowedDomain = "picky.example"

// MailpitRoots are the roots the SMTP servers' certificates are signed by;
// a test serving with WithMailRoots trusts them.
func MailpitRoots() *x509.CertPool { return fixtures.authority().pool }

var fixtures mailpitFixtures

type mailpitFixtures struct {
	mu       sync.Mutex
	ca       *testAuthority
	started  map[MailpitMode]*Mailpit
	stoppers []func()
}

func (f *mailpitFixtures) authority() *testAuthority {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authorityLocked()
}

func (f *mailpitFixtures) authorityLocked() *testAuthority {
	if f.ca == nil {
		f.ca = newTestAuthority()
	}
	return f.ca
}

// stopMailpits ends the containers the package started.
func stopMailpits() {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	for _, stop := range fixtures.stoppers {
		stop()
	}
	fixtures.stoppers, fixtures.started = nil, nil
}

// Mailpit is a running Mailpit.
type Mailpit struct {
	// Host and Port are where its SMTP server listens, on loopback.
	Host string
	Port int
	// API is the base URL of its HTTP interface.
	API  string
	mode MailpitMode
}

// StartMailpit returns the package's Mailpit in mode, starting it on first
// use. Tests share it and isolate by recipient: each mail a test sends goes to
// an address of its own.
func StartMailpit(t testing.TB, mode MailpitMode) *Mailpit {
	t.Helper()
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	if m, ok := fixtures.started[mode]; ok {
		return m
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithEnv(map[string]string{"MP_MAX_MESSAGES": "5000"}),
	}
	switch mode {
	case MailpitOpen:
	case MailpitAuth:
		opts = append(opts, mailpit.WithSMTPAuth(MailpitUser, MailpitPassword))
	case MailpitStartTLS, MailpitTLS:
		cert, key := fixtures.authorityLocked().serverPEM()
		// Mailpit refuses to require TLS and allow credentials in the clear
		// together, so its credentials are set without the module's helper.
		env := map[string]string{
			"MP_SMTP_TLS_CERT": "/certs/tls.crt", "MP_SMTP_TLS_KEY": "/certs/tls.key",
			"MP_SMTP_AUTH": MailpitUser + ":" + MailpitPassword,
		}
		if mode == MailpitStartTLS {
			env["MP_SMTP_REQUIRE_STARTTLS"] = "true"
		} else {
			env["MP_SMTP_REQUIRE_TLS"] = "true"
		}
		opts = append(opts, testcontainers.WithEnv(env),
			testcontainers.WithFiles(
				testcontainers.ContainerFile{Reader: bytes.NewReader(cert), ContainerFilePath: "/certs/tls.crt", FileMode: 0o644},
				testcontainers.ContainerFile{Reader: bytes.NewReader(key), ContainerFilePath: "/certs/tls.key", FileMode: 0o644},
			))
	case MailpitPicky:
		opts = append(opts, mailpit.WithSMTPAuth(MailpitUser, MailpitPassword), testcontainers.WithEnv(map[string]string{
			"MP_SMTP_ALLOWED_RECIPIENTS": "@" + strings.ReplaceAll(MailpitAllowedDomain, ".", `\.`) + "$",
		}))
	default:
		t.Fatalf("no Mailpit mode %q", mode)
	}
	c, err := mailpit.Run(ctx, mailpitImage, opts...)
	if c != nil {
		fixtures.stoppers = append(fixtures.stoppers, func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		t.Fatalf("starting Mailpit (is Docker running?): %v", err)
	}
	smtpEndpoint, err := c.SMTPEndpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(smtpEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	api, err := c.HTTPURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := &Mailpit{Host: host, Port: port, API: api, mode: mode}
	if fixtures.started == nil {
		fixtures.started = map[MailpitMode]*Mailpit{}
	}
	fixtures.started[mode] = m
	return m
}

// Security is how a client reaches this Mailpit's SMTP server, as the SMTP
// settings name it.
func (m *Mailpit) Security() string {
	switch m.mode {
	case MailpitStartTLS:
		return "starttls"
	case MailpitTLS:
		return "tls"
	}
	return "none"
}

// Credentials reports whether this Mailpit asks the client to authenticate.
func (m *Mailpit) Credentials() bool {
	return m.mode != MailpitOpen
}

// ReceivedMail is a mail Mailpit holds.
type ReceivedMail struct {
	ID      string
	From    string
	To      []string
	Subject string
	Text    string
	HTML    string
	Date    time.Time
}

// AwaitMails returns the mails sent to address once there are n of them,
// failing after 10 seconds. Mail leaves a replica in the background, so a
// test waits for it as the recipient would.
func (m *Mailpit) AwaitMails(t testing.TB, address string, n int) []ReceivedMail {
	t.Helper()
	var got []ReceivedMail
	for deadline := time.Now().Add(10 * time.Second); ; {
		got = m.MailsTo(t, address)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d mails to %s after 10 seconds, want %d", len(got), address, n)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// MailsTo returns the mails sent to address, oldest first.
func (m *Mailpit) MailsTo(t testing.TB, address string) []ReceivedMail {
	t.Helper()
	var list struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	m.get(t, "/api/v1/search?query="+url.QueryEscape("to:"+address), &list)
	var out []ReceivedMail
	for i := len(list.Messages) - 1; i >= 0; i-- {
		var msg struct {
			ID                  string
			From                struct{ Address string }
			To                  []struct{ Address string }
			Subject, Text, HTML string
			Date                time.Time
		}
		m.get(t, "/api/v1/message/"+list.Messages[i].ID, &msg)
		got := ReceivedMail{ID: msg.ID, From: msg.From.Address, Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML, Date: msg.Date}
		for _, to := range msg.To {
			got.To = append(got.To, to.Address)
		}
		out = append(out, got)
	}
	return out
}

// AssertNoMail fails if address has been sent a mail, after the replica's
// background work is done.
func (m *Mailpit) AssertNoMail(t testing.TB, address string) {
	t.Helper()
	if got := m.MailsTo(t, address); len(got) != 0 {
		t.Errorf("%d mails to %s, want none: %q", len(got), address, got[0].Subject)
	}
}

func (m *Mailpit) get(t testing.TB, path string, into any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.API+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Mailpit %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("Mailpit %s = %d, %v: %s", path, res.StatusCode, err, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("Mailpit %s: %v: %s", path, err, body)
	}
}

// testAuthority signs the certificate the TLS Mailpits present.
type testAuthority struct {
	pool *x509.CertPool
	cert []byte
	key  []byte
}

func newTestAuthority() *testAuthority {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Flagnetic test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		panic(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		panic(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		panic(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return &testAuthority{
		pool: pool,
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
}

func (a *testAuthority) serverPEM() (cert, key []byte) { return a.cert, a.key }
