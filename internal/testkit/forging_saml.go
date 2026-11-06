package testkit

import (
	"bytes"
	"compress/flate"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // RSA-OAEP's own digest, the one SAML service providers default to
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

// SAMLFault is what a ForgingSAMLIDP gets wrong in the one response it builds.
type SAMLFault int

// The mistakes a forging SAML identity provider makes.
const (
	// SAMLUnsigned signs neither the response nor the assertion.
	SAMLUnsigned SAMLFault = iota + 1
	// SAMLSignedByOtherKey signs with a key the provider's metadata does not publish.
	SAMLSignedByOtherKey
	// SAMLWrongAudience addresses the assertion to another audience.
	SAMLWrongAudience
	// SAMLWrongDestination names another destination than the ACS the response is posted to.
	SAMLWrongDestination
	// SAMLWrongInResponseTo answers another request than the attempt's.
	SAMLWrongInResponseTo
	// SAMLNoInResponseTo answers no request: an unsolicited response.
	SAMLNoInResponseTo
	// SAMLExpired dates the assertion's validity ten minutes ago, beyond any clock skew.
	SAMLExpired
	// SAMLNotYetValid starts the assertion's validity ten minutes from now.
	SAMLNotYetValid
	// SAMLWrongIssuer names another entity as the issuer.
	SAMLWrongIssuer
	// SAMLWrapped signs an assertion about one person and wraps it, as an attacker would,
	// beside an unsigned assertion about another.
	SAMLWrapped
	// SAMLDocType puts a DOCTYPE in front of an otherwise valid response.
	SAMLDocType
	// SAMLFailedStatus answers that the person did not authenticate, with no assertion.
	SAMLFailedStatus
	// SAMLNoAudience leaves the audience restriction out.
	SAMLNoAudience
)

// SAMLEncryption is how a ForgingSAMLIDP encrypts the assertion.
type SAMLEncryption int

// The encryptions of an assertion.
const (
	// SAMLPlain leaves the assertion readable.
	SAMLPlain SAMLEncryption = iota
	// SAMLAES128CBC encrypts with AES-128-CBC, the key sent by RSA-OAEP.
	SAMLAES128CBC
	// SAMLAES256GCM encrypts with AES-256-GCM, the key sent by RSA-OAEP.
	SAMLAES256GCM
	// SAMLRSA15 encrypts with AES-128-CBC, the key sent by RSA 1.5, which the instance refuses.
	SAMLRSA15
	// SAMLTripleDES names Triple DES in CBC mode as the encryption, the key sent by RSA-OAEP, which the
	// instance refuses.
	SAMLTripleDES
)

// SAMLAssertion is what a ForgingSAMLIDP asserts of a person, and what it
// gets wrong.
type SAMLAssertion struct {
	// Subject is the NameID; "subject-1" when empty. NameIDFormat is its
	// format: unspecified when empty.
	Subject      string
	NameIDFormat string
	// Attributes are the assertion's attributes, name to values. Nil asserts
	// an email of "ada@example.com" and a name of "Ada Lovelace".
	Attributes map[string][]string
	// ID is the assertion's ID; random when empty.
	ID string
	// SignResponse signs the Response element instead of the assertion.
	SignResponse bool
	Fault        SAMLFault
	Encryption   SAMLEncryption
	// Until, when not zero, is when the assertion's validity ends, from now:
	// negative for a validity that ended already.
	Until time.Duration
	// EncryptTo is the certificate, PEM encoded, the assertion is encrypted to.
	EncryptTo []byte
}

// SAMLRequest is an AuthnRequest a forging provider was sent.
type SAMLRequest struct {
	ID, Issuer, Destination, ACS, NameIDFormat string
	RelayState                                 string
	// Signed is whether the request's query carried a signature that verified
	// against a certificate of the service provider's metadata.
	Signed bool
	// Query is the raw query the browser was sent with.
	Query string
}

// SAMLMetadataOptions are the mistakes a forging provider makes in its metadata.
type SAMLMetadataOptions struct {
	// NoRedirectBinding offers the HTTP-POST binding only.
	NoRedirectBinding bool
	// NoCertificate publishes no signing certificate.
	NoCertificate bool
	// OtherEntityID is the entity ID the metadata names instead of the provider's.
	OtherEntityID string
}

// ForgingSAMLIDP is an in-process SAML identity provider that builds what a
// test asks, to produce what a real one never sends: a response unsigned or
// signed by another key, for another audience or destination, expired,
// replayed, wrapped, encrypted with a key transport the instance refuses. It
// fakes the outside party for refusals, never a repository.
type ForgingSAMLIDP struct {
	// EntityID, SSOURL and MetadataURL are its addresses, plain http on a
	// loopback address.
	EntityID, SSOURL, MetadataURL string
	// Certificate is the PEM certificate it signs with.
	Certificate []byte
	// Now is the time its assertions are dated at.
	Now func() time.Time

	key, otherKey   *rsa.PrivateKey
	cert, otherCert []byte // DER
	srv             *httptest.Server

	// mu guards what the metadata endpoint serves, which a test changes while
	// the instance fetches it.
	mu sync.Mutex
	// published are the signing certificates (DER) the metadata lists, and
	// served what else of it is changed; fetches counts the requests for it.
	published [][]byte
	served    SAMLMetadataOptions
	status    int
	delay     time.Duration
	fetches   int
}

// StartForgingSAMLIDP starts a forging provider that ends with the test.
func StartForgingSAMLIDP(t testing.TB) *ForgingSAMLIDP {
	t.Helper()
	f := &ForgingSAMLIDP{Now: time.Now}
	f.key, f.cert = newSigningKey(t)
	f.otherKey, f.otherCert = newSigningKey(t)
	f.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.cert})
	mux := http.NewServeMux()
	f.published = [][]byte{f.cert}
	mux.HandleFunc("GET /metadata", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.fetches++
		status, delay, options := f.status, f.delay, f.served
		f.mu.Unlock()
		time.Sleep(delay)
		if status != 0 {
			http.Error(w, "unavailable", status)
			return
		}
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		_, _ = io.WriteString(w, f.Metadata(options))
	})
	mux.HandleFunc("GET /big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(append([]byte("<!-- "), bytes.Repeat([]byte("x"), 2<<20)...))
	})
	mux.HandleFunc("GET /broken", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusInternalServerError) })
	mux.HandleFunc("GET /not-metadata", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html></html>") })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.EntityID, f.SSOURL, f.MetadataURL = f.srv.URL+"/metadata", f.srv.URL+"/sso", f.srv.URL+"/metadata"
	return f
}

// MetadataFetches is how many times the metadata endpoint was requested.
func (f *ForgingSAMLIDP) MetadataFetches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches
}

// ServeMetadata changes the mistakes the metadata endpoint makes in what it
// serves from now on, such as another entity ID.
func (f *ForgingSAMLIDP) ServeMetadata(o SAMLMetadataOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served = o
}

// FailMetadata makes the metadata endpoint answer status, and the metadata
// again for 0.
func (f *ForgingSAMLIDP) FailMetadata(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

// DelayMetadata makes the metadata endpoint take d to answer, so two requests
// for it overlap.
func (f *ForgingSAMLIDP) DelayMetadata(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// RotateSigningKey makes the provider sign with a new key and publish its
// certificate in its metadata, as an identity provider does when its
// certificate is about to expire. With keepOld the certificate it replaced is
// published beside the new one; without, it is not.
func (f *ForgingSAMLIDP) RotateSigningKey(t testing.TB, keepOld bool) {
	t.Helper()
	key, cert := newSigningKey(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	published := [][]byte{cert}
	if keepOld {
		published = append(published, f.cert)
	}
	f.key, f.cert, f.published = key, cert, published
	f.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})
}

// Close stops the provider answering, as an identity provider that went away.
func (f *ForgingSAMLIDP) Close() { f.srv.Close() }

// URL returns the address of path on the provider's own server.
func (f *ForgingSAMLIDP) URL(path string) string { return f.srv.URL + path }

func newSigningKey(t testing.TB) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	// Valid from long before to long after any time the tests run at, so a
	// moved test clock never makes the certificate the fault.
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "forging identity provider"},
		NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2060, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

// Metadata returns the provider's metadata document.
func (f *ForgingSAMLIDP) Metadata(o SAMLMetadataOptions) string {
	f.mu.Lock()
	published := f.published
	f.mu.Unlock()
	entity := f.EntityID
	if o.OtherEntityID != "" {
		entity = o.OtherEntityID
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID=%q>`, entity)
	b.WriteString(`<md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">`)
	if !o.NoCertificate {
		for _, cert := range published {
			fmt.Fprintf(&b, `<md:KeyDescriptor use="signing"><ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:X509Data>`+
				`<ds:X509Certificate>%s</ds:X509Certificate></ds:X509Data></ds:KeyInfo></md:KeyDescriptor>`, base64.StdEncoding.EncodeToString(cert))
		}
	}
	if !o.NoRedirectBinding {
		fmt.Fprintf(&b, `<md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location=%q/>`, f.SSOURL)
	}
	fmt.Fprintf(&b, `<md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location=%q/>`, f.SSOURL)
	b.WriteString(`</md:IDPSSODescriptor></md:EntityDescriptor>`)
	return b.String()
}

// ParseRequest reads the AuthnRequest in the address a browser was sent to
// the provider's single sign-on endpoint at, and checks the signature of its
// query against the certificates in spMetadata, the service provider's
// metadata: Signed says whether it carried one that verified.
func (f *ForgingSAMLIDP) ParseRequest(t testing.TB, redirect *url.URL, spMetadata []byte) SAMLRequest {
	t.Helper()
	q := redirect.Query()
	deflated, err := base64.StdEncoding.DecodeString(q.Get("SAMLRequest"))
	if err != nil {
		t.Fatalf("the SAMLRequest is not base64: %v", err)
	}
	doc, err := io.ReadAll(flate.NewReader(bytes.NewReader(deflated)))
	if err != nil {
		t.Fatalf("the SAMLRequest is not deflated: %v", err)
	}
	root := etree.NewDocument()
	if err = root.ReadFromBytes(doc); err != nil {
		t.Fatalf("the SAMLRequest is not XML: %v", err)
	}
	el := root.Root()
	req := SAMLRequest{
		ID: el.SelectAttrValue("ID", ""), Destination: el.SelectAttrValue("Destination", ""),
		ACS: el.SelectAttrValue("AssertionConsumerServiceURL", ""), RelayState: q.Get("RelayState"), Query: redirect.RawQuery,
	}
	if i := el.FindElement("./Issuer"); i != nil {
		req.Issuer = i.Text()
	}
	if p := el.FindElement("./NameIDPolicy"); p != nil {
		req.NameIDFormat = p.SelectAttrValue("Format", "")
	}
	if sig := q.Get("Signature"); sig != "" {
		req.Signed = verifyRedirectSignature(q, sig, spMetadata)
	}
	return req
}

// verifyRedirectSignature checks the signature of the redirect binding's
// query, which covers the SAMLRequest, the RelayState and the SigAlg as the
// browser carried them, against the signing certificates of spMetadata.
func verifyRedirectSignature(q url.Values, sig string, spMetadata []byte) bool {
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || q.Get("SigAlg") != dsig.RSASHA256SignatureMethod {
		return false
	}
	signed := "SAMLRequest=" + url.QueryEscape(q.Get("SAMLRequest"))
	if rs := q.Get("RelayState"); rs != "" {
		signed += "&RelayState=" + url.QueryEscape(rs)
	}
	signed += "&SigAlg=" + url.QueryEscape(q.Get("SigAlg"))
	sum := sha256.Sum256([]byte(signed))
	for _, c := range SPCertificates(spMetadata) {
		if pub, ok := c.PublicKey.(*rsa.PublicKey); ok && rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], raw) == nil {
			return true
		}
	}
	return false
}

// SPCertificates returns the certificates a service provider's metadata
// publishes.
func SPCertificates(metadata []byte) []*x509.Certificate {
	var doc struct {
		Certificates []string `xml:"SPSSODescriptor>KeyDescriptor>KeyInfo>X509Data>X509Certificate"`
	}
	if err := xml.Unmarshal(metadata, &doc); err != nil {
		return nil
	}
	var out []*x509.Certificate
	for _, c := range doc.Certificates {
		der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(c), ""))
		if err != nil {
			continue
		}
		if cert, err := x509.ParseCertificate(der); err == nil {
			out = append(out, cert)
		}
	}
	return out
}

// Respond builds what the identity provider posts to the ACS in answer to
// req: the form's SAMLResponse and RelayState.
func (f *ForgingSAMLIDP) Respond(t testing.TB, req SAMLRequest, a SAMLAssertion) url.Values {
	t.Helper()
	now := f.Now().UTC()
	if a.Subject == "" {
		a.Subject = "subject-1"
	}
	if a.NameIDFormat == "" {
		a.NameIDFormat = "urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified"
	}
	if a.Attributes == nil {
		a.Attributes = map[string][]string{"email": {"ada@example.com"}, "name": {"Ada Lovelace"}}
	}
	if a.ID == "" {
		a.ID = "_" + randomHex(16)
	}
	issuer, audience, destination, inResponseTo := f.EntityID, req.Issuer, req.ACS, req.ID
	notBefore, notAfter := now.Add(-time.Minute), now.Add(5*time.Minute)
	switch a.Fault {
	case SAMLWrongAudience:
		audience = "https://other.example.com/sp"
	case SAMLWrongDestination:
		destination = "https://other.example.com/acs"
	case SAMLWrongInResponseTo:
		inResponseTo = "_not-the-request"
	case SAMLNoInResponseTo:
		inResponseTo = ""
	case SAMLExpired:
		notBefore, notAfter = now.Add(-20*time.Minute), now.Add(-10*time.Minute)
	case SAMLNotYetValid:
		notBefore, notAfter = now.Add(10*time.Minute), now.Add(20*time.Minute)
	case SAMLWrongIssuer:
		issuer = "https://other.example.com/idp"
	}
	if a.Until != 0 {
		notBefore, notAfter = now.Add(a.Until-20*time.Minute), now.Add(a.Until)
	}
	if a.Fault == SAMLFailedStatus {
		resp := f.response(now, destination, inResponseTo, issuer)
		code := resp.FindElement("./Status/StatusCode")
		code.RemoveAttr("Value")
		code.CreateAttr("Value", "urn:oasis:names:tc:SAML:2.0:status:AuthnFailed")
		return f.form(t, resp, req.RelayState, false)
	}
	assertion := f.assertion(now, a, issuer, audience, destination, inResponseTo, notBefore, notAfter, a.Fault == SAMLNoAudience)
	resp := f.response(now, destination, inResponseTo, issuer)

	switch {
	case a.Fault == SAMLUnsigned:
		resp.AddChild(assertion)
	case a.Fault == SAMLSignedByOtherKey:
		resp.AddChild(f.signWith(t, assertion, f.otherKey, f.otherCert))
	case a.Fault == SAMLWrapped:
		f.wrap(t, resp, a, now, issuer, audience, destination, inResponseTo, notBefore, notAfter)
	case a.SignResponse:
		resp.AddChild(assertion)
		resp = f.signWith(t, resp, f.key, f.cert)
	default:
		signed := f.signWith(t, assertion, f.key, f.cert)
		if a.Encryption != SAMLPlain {
			resp.AddChild(encryptAssertion(t, signed, a))
		} else {
			resp.AddChild(signed)
		}
	}
	return f.form(t, resp, req.RelayState, a.Fault == SAMLDocType)
}

// wrap builds a signature wrapping attack: the response holds an unsigned
// assertion about the victim, where a processor reading the first assertion
// finds it, and the assertion the identity provider signed, about the
// attacker, beside it where the signature still finds it by its ID.
func (f *ForgingSAMLIDP) wrap(t testing.TB, resp *etree.Element, genuine SAMLAssertion, now time.Time,
	issuer, audience, destination, inResponseTo string, notBefore, notAfter time.Time,
) {
	t.Helper()
	signed := f.signWith(t, f.assertion(now, genuine, issuer, audience, destination, inResponseTo, notBefore, notAfter, false), f.key, f.cert)
	evil := genuine
	evil.Subject, evil.Attributes, evil.ID = "victim", map[string][]string{"email": {"victim@example.com"}}, "_evil"
	resp.AddChild(f.assertion(now, evil, issuer, audience, destination, inResponseTo, notBefore, notAfter, false))
	ext := resp.CreateElement("samlp:Extensions")
	ext.AddChild(signed)
}

func (f *ForgingSAMLIDP) response(now time.Time, destination, inResponseTo, issuer string) *etree.Element {
	r := etree.NewElement("samlp:Response")
	r.CreateAttr("xmlns:samlp", "urn:oasis:names:tc:SAML:2.0:protocol")
	r.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	r.CreateAttr("ID", "_"+randomHex(16))
	r.CreateAttr("Version", "2.0")
	r.CreateAttr("IssueInstant", now.Format(time.RFC3339))
	r.CreateAttr("Destination", destination)
	if inResponseTo != "" {
		r.CreateAttr("InResponseTo", inResponseTo)
	}
	r.CreateElement("saml:Issuer").SetText(issuer)
	r.CreateElement("samlp:Status").CreateElement("samlp:StatusCode").CreateAttr("Value", "urn:oasis:names:tc:SAML:2.0:status:Success")
	return r
}

func (f *ForgingSAMLIDP) assertion(now time.Time, a SAMLAssertion, issuer, audience, recipient, inResponseTo string,
	notBefore, notAfter time.Time, noAudience bool,
) *etree.Element {
	e := etree.NewElement("saml:Assertion")
	e.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	e.CreateAttr("ID", a.ID)
	e.CreateAttr("Version", "2.0")
	e.CreateAttr("IssueInstant", now.Format(time.RFC3339))
	e.CreateElement("saml:Issuer").SetText(issuer)
	subject := e.CreateElement("saml:Subject")
	nameID := subject.CreateElement("saml:NameID")
	nameID.CreateAttr("Format", a.NameIDFormat)
	nameID.SetText(a.Subject)
	confirmation := subject.CreateElement("saml:SubjectConfirmation")
	confirmation.CreateAttr("Method", "urn:oasis:names:tc:SAML:2.0:cm:bearer")
	data := confirmation.CreateElement("saml:SubjectConfirmationData")
	data.CreateAttr("NotOnOrAfter", notAfter.Format(time.RFC3339))
	data.CreateAttr("Recipient", recipient)
	if inResponseTo != "" {
		data.CreateAttr("InResponseTo", inResponseTo)
	}
	conditions := e.CreateElement("saml:Conditions")
	conditions.CreateAttr("NotBefore", notBefore.Format(time.RFC3339))
	conditions.CreateAttr("NotOnOrAfter", notAfter.Format(time.RFC3339))
	if !noAudience {
		conditions.CreateElement("saml:AudienceRestriction").CreateElement("saml:Audience").SetText(audience)
	}
	statement := e.CreateElement("saml:AttributeStatement")
	for name, values := range a.Attributes {
		attr := statement.CreateElement("saml:Attribute")
		attr.CreateAttr("Name", name)
		for _, v := range values {
			attr.CreateElement("saml:AttributeValue").SetText(v)
		}
	}
	authn := e.CreateElement("saml:AuthnStatement")
	authn.CreateAttr("AuthnInstant", now.Format(time.RFC3339))
	authn.CreateElement("saml:AuthnContext").CreateElement("saml:AuthnContextClassRef").
		SetText("urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport")
	return e
}

// signWith returns el signed with key, whose certificate is der, enveloped.
func (f *ForgingSAMLIDP) signWith(t testing.TB, el *etree.Element, key *rsa.PrivateKey, der []byte) *etree.Element {
	t.Helper()
	ctx := dsig.NewDefaultSigningContext(dsig.TLSCertKeyStore(tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}))
	if err := ctx.SetSignatureMethod(dsig.RSASHA256SignatureMethod); err != nil {
		t.Fatal(err)
	}
	// Exclusive canonicalization, which every identity provider signs with: the
	// default includes the namespaces in scope, which differ once the element
	// sits in a response.
	ctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	signed, err := ctx.SignEnveloped(el)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signed
}

// form serializes resp as the SAMLResponse of the form an identity provider
// posts.
func (f *ForgingSAMLIDP) form(t testing.TB, resp *etree.Element, relayState string, docType bool) url.Values {
	t.Helper()
	doc := etree.NewDocument()
	doc.SetRoot(resp)
	raw, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if docType {
		raw = append([]byte(`<!DOCTYPE foo [<!ENTITY xxe "x">]>`), raw...)
	}
	return url.Values{"SAMLResponse": {base64.StdEncoding.EncodeToString(raw)}, "RelayState": {relayState}}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// encryptAssertion returns the EncryptedAssertion that holds assertion
// encrypted to a.EncryptTo.
func encryptAssertion(t testing.TB, assertion *etree.Element, a SAMLAssertion) *etree.Element {
	t.Helper()
	block, _ := pem.Decode(a.EncryptTo)
	if block == nil {
		t.Fatal("encrypting an assertion needs the certificate to encrypt to")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub := cert.PublicKey.(*rsa.PublicKey)
	doc := etree.NewDocument()
	doc.SetRoot(assertion.Copy())
	plain, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	keySize, dataMethod := 16, "http://www.w3.org/2001/04/xmlenc#aes128-cbc"
	switch a.Encryption {
	case SAMLAES256GCM:
		keySize, dataMethod = 32, "http://www.w3.org/2009/xmlenc11#aes256-gcm"
	case SAMLTripleDES:
		// Only the algorithm it names is Triple DES: the instance refuses it
		// before decrypting, so the data is AES like the rest.
		dataMethod = "http://www.w3.org/2001/04/xmlenc#tripledes-cbc"
	}
	key := make([]byte, keySize)
	_, _ = rand.Read(key)
	c, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	var cipherText []byte
	if a.Encryption == SAMLAES256GCM {
		g, gcmErr := cipher.NewGCM(c)
		if gcmErr != nil {
			t.Fatal(gcmErr)
		}
		nonce := make([]byte, g.NonceSize())
		_, _ = rand.Read(nonce)
		cipherText = g.Seal(nonce, nonce, plain, nil)
	} else {
		size := c.BlockSize()
		pad := size - len(plain)%size
		padded := append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...) //nolint:gosec // pad is at most the block size, 16
		iv := make([]byte, size)
		_, _ = rand.Read(iv)
		cipherText = append(iv, make([]byte, len(padded))...)
		cipher.NewCBCEncrypter(c, iv).CryptBlocks(cipherText[size:], padded)
	}
	var wrappedKey []byte
	transport := "http://www.w3.org/2001/04/xmlenc#rsa-oaep-mgf1p"
	if a.Encryption == SAMLRSA15 {
		transport = "http://www.w3.org/2001/04/xmlenc#rsa-1_5"
		wrappedKey, err = rsa.EncryptPKCS1v15(rand.Reader, pub, key) //nolint:staticcheck // the key transport the instance refuses has to be built to be refused
	} else {
		wrappedKey, err = rsa.EncryptOAEP(sha1.New(), rand.Reader, pub, key, nil) //nolint:gosec // the digest SAML service providers default to
	}
	if err != nil {
		t.Fatal(err)
	}

	enc := etree.NewElement("saml:EncryptedAssertion")
	enc.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	data := enc.CreateElement("xenc:EncryptedData")
	data.CreateAttr("xmlns:xenc", "http://www.w3.org/2001/04/xmlenc#")
	data.CreateAttr("xmlns:ds", "http://www.w3.org/2000/09/xmldsig#")
	data.CreateAttr("Type", "http://www.w3.org/2001/04/xmlenc#Element")
	data.CreateElement("xenc:EncryptionMethod").CreateAttr("Algorithm", dataMethod)
	ek := data.CreateElement("ds:KeyInfo").CreateElement("xenc:EncryptedKey")
	ek.CreateElement("xenc:EncryptionMethod").CreateAttr("Algorithm", transport)
	ek.CreateElement("ds:KeyInfo").CreateElement("ds:X509Data").CreateElement("ds:X509Certificate").
		SetText(base64.StdEncoding.EncodeToString(block.Bytes))
	ek.CreateElement("xenc:CipherData").CreateElement("xenc:CipherValue").SetText(base64.StdEncoding.EncodeToString(wrappedKey))
	data.CreateElement("xenc:CipherData").CreateElement("xenc:CipherValue").SetText(base64.StdEncoding.EncodeToString(cipherText))
	return enc
}
