package sso_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// refreshEnv is a replica on a clock the test moves, and an identity provider
// whose assertions are dated by it.
func refreshEnv(t *testing.T) (env, *clock.Fake, *testkit.ForgingSAMLIDP) {
	t.Helper()
	c := testkit.Clock()
	idp := testkit.StartForgingSAMLIDP(t)
	idp.Now = c.Now
	return start(t, testkit.WithClock(c)), c, idp
}

func (e env) refreshMetadata(t *testing.T) {
	t.Helper()
	if err := e.srv.Tick(t, worker.SAMLMetadataRefreshName); err != nil {
		t.Fatalf("a round of the metadata refresh: %v", err)
	}
}

func (e env) getProvider(t *testing.T, slug string) management.AuthProvider {
	t.Helper()
	res, err := e.admin.GetAuthProviderWithResponse(t.Context(), slug)
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET provider = %d, %v: %s", res.StatusCode(), err, res.Body)
	}
	return *res.JSON200
}

func certificateHashes(p management.AuthProvider) []string {
	var out []string
	for _, c := range *p.IdpCertificates {
		out = append(out, c.Sha256)
	}
	return out
}

// loggedAbout returns the records whose message holds text and that name the
// provider.
func (e env) loggedAbout(text, slug string) []testkit.LogRecord {
	var out []testkit.LogRecord
	for _, r := range e.srv.Logs.Records() {
		if strings.Contains(r.Message, text) && r.Attrs["provider"] == slug {
			out = append(out, r)
		}
	}
	return out
}

func (e env) signsInThrough(t *testing.T, idp *testkit.ForgingSAMLIDP, slug string) bool {
	t.Helper()
	b, _ := e.samlSignIn(t, idp, slug, "", samlPerson(uniqueEmail()))
	return signedIn(t, b) != nil
}

func TestACertificateTheIdentityProviderRotatedIsAppliedAfterADayAndAudited(t *testing.T) {
	e, c, idp := refreshEnv(t)
	p := e.createSAML(t, idp, nil)
	idp.RotateSigningKey(t, false)
	if e.signsInThrough(t, idp, p.Slug) {
		t.Fatal("the instance accepted a signature by a certificate it has not read yet")
	}
	c.Advance(25 * time.Hour)

	e.refreshMetadata(t)

	got := e.getProvider(t, p.Slug)
	if want := certificateHashes(p); len(*got.IdpCertificates) != 1 || certificateHashes(got)[0] == want[0] {
		t.Errorf("idp_certificates = %v, want the rotated one instead of %v", certificateHashes(got), want)
	}
	if got.IdpMetadataRefreshedAt == nil || !got.IdpMetadataRefreshedAt.Equal(c.Now()) || got.Version != p.Version+1 {
		t.Errorf("refreshed at %v, version %d; want %s and %d", got.IdpMetadataRefreshedAt, got.Version, c.Now(), p.Version+1)
	}
	if !e.signsInThrough(t, idp, p.Slug) {
		t.Error("a person signed by the rotated certificate could not sign in after the refresh")
	}
	entries := e.auditJSON(t, "auth_provider.metadata_refreshed", p.Id.String())
	if len(entries) != 1 {
		t.Fatalf("%d metadata_refreshed entries, want one", len(entries))
	}
	for _, want := range []string{`"kind":"system"`, certificateHashes(p)[0], certificateHashes(got)[0]} {
		if !strings.Contains(entries[0], want) {
			t.Errorf("the audit entry lacks %s: %s", want, entries[0])
		}
	}
}

func TestACertificateAddedBesideTheOldOneIsApplied(t *testing.T) {
	e, c, idp := refreshEnv(t)
	p := e.createSAML(t, idp, nil)
	idp.RotateSigningKey(t, true)
	c.Advance(24 * time.Hour)

	e.refreshMetadata(t)

	if got := e.getProvider(t, p.Slug); len(*got.IdpCertificates) != 2 {
		t.Errorf("idp_certificates = %v, want the old and the new", certificateHashes(got))
	}
}

func TestMetadataIsNotReadAgainBeforeADayIsOut(t *testing.T) {
	e, c, idp := refreshEnv(t)
	p := e.createSAML(t, idp, nil)
	fetched := idp.MetadataFetches()
	c.Advance(24*time.Hour - time.Minute)

	e.refreshMetadata(t)

	if n := idp.MetadataFetches(); n != fetched {
		t.Errorf("the metadata was fetched %d more times before it was a day old", n-fetched)
	}
	if got := e.getProvider(t, p.Slug); got.Version != p.Version || !got.IdpMetadataRefreshedAt.Equal(*p.IdpMetadataRefreshedAt) {
		t.Errorf("version %d refreshed at %v, want them as they were", got.Version, got.IdpMetadataRefreshedAt)
	}
}

func TestMetadataThatSaysTheSameOnlyMovesTheTimeItWasRead(t *testing.T) {
	e, c, idp := refreshEnv(t)
	p := e.createSAML(t, idp, nil)
	fetched := idp.MetadataFetches()
	c.Advance(25 * time.Hour)

	e.refreshMetadata(t)

	if n := idp.MetadataFetches(); n != fetched+1 {
		t.Errorf("the metadata was fetched %d times, want once", n-fetched)
	}
	got := e.getProvider(t, p.Slug)
	if got.Version != p.Version || got.IdpMetadataRefreshedAt == nil || !got.IdpMetadataRefreshedAt.Equal(c.Now()) {
		t.Errorf("version %d refreshed at %v, want version %d and %s", got.Version, got.IdpMetadataRefreshedAt, p.Version, c.Now())
	}
	if n := len(e.auditJSON(t, "auth_provider.metadata_refreshed", p.Id.String())); n != 0 {
		t.Errorf("%d audit entries for a refresh that changed nothing", n)
	}
	// It is not due again until another day is out.
	e.refreshMetadata(t)
	if n := idp.MetadataFetches(); n != fetched+1 {
		t.Errorf("the metadata was fetched again within the day the refresh renewed")
	}
}

func TestAChangedEntityIDIsRefusedLoggedAndAuditedEveryRound(t *testing.T) {
	e, c, idp := refreshEnv(t)
	p := e.createSAML(t, idp, nil)
	idp.ServeMetadata(testkit.SAMLMetadataOptions{OtherEntityID: "https://other-idp.example.com/entity"})
	idp.RotateSigningKey(t, false)
	c.Advance(25 * time.Hour)

	e.refreshMetadata(t)
	e.refreshMetadata(t)

	got := e.getProvider(t, p.Slug)
	if *got.IdpEntityId != *p.IdpEntityId || certificateHashes(got)[0] != certificateHashes(p)[0] || got.Version != p.Version ||
		!got.IdpMetadataRefreshedAt.Equal(*p.IdpMetadataRefreshedAt) {
		t.Errorf("a refused refresh changed the provider: entity %s, certificates %v, version %d, refreshed at %v",
			*got.IdpEntityId, certificateHashes(got), got.Version, got.IdpMetadataRefreshedAt)
	}
	entries := e.auditJSON(t, "auth_provider.metadata_refresh_refused", p.Id.String())
	if len(entries) != 2 {
		t.Fatalf("%d metadata_refresh_refused entries after two rounds, want 2", len(entries))
	}
	for _, want := range []string{`"kind":"system"`, *p.IdpEntityId, "https://other-idp.example.com/entity"} {
		if !strings.Contains(entries[0], want) {
			t.Errorf("the audit entry lacks %s: %s", want, entries[0])
		}
	}
	if n := len(e.loggedAbout("names another entity", p.Slug)); n != 2 {
		t.Errorf("%d log records about the refusal, want 2", n)
	}
	if n := len(e.auditJSON(t, "auth_provider.metadata_refreshed", p.Id.String())); n != 0 {
		t.Errorf("%d metadata_refreshed entries for a refused refresh", n)
	}
}

func TestAFetchThatFailsIsLoggedAndTriedAgainTheNextRound(t *testing.T) {
	e, c, idp := refreshEnv(t)
	p := e.createSAML(t, idp, nil)
	idp.RotateSigningKey(t, false)
	idp.FailMetadata(503)
	c.Advance(25 * time.Hour)

	e.refreshMetadata(t)

	got := e.getProvider(t, p.Slug)
	if certificateHashes(got)[0] != certificateHashes(p)[0] || got.Version != p.Version || !got.IdpMetadataRefreshedAt.Equal(*p.IdpMetadataRefreshedAt) {
		t.Errorf("a failed fetch changed the provider: %v at version %d", certificateHashes(got), got.Version)
	}
	logged := e.loggedAbout("could not be refreshed", p.Slug)
	if len(logged) != 1 || !strings.Contains(logged[0].Attrs["error"].(string), "503") {
		t.Errorf("the failure was logged as %v, want one record naming the 503", logged)
	}
	if e.signsInThrough(t, idp, p.Slug) {
		t.Error("the instance accepted a signature by a certificate it has not read")
	}

	idp.FailMetadata(0)
	e.refreshMetadata(t)

	if got = e.getProvider(t, p.Slug); certificateHashes(got)[0] == certificateHashes(p)[0] {
		t.Error("the next round did not read the metadata again")
	}
	if !e.signsInThrough(t, idp, p.Slug) {
		t.Error("a person signed by the rotated certificate could not sign in after the retry")
	}
}

func TestMetadataThatWasPastedOrBelongsToADisabledProviderIsNeverRead(t *testing.T) {
	e, c, idp := refreshEnv(t)
	pasted := e.createSAML(t, idp, func(b *management.AuthProviderCreate) {
		b.IdpMetadataUrl, b.IdpMetadataXml = nil, ptr(idp.Metadata(testkit.SAMLMetadataOptions{}))
	})
	disabled := e.createSAML(t, idp, nil)
	off, err := e.admin.DisableAuthProviderWithResponse(t.Context(), disabled.Slug)
	if err != nil || off.JSON200 == nil {
		t.Fatalf("disabling: %v, %v", off.StatusCode(), err)
	}
	disabled = *off.JSON200
	fetched := idp.MetadataFetches()
	idp.RotateSigningKey(t, false)
	c.Advance(30 * 24 * time.Hour)

	e.refreshMetadata(t)

	if n := idp.MetadataFetches(); n != fetched {
		t.Errorf("the metadata was fetched %d times for providers that are never refreshed", n-fetched)
	}
	for _, p := range []management.AuthProvider{pasted, disabled} {
		if got := e.getProvider(t, p.Slug); certificateHashes(got)[0] != certificateHashes(p)[0] || got.Version != p.Version {
			t.Errorf("provider %s (version %d) changed to %v", p.Slug, got.Version, certificateHashes(got))
		}
	}
}

func TestTwoReplicasRefreshTheMetadataOnce(t *testing.T) {
	c := testkit.Clock()
	idp := testkit.StartForgingSAMLIDP(t)
	idp.Now = c.Now
	first, second := start(t, testkit.WithClock(c)), start(t, testkit.WithClock(c))
	p := first.createSAML(t, idp, nil)
	fetched := idp.MetadataFetches()
	// The identity provider answers slowly enough for the replicas' rounds to
	// overlap: the one that finds the other refreshing leaves it the work.
	idp.DelayMetadata(500 * time.Millisecond)
	idp.RotateSigningKey(t, false)
	c.Advance(25 * time.Hour)

	var wg sync.WaitGroup
	for _, replica := range []env{first, second} {
		wg.Go(func() { replica.refreshMetadata(t) })
	}
	wg.Wait()

	if n := idp.MetadataFetches(); n != fetched+1 {
		t.Errorf("the metadata was fetched %d times by two replicas, want once", n-fetched)
	}
	if n := len(first.auditJSON(t, "auth_provider.metadata_refreshed", p.Id.String())); n != 1 {
		t.Errorf("%d metadata_refreshed entries, want one", n)
	}
	if got := first.getProvider(t, p.Slug); got.Version != p.Version+1 {
		t.Errorf("version %d, want %d", got.Version, p.Version+1)
	}
}
