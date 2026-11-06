package saml

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"
	dsig "github.com/russellhaering/goxmldsig"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
)

const (
	statusSuccess = "urn:oasis:names:tc:SAML:2.0:status:Success"
	// methodRSA15 is the key transport that makes a decryption oracle of the
	// instance's key; no identity provider needs it, since RSA-OAEP exists.
	methodRSA15 = types.MethodRSAv1_5
	// methodTripleDES is a data encryption gosaml2 decrypts but the instance
	// does not accept: its 64-bit blocks are open to birthday attacks, and an
	// identity provider that encrypts can use AES-CBC or AES-GCM.
	methodTripleDES = types.MethodTripleDESCBC
	// nameIDEmail is the format of a NameID that is an email address.
	nameIDEmail = "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress"
)

// VerifyResponse checks the base64 encoded SAML response an identity provider
// posted to the ACS and returns what its assertion asserts of a person.
//
// gosaml2 verifies the signature on the Response or on the assertion against
// the identity provider's certificates, decrypts an encrypted assertion, and
// checks the issuer, the status, the destination, the recipient and the
// validity of the subject confirmation, handing back the elements the
// signature covers. What it leaves to its caller is checked here: that the
// answer is to the request this attempt made, the audience, the conditions
// with the clock skew, that exactly one assertion speaks, and that nothing
// uses RSA 1.5 key transport or Triple DES encryption.
func (c *Client) VerifyResponse(cfg appidentity.SAMLConfig, response, requestID string) (appidentity.SAMLAssertion, error) {
	var none appidentity.SAMLAssertion
	// An empty request ID would let an assertion that answers no request, an
	// unsolicited one, pass for the answer to it.
	if requestID == "" {
		return none, errors.New("the attempt made no request for a response to answer")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(response))
	if err != nil || len(raw) == 0 {
		return none, errors.New("the response is not base64")
	}
	if err = inspect(raw); err != nil {
		return none, err
	}
	sps, err := serviceProviders(cfg)
	if err != nil {
		return none, err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	// The identity provider encrypted to the key pair the SP metadata it
	// imported listed, which during a rotation is either of two; nothing in the
	// response says which, and a response that fails for any other reason fails
	// with each key alike.
	var first error
	for _, sp := range sps {
		assertion, err := verifyWith(sp, cfg, encoded, requestID)
		if err == nil {
			return assertion, nil
		}
		if first == nil {
			first = err
		}
	}
	return none, first
}

// verifyWith checks the response encoded as sp, which decrypts with one key
// pair, and returns what its assertion asserts of a person.
func verifyWith(sp *saml2.SAMLServiceProvider, cfg appidentity.SAMLConfig, encoded, requestID string) (appidentity.SAMLAssertion, error) {
	var none appidentity.SAMLAssertion
	resp, err := sp.ValidateEncodedResponse(encoded)
	var invalid saml2.ErrInvalidValue
	if errors.As(err, &invalid) && invalid.Reason == saml2.ReasonExpired {
		// The subject confirmation ended, perhaps within the skew of the
		// identity provider's clock. The clock goes back only now: moved
		// before, it would also make a certificate that was just issued, as
		// one a rotation brings, not valid yet.
		sp.Clock = dsig.NewFakeClockAt(cfg.Now.Add(-identity.ClockSkew))
		resp, err = sp.ValidateEncodedResponse(encoded)
	}
	if err != nil {
		return none, fmt.Errorf("verifying the response: %w", err)
	}
	if len(resp.Assertions) != 1 {
		return none, fmt.Errorf("the response holds %d assertions, not one", len(resp.Assertions))
	}
	assertion := resp.Assertions[0]
	if resp.SignatureValidated && resp.Destination == "" {
		return none, errors.New("the signed response names no destination")
	}
	if resp.InResponseTo != "" && resp.InResponseTo != requestID {
		return none, errors.New("the response answers another request")
	}
	validUntil, err := checkAssertion(assertion, cfg, requestID)
	if err != nil {
		return none, err
	}
	nameID := assertion.Subject.NameID
	if nameID == nil || strings.TrimSpace(nameID.Value) == "" {
		return none, errors.New("the assertion names no subject")
	}
	claims := identity.VerifiedClaims{Subject: strings.TrimSpace(nameID.Value)}
	attributes := assertion.AttributeStatement
	if nameID.Format == nameIDEmail {
		claims.Email = strings.TrimSpace(nameID.Value)
	} else if attributes != nil {
		claims.Email = attributeValue(attributes, cfg.Claims.Email)
	}
	if attributes != nil {
		claims.Name = attributeValue(attributes, cfg.Claims.Name)
		claims.Groups, claims.HasGroups = attributeValues(attributes, cfg.Claims.Groups)
	}
	return appidentity.SAMLAssertion{ID: assertion.ID, ValidUntil: validUntil, Claims: claims}, nil
}

// checkAssertion checks what gosaml2 leaves of a verified assertion: its ID,
// that it answers the request, its audience and its conditions; it returns the
// last moment it could be accepted, the skew included.
func checkAssertion(a types.Assertion, cfg appidentity.SAMLConfig, requestID string) (time.Time, error) {
	var none time.Time
	if a.ID == "" {
		return none, errors.New("the assertion has no ID")
	}
	if a.Subject == nil || a.Subject.SubjectConfirmation == nil || a.Subject.SubjectConfirmation.SubjectConfirmationData == nil {
		return none, errors.New("the assertion has no subject confirmation")
	}
	data := a.Subject.SubjectConfirmation.SubjectConfirmationData
	if data.InResponseTo != requestID {
		return none, errors.New("the assertion answers another request")
	}
	confirmedUntil, err := time.Parse(time.RFC3339, data.NotOnOrAfter)
	if err != nil {
		return none, errors.New("the subject confirmation has no valid end")
	}
	cond := a.Conditions
	if cond == nil {
		return none, errors.New("the assertion has no conditions")
	}
	notBefore, err := time.Parse(time.RFC3339, cond.NotBefore)
	if err != nil {
		return none, errors.New("the conditions have no valid start")
	}
	notOnOrAfter, err := time.Parse(time.RFC3339, cond.NotOnOrAfter)
	if err != nil {
		return none, errors.New("the conditions have no valid end")
	}
	if cfg.Now.Add(identity.ClockSkew).Before(notBefore) {
		return none, errors.New("the assertion is not valid yet")
	}
	until := notOnOrAfter
	if confirmedUntil.Before(until) {
		until = confirmedUntil
	}
	until = until.Add(identity.ClockSkew)
	if !cfg.Now.Before(until) {
		return none, errors.New("the assertion expired")
	}
	if len(cond.AudienceRestrictions) == 0 {
		return none, errors.New("the assertion names no audience")
	}
	for _, r := range cond.AudienceRestrictions {
		if !slices.ContainsFunc(r.Audiences, func(au types.Audience) bool { return au.Value == cfg.EntityID }) {
			return none, errors.New("the assertion is for another audience")
		}
	}
	return until, nil
}

// attributeValue returns the first value of the attribute named name, by its
// name or its friendly name, trimmed; "" when there is none.
func attributeValue(s *types.AttributeStatement, name string) string {
	for _, attr := range s.Attributes {
		if attr.Name != name && attr.FriendlyName != name {
			continue
		}
		for _, v := range attr.Values {
			if value := strings.TrimSpace(v.Value); value != "" {
				return value
			}
		}
	}
	return ""
}

// attributeValues returns the values of every attribute named name, by its
// name or its friendly name, trimmed and without the blank ones; found is
// whether the assertion has such an attribute at all, which may hold no
// value, as a directory says a person is in no group.
func attributeValues(s *types.AttributeStatement, name string) (values []string, found bool) {
	values = []string{}
	for _, attr := range s.Attributes {
		if attr.Name != name && attr.FriendlyName != name {
			continue
		}
		found = true
		for _, v := range attr.Values {
			if value := strings.TrimSpace(v.Value); value != "" {
				values = append(values, value)
			}
		}
	}
	if !found {
		return nil, false
	}
	return values, true
}

// inspect refuses a response before it is verified when it is not to be
// trusted whatever its signature: a DOCTYPE or a processing instruction, which
// can declare or expand entities; RSA 1.5 key transport or Triple DES in an
// encryption method. It answers ErrIDPRefused for a response that carries no assertion
// and a status that is not success, which the identity provider uses to say
// the person did not authenticate; no signature is asked of it, since it
// grants nothing.
func inspect(raw []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var (
		depth             int
		assertionSeen     bool
		failedStatus      bool
		inStatus          bool
		insideStatusCodes int
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the response is not well formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return errors.New("the response has a DOCTYPE, which is refused")
		case xml.ProcInst:
			if t.Target != "xml" {
				return errors.New("the response has a processing instruction, which is refused")
			}
		case xml.StartElement:
			depth++
			for _, a := range t.Attr {
				switch a.Value {
				case methodRSA15:
					return errors.New("the response uses RSA 1.5 key transport, which is refused")
				case methodTripleDES:
					return errors.New("the response uses Triple DES encryption, which is refused")
				}
			}
			switch {
			case depth == 2 && (t.Name.Local == "Assertion" || t.Name.Local == "EncryptedAssertion"):
				assertionSeen = true
			case depth == 2 && t.Name.Local == "Status":
				inStatus = true
			case inStatus && t.Name.Local == "StatusCode":
				insideStatusCodes++
				if insideStatusCodes == 1 {
					for _, a := range t.Attr {
						if a.Name.Local == "Value" && a.Value != statusSuccess {
							failedStatus = true
						}
					}
				}
			}
		case xml.EndElement:
			if depth == 2 && t.Name.Local == "Status" {
				inStatus = false
			}
			depth--
		}
	}
	if failedStatus && !assertionSeen {
		return appidentity.ErrIDPRefused
	}
	return nil
}
