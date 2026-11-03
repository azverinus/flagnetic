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

	"github.com/russellhaering/gosaml2/types"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
)

const (
	statusSuccess = "urn:oasis:names:tc:SAML:2.0:status:Success"
	// methodRSA15 is the key transport that makes a decryption oracle of the
	// instance's key; no identity provider needs it, since RSA-OAEP exists.
	methodRSA15 = types.MethodRSAv1_5
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
// uses RSA 1.5 key transport.
func (c *Client) VerifyResponse(cfg appidentity.SAMLConfig, response, requestID string) (appidentity.SAMLAssertion, error) {
	var none appidentity.SAMLAssertion
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(response))
	if err != nil || len(raw) == 0 {
		return none, errors.New("the response is not base64")
	}
	if err = inspect(raw); err != nil {
		return none, err
	}
	sp, err := serviceProvider(cfg)
	if err != nil {
		return none, err
	}
	resp, err := sp.ValidateEncodedResponse(base64.StdEncoding.EncodeToString(raw))
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

// inspect refuses a response before it is verified when it is not to be
// trusted whatever its signature: a DOCTYPE or a processing instruction, which
// can declare or expand entities; RSA 1.5 key transport in an encryption
// method. It answers ErrIdPRefused for a response that carries no assertion
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
				if a.Value == methodRSA15 {
					return errors.New("the response uses RSA 1.5 key transport, which is refused")
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
		return appidentity.ErrIdPRefused
	}
	return nil
}
