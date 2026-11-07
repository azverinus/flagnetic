package identity

import (
	"slices"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// MetadataRefreshAfter is how old an identity provider's metadata fetched from
// a URL is when the instance reads it again.
const MetadataRefreshAfter = 24 * time.Hour

var (
	// ErrNotSAML refuses rotating the certificate of a provider that is not a
	// SAML one: it has no key pair to rotate.
	ErrNotSAML = apperrors.Invalid("not_saml", "only a SAML provider has a certificate to rotate")
	// ErrRotationPending refuses starting a rotation while one is pending: the
	// identity provider may be importing the next certificate already.
	ErrRotationPending = apperrors.Conflict("rotation_pending",
		"a certificate rotation is pending; complete it before starting another")
	// ErrNoRotationPending refuses completing a rotation that was not started.
	ErrNoRotationPending = apperrors.Conflict("no_rotation_pending", "no certificate rotation is pending")
)

// The status of an SP certificate: the one AuthnRequests are signed with, or
// the one that takes its place when the rotation completes.
const (
	SPCertificateCurrent = "current"
	SPCertificateNext    = "next"
)

// SPCertificate describes one of the certificates the instance presents to a
// SAML identity provider.
type SPCertificate struct {
	CertificateInfo
	// Status is SPCertificateCurrent or SPCertificateNext.
	Status string
}

// RotationPending reports whether a next key pair was generated and the
// rotation not yet completed.
func (s SAML) RotationPending() bool { return s.NextSPCertificate != "" }

// SPCertificates describes the certificates the instance presents: the current
// one, then the next while a rotation is pending.
func (s SAML) SPCertificates() []SPCertificate {
	var out []SPCertificate
	for _, c := range []struct{ pem, status string }{
		{s.SPCertificate, SPCertificateCurrent}, {s.NextSPCertificate, SPCertificateNext},
	} {
		if c.pem == "" {
			continue
		}
		for _, info := range (IDPMetadata{Certificates: []string{c.pem}}).CertificateInfos() {
			out = append(out, SPCertificate{CertificateInfo: info, Status: c.status})
		}
	}
	return out
}

// StartRotation returns p with next as the key pair that takes the current one's
// place when the rotation completes. Until then the SP metadata lists both
// certificates and an assertion encrypted to either is accepted, while
// requests stay signed with the current key, so the identity provider can
// import the new certificate without a sign-in failing.
func (p Provider) StartRotation(next SPKey, now time.Time) (Provider, error) {
	if p.SAML == nil {
		return p, ErrNotSAML
	}
	if p.SAML.RotationPending() {
		return p, ErrRotationPending
	}
	p = p.clone()
	p.SAML.NextSPCertificate, p.SAML.NextSealedSPKey = next.CertificatePEM, next.SealedKey
	p.UpdatedAt, p.Version = now, p.Version+1
	return p, nil
}

// CompleteRotation returns p with the next key pair current and the retired one
// gone, so the key it held can decrypt nothing and sign nothing any more.
func (p Provider) CompleteRotation(now time.Time) (Provider, error) {
	if p.SAML == nil {
		return p, ErrNotSAML
	}
	if !p.SAML.RotationPending() {
		return p, ErrNoRotationPending
	}
	p = p.clone()
	p.SAML.SPCertificate, p.SAML.SealedSPKey = p.SAML.NextSPCertificate, p.SAML.NextSealedSPKey
	p.SAML.NextSPCertificate, p.SAML.NextSealedSPKey = "", ""
	p.UpdatedAt, p.Version = now, p.Version+1
	return p, nil
}

// RefreshOutcome is what reading an identity provider's metadata again found.
type RefreshOutcome int

// The outcomes of a metadata refresh.
const (
	// RefreshUnchanged: the metadata says what the provider already holds.
	RefreshUnchanged RefreshOutcome = iota
	// RefreshChanged: the certificates or the single sign-on URL differ and
	// were applied.
	RefreshChanged
	// RefreshRefused: the metadata names another entity than the provider's,
	// which is a different identity provider and never applied silently.
	RefreshRefused
)

// MetadataDue reports whether p's metadata should be read again at now: p is an
// enabled SAML provider whose metadata comes from a URL and was read a day ago
// or more.
func (p Provider) MetadataDue(now time.Time) bool {
	s := p.SAML
	return p.Enabled && s != nil && s.MetadataURL != "" && !now.Before(s.RefreshedAt.Add(MetadataRefreshAfter))
}

// RefreshIDPMetadata returns p with what fetched says, read at now. The
// provider's certificates and single sign-on URL are replaced and its version
// raised when they differ; when they do not, only the time of the refresh
// moves. A different entity ID is RefreshRefused and p is returned as it was.
func (p Provider) RefreshIDPMetadata(fetched IDPMetadata, now time.Time) (Provider, RefreshOutcome) {
	current := p.SAML.IDP
	if fetched.EntityID != current.EntityID {
		return p, RefreshRefused
	}
	p = p.clone()
	p.SAML.RefreshedAt = now
	if fetched.SSOURL == current.SSOURL && sameCertificates(fetched.Certificates, current.Certificates) {
		return p, RefreshUnchanged
	}
	p.SAML.IDP = fetched
	p.SAML.IDP.Certificates = slices.Clone(fetched.Certificates)
	p.UpdatedAt, p.Version = now, p.Version+1
	return p, RefreshChanged
}

// sameCertificates reports whether a and b hold the same certificates, in any
// order.
func sameCertificates(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
