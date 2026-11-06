package identity_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

func TestMetadataFetchedFromAURLIsDueAfterADay(t *testing.T) {
	p := samlProvider(t)
	fetchedAt := p.SAML.RefreshedAt

	for name, tc := range map[string]struct {
		now  time.Time
		want bool
	}{
		"a minute short of a day": {fetchedAt.Add(24*time.Hour - time.Minute), false},
		"exactly a day":           {fetchedAt.Add(24 * time.Hour), true},
		"a week":                  {fetchedAt.Add(7 * 24 * time.Hour), true},
	} {
		if got := p.MetadataDue(tc.now); got != tc.want {
			t.Errorf("%s: due = %v, want %v", name, got, tc.want)
		}
	}
}

func TestMetadataThatWasPastedOrBelongsToADisabledOrOIDCProviderIsNeverDue(t *testing.T) {
	later := samlProvider(t).SAML.RefreshedAt.Add(30 * 24 * time.Hour)

	pasted := samlProvider(t)
	pasted.SAML.MetadataURL, pasted.SAML.MetadataXML = "", "<EntityDescriptor/>"
	disabled, _ := samlProvider(t).Disable(later)
	in, err := identity.CheckProviderInput(validInput())
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]identity.Provider{
		"pasted metadata": pasted, "a disabled provider": disabled, "an OIDC provider": identity.New(identity.ProviderID{}, in, "sealed", later),
	} {
		if p.MetadataDue(later) {
			t.Errorf("%s is due for a refresh", name)
		}
	}
}

func TestRefreshingWithTheSameMetadataOnlyMovesTheTimeOfTheRefresh(t *testing.T) {
	p := samlProvider(t)
	later := p.SAML.RefreshedAt.Add(25 * time.Hour)

	got, outcome := p.RefreshIDPMetadata(p.SAML.IDP, later)

	if outcome != identity.RefreshUnchanged {
		t.Fatalf("outcome = %v, want unchanged", outcome)
	}
	if !got.SAML.RefreshedAt.Equal(later) || got.Version != p.Version || !got.UpdatedAt.Equal(p.UpdatedAt) {
		t.Errorf("refreshed at %s, version %d, updated at %s; want %s with the version and update time as they were",
			got.SAML.RefreshedAt, got.Version, got.UpdatedAt, later)
	}
}

func TestTheOrderOfTheCertificatesIsNotAChange(t *testing.T) {
	p := samlProvider(t)
	second := certificate(t, rotationNow.AddDate(2, 0, 0))
	p.SAML.IDP.Certificates = append(p.SAML.IDP.Certificates, second)
	reordered := p.SAML.IDP
	reordered.Certificates = []string{p.SAML.IDP.Certificates[1], p.SAML.IDP.Certificates[0]}

	_, outcome := p.RefreshIDPMetadata(reordered, p.SAML.RefreshedAt.Add(25*time.Hour))

	if outcome != identity.RefreshUnchanged {
		t.Errorf("outcome = %v, want unchanged", outcome)
	}
}

func TestNewCertificatesOrANewSSOURLAreAppliedAsAVersionOfTheProvider(t *testing.T) {
	later := samlProvider(t).SAML.RefreshedAt.Add(25 * time.Hour)
	for name, change := range map[string]func(*identity.IDPMetadata){
		"a rotated certificate": func(m *identity.IDPMetadata) { m.Certificates = []string{certificate(t, rotationNow.AddDate(2, 0, 0))} },
		"an added certificate": func(m *identity.IDPMetadata) {
			m.Certificates = append(m.Certificates, certificate(t, rotationNow.AddDate(2, 0, 0)))
		},
		"a new single sign-on URL": func(m *identity.IDPMetadata) { m.SSOURL = "https://idp.example.com/sso/v2" },
	} {
		t.Run(name, func(t *testing.T) {
			p := samlProvider(t)
			fetched := p.SAML.IDP
			fetched.Certificates = append([]string(nil), fetched.Certificates...)
			change(&fetched)

			got, outcome := p.RefreshIDPMetadata(fetched, later)

			if outcome != identity.RefreshChanged {
				t.Fatalf("outcome = %v, want changed", outcome)
			}
			if got.SAML.IDP.SSOURL != fetched.SSOURL || len(got.SAML.IDP.Certificates) != len(fetched.Certificates) ||
				!got.SAML.RefreshedAt.Equal(later) || got.Version != p.Version+1 || !got.UpdatedAt.Equal(later) {
				t.Errorf("provider = %+v at version %d, want the fetched metadata read at %s as version %d", got.SAML, got.Version, later, p.Version+1)
			}
		})
	}
}

func TestAChangedEntityIDIsRefusedAndNothingIsApplied(t *testing.T) {
	p := samlProvider(t)
	fetched := p.SAML.IDP
	fetched.EntityID = "https://other-idp.example.com/entity"
	fetched.Certificates = []string{certificate(t, rotationNow.AddDate(2, 0, 0))}

	got, outcome := p.RefreshIDPMetadata(fetched, p.SAML.RefreshedAt.Add(25*time.Hour))

	if outcome != identity.RefreshRefused {
		t.Fatalf("outcome = %v, want refused", outcome)
	}
	if got.SAML.IDP.EntityID != p.SAML.IDP.EntityID || got.SAML.IDP.Certificates[0] != p.SAML.IDP.Certificates[0] ||
		!got.SAML.RefreshedAt.Equal(p.SAML.RefreshedAt) || got.Version != p.Version {
		t.Errorf("a refused refresh changed the provider: %+v", got.SAML)
	}
}
