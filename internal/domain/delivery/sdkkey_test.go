package delivery_test

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var keyIDs = id.NewSource(clock.NewFake(bumpedAt))

func newKey(t *testing.T, kind delivery.SDKKeyKind, origins ...string) (delivery.SDKKey, string) {
	t.Helper()
	k, v, err := delivery.NewSDKKey(id.New[delivery.SDKKey](keyIDs), delivery.NewSDKKeyInput{
		Project: id.New[workspace.Project](keyIDs), Environment: id.New[workspace.Environment](keyIDs),
		Kind: kind, Name: "checkout", AllowedOrigins: origins, Now: bumpedAt,
	}, rand.Reader)
	if err != nil {
		t.Fatalf("NewSDKKey(%s) = %v", kind, err)
	}
	return k, v.Reveal()
}

// violations lists an invalid error's violations as "pointer code".
func violations(t *testing.T, err error, code string) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != code {
		t.Fatalf("err = %v, want invalid %s", err, code)
	}
	out := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func TestAnSDKKeyValueIsItsKindsPrefixAnd40Base62CharactersStoredOnlyAsItsHash(t *testing.T) {
	for kind, prefix := range map[delivery.SDKKeyKind]string{delivery.SDKKeyServer: "fn_srv_", delivery.SDKKeyClient: "fn_cli_"} {
		k, v := newKey(t, kind)
		if !regexp.MustCompile(`^` + prefix + `[0-9A-Za-z]{40}$`).MatchString(v) {
			t.Errorf("%s value %q is not %s and 40 base62 characters", kind, v, prefix)
		}
		if k.KeyID != v[len(prefix):len(prefix)+8] {
			t.Errorf("%s key ID = %q, want the 8 characters after the prefix of %q", kind, k.KeyID, v)
		}
		if k.Hash != sha256.Sum256([]byte(v)) {
			t.Errorf("%s hash is not the SHA-256 of the whole value", kind)
		}
		if k.Kind != kind || k.Name != "checkout" || !k.CreatedAt.Equal(bumpedAt) || k.ExpiresAt != nil || k.RevokedAt != nil {
			t.Errorf("key = %+v", k)
		}
	}
}

func TestAnSDKKeyValueIsParsedToItsKindAndHashAndAnythingElseIsInvalid(t *testing.T) {
	_, server := newKey(t, delivery.SDKKeyServer)
	kind, hash, err := delivery.ParseSDKKeyValue(server)
	if err != nil || kind != delivery.SDKKeyServer || hash != sha256.Sum256([]byte(server)) {
		t.Errorf("ParseSDKKeyValue(server) = %s, %x, %v", kind, hash, err)
	}
	_, client := newKey(t, delivery.SDKKeyClient)
	if kind, _, err := delivery.ParseSDKKeyValue(client); err != nil || kind != delivery.SDKKeyClient {
		t.Errorf("ParseSDKKeyValue(client) = %s, %v", kind, err)
	}
	for name, v := range map[string]string{
		"empty":           "",
		"an API token":    "fn_prj_" + server[7:],
		"too short":       server[:len(server)-1],
		"too long":        server + "a",
		"not base62":      server[:len(server)-1] + "-",
		"no prefix":       server[7:],
		"an upper prefix": "FN_SRV_" + server[7:],
	} {
		if _, _, err := delivery.ParseSDKKeyValue(v); !errors.Is(err, delivery.ErrSDKKeyInvalid) {
			t.Errorf("%s: err = %v, want sdk_key_invalid", name, err)
		}
	}
}

func TestAnSDKKeysStatusFollowsItsRevocationAndExpiry(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyServer)
	if s := k.StatusAt(bumpedAt.Add(24 * time.Hour)); s != delivery.SDKKeyActive {
		t.Errorf("a new key is %s, want active", s)
	}
	rotated, _, _, err := k.Rotate(id.New[delivery.SDKKey](keyIDs), time.Hour, bumpedAt, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if s := rotated.StatusAt(bumpedAt.Add(59 * time.Minute)); s != delivery.SDKKeyRotating {
		t.Errorf("within its overlap a rotated key is %s, want rotating", s)
	}
	if s := rotated.StatusAt(bumpedAt.Add(time.Hour)); s != delivery.SDKKeyExpired {
		t.Errorf("at the end of its overlap a rotated key is %s, want expired", s)
	}
	revoked, _ := rotated.Revoke(bumpedAt.Add(time.Minute))
	if s := revoked.StatusAt(bumpedAt.Add(2 * time.Minute)); s != delivery.SDKKeyRevoked {
		t.Errorf("a revoked key within its overlap is %s, want revoked", s)
	}
	if s := revoked.StatusAt(bumpedAt.Add(2 * time.Hour)); s != delivery.SDKKeyRevoked {
		t.Errorf("a revoked key past its overlap is %s, want revoked", s)
	}
}

func TestRotatingReplacesAnActiveKeyWithTheSameKindNameAndOriginsAndEndsItAfterTheOverlap(t *testing.T) {
	k, value := newKey(t, delivery.SDKKeyClient, "https://shop.example")
	at := bumpedAt.Add(time.Hour)
	rotated, replacement, v, err := k.Rotate(id.New[delivery.SDKKey](keyIDs), 7*24*time.Hour, at, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ExpiresAt == nil || !rotated.ExpiresAt.Equal(at.Add(7*24*time.Hour)) {
		t.Errorf("the rotated key expires at %v, want the rotation plus the overlap", rotated.ExpiresAt)
	}
	if replacement.ID == k.ID || v.Reveal() == value || replacement.Hash == k.Hash || replacement.Kind != k.Kind ||
		replacement.Name != k.Name || !slices.Equal(replacement.AllowedOrigins, k.AllowedOrigins) ||
		replacement.Environment != k.Environment || replacement.Project != k.Project ||
		!replacement.CreatedAt.Equal(at) || replacement.ExpiresAt != nil {
		t.Errorf("replacement = %+v, want a new active key like %+v", replacement, k)
	}
	immediate, _, _, err := k.Rotate(id.New[delivery.SDKKey](keyIDs), 0, at, rand.Reader)
	if err != nil || immediate.StatusAt(at) != delivery.SDKKeyExpired {
		t.Errorf("a rotation without overlap leaves the key %s (%v), want expired at once", immediate.StatusAt(at), err)
	}
}

func TestOnlyAnActiveKeyIsRotated(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyServer)
	rotated, _, _, err := k.Rotate(id.New[delivery.SDKKey](keyIDs), time.Hour, bumpedAt, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	revoked, _ := k.Revoke(bumpedAt)
	expired, _, _, _ := k.Rotate(id.New[delivery.SDKKey](keyIDs), 0, bumpedAt, rand.Reader)
	for name, key := range map[string]delivery.SDKKey{"rotating": rotated, "revoked": revoked, "expired": expired} {
		_, _, _, err := key.Rotate(id.New[delivery.SDKKey](keyIDs), time.Hour, bumpedAt.Add(time.Minute), rand.Reader)
		if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindConflict || e.Code != "sdk_key_not_active" {
			t.Errorf("rotating a %s key: err = %v, want conflict sdk_key_not_active", name, err)
		}
	}
}

func TestTheRotationOverlapIsZeroToNinetyDaysAndSevenDaysByDefault(t *testing.T) {
	if d, err := delivery.RotationOverlap(nil); err != nil || d != 7*24*time.Hour {
		t.Errorf("default overlap = %v, %v; want 7 days", d, err)
	}
	for _, seconds := range []int64{0, 3600, 7_776_000} {
		if d, err := delivery.RotationOverlap(&seconds); err != nil || d != time.Duration(seconds)*time.Second {
			t.Errorf("overlap %d s = %v, %v", seconds, d, err)
		}
	}
	for _, seconds := range []int64{-1, 7_776_001, 18_446_744_074} {
		_, err := delivery.RotationOverlap(&seconds)
		if got := violations(t, err, "invalid_rotation"); !slices.Equal(got, []string{"/overlap_seconds out_of_range"}) {
			t.Errorf("overlap %d s: violations %v", seconds, got)
		}
	}
}

func TestAnEnvironmentHoldsAtMostTenActiveKeysOfAKind(t *testing.T) {
	if err := delivery.CheckActiveSDKKeys(9); err != nil {
		t.Errorf("a tenth key: %v", err)
	}
	err := delivery.CheckActiveSDKKeys(10)
	if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindConflict || e.Code != "sdk_key_limit_reached" {
		t.Errorf("an eleventh key: err = %v, want conflict sdk_key_limit_reached", err)
	}
}

func TestRevokingSetsTheRevocationOnce(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyServer)
	revoked, changed := k.Revoke(bumpedAt.Add(time.Minute))
	if !changed || revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(bumpedAt.Add(time.Minute)) {
		t.Errorf("revoked = %+v, changed %v", revoked, changed)
	}
	again, changed := revoked.Revoke(bumpedAt.Add(time.Hour))
	if changed || !again.RevokedAt.Equal(bumpedAt.Add(time.Minute)) {
		t.Errorf("revoking again changed it: %+v", again)
	}
}

func TestAllowedOriginsAreNormalizedToSchemeHostAndANonDefaultPort(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyClient,
		"HTTPS://Shop.Example.COM", "https://shop.example.com:443/", "http://localhost:80", "http://localhost:8080",
		"https://[::1]:8443", "https://shop.example.com")
	want := []string{"https://shop.example.com", "http://localhost", "http://localhost:8080", "https://[::1]:8443"}
	if !slices.Equal(k.AllowedOrigins, want) {
		t.Errorf("allowed origins = %v, want %v", k.AllowedOrigins, want)
	}
	if k, _ := newKey(t, delivery.SDKKeyClient); k.AllowedOrigins == nil || len(k.AllowedOrigins) != 0 {
		t.Errorf("no origins = %#v, want an empty list", k.AllowedOrigins)
	}
}

func TestAnSDKKeyIsRefusedWithEveryBrokenRule(t *testing.T) {
	many := make([]string, 51)
	for i := range many {
		many[i] = "https://shop" + strings.Repeat("a", i) + ".example"
	}
	for name, tc := range map[string]struct {
		in   delivery.NewSDKKeyInput
		want []string
	}{
		"an unknown kind":         {delivery.NewSDKKeyInput{Kind: "mobile", Name: "app"}, []string{"/kind unknown_kind"}},
		"a blank name":            {delivery.NewSDKKeyInput{Kind: delivery.SDKKeyServer, Name: "  "}, []string{"/name invalid_name"}},
		"a name over 200":         {delivery.NewSDKKeyInput{Kind: delivery.SDKKeyServer, Name: strings.Repeat("n", 201)}, []string{"/name invalid_name"}},
		"origins on a server key": {delivery.NewSDKKeyInput{Kind: delivery.SDKKeyServer, Name: "api", AllowedOrigins: []string{"https://shop.example"}}, []string{"/allowed_origins server_key_origins"}},
		"over 50 origins":         {delivery.NewSDKKeyInput{Kind: delivery.SDKKeyClient, Name: "web", AllowedOrigins: many}, []string{"/allowed_origins too_many"}},
		"origins that are no origin": {delivery.NewSDKKeyInput{Kind: delivery.SDKKeyClient, Name: "web", AllowedOrigins: []string{
			"https://shop.example/cart", "shop.example", "ftp://shop.example", "https://user@shop.example", "https://shop.example?q=1",
			"https://shop.example#top", "*", "https://", "https://shop.example",
		}}, []string{
			"/allowed_origins/0 invalid_origin", "/allowed_origins/1 invalid_origin", "/allowed_origins/2 invalid_origin",
			"/allowed_origins/3 invalid_origin", "/allowed_origins/4 invalid_origin", "/allowed_origins/5 invalid_origin",
			"/allowed_origins/6 invalid_origin", "/allowed_origins/7 invalid_origin",
		}},
	} {
		tc.in.Now = bumpedAt
		_, _, err := delivery.NewSDKKey(id.New[delivery.SDKKey](keyIDs), tc.in, rand.Reader)
		if got := violations(t, err, "invalid_sdk_key"); !slices.Equal(got, tc.want) {
			t.Errorf("%s: violations %v, want %v", name, got, tc.want)
		}
	}
}

func TestAnUpdateRenamesAndReplacesTheAllowedOrigins(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyClient, "https://shop.example")
	name := " storefront "
	after, changed, err := k.Update(delivery.SDKKeyChange{Name: &name, AllowedOrigins: []string{"https://SHOP.example:443", "http://localhost:3000"}})
	if err != nil || !changed || after.Name != "storefront" ||
		!slices.Equal(after.AllowedOrigins, []string{"https://shop.example", "http://localhost:3000"}) {
		t.Errorf("updated = %+v, %v, %v", after, changed, err)
	}
	same, changed, err := after.Update(delivery.SDKKeyChange{AllowedOrigins: []string{"http://localhost:3000", "https://shop.example"}})
	if err != nil || !changed || same.Name != "storefront" {
		t.Errorf("reordering the origins: %+v, changed %v, %v", same, changed, err)
	}
	if _, unchanged, sameErr := same.Update(delivery.SDKKeyChange{AllowedOrigins: same.AllowedOrigins}); sameErr != nil || unchanged {
		t.Errorf("an update to the same values changed %v, %v", unchanged, sameErr)
	}
	cleared, changed, err := same.Update(delivery.SDKKeyChange{AllowedOrigins: []string{}})
	if err != nil || !changed || len(cleared.AllowedOrigins) != 0 {
		t.Errorf("clearing the origins: %+v, %v, %v", cleared, changed, err)
	}

	server, _ := newKey(t, delivery.SDKKeyServer)
	_, _, err = server.Update(delivery.SDKKeyChange{AllowedOrigins: []string{"https://shop.example"}})
	if got := violations(t, err, "invalid_sdk_key"); !slices.Equal(got, []string{"/allowed_origins server_key_origins"}) {
		t.Errorf("origins on a server key: %v", got)
	}
	blank := ""
	_, _, err = server.Update(delivery.SDKKeyChange{Name: &blank, AllowedOrigins: []string{}})
	if got := violations(t, err, "invalid_sdk_key"); !slices.Equal(got, []string{"/name invalid_name"}) {
		t.Errorf("a blank name: %v", got)
	}
}

func TestAKeyIsAcceptedUntilRevokedOrPastItsOverlap(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyServer)
	if err := k.AcceptAt(bumpedAt); err != nil {
		t.Errorf("an active key: %v", err)
	}
	rotated, _, _, err := k.Rotate(id.New[delivery.SDKKey](keyIDs), time.Hour, bumpedAt, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := rotated.AcceptAt(bumpedAt.Add(time.Hour - time.Millisecond)); err != nil {
		t.Errorf("a key within its overlap: %v", err)
	}
	if err := rotated.AcceptAt(bumpedAt.Add(time.Hour)); !hasCode(err, apperrors.KindUnauthenticated, "sdk_key_expired") {
		t.Errorf("a key at the end of its overlap: %v, want sdk_key_expired", err)
	}
	revoked, _ := rotated.Revoke(bumpedAt)
	if err := revoked.AcceptAt(bumpedAt); !hasCode(err, apperrors.KindUnauthenticated, "sdk_key_revoked") {
		t.Errorf("a revoked key: %v, want sdk_key_revoked", err)
	}
}

func TestAnOperationRefusesAKeyOfAKindItDoesNotServe(t *testing.T) {
	server, _ := newKey(t, delivery.SDKKeyServer)
	client, _ := newKey(t, delivery.SDKKeyClient)
	if err := server.CheckKind(delivery.SDKKeyServer); err != nil {
		t.Errorf("a server key where server keys are served: %v", err)
	}
	if err := client.CheckKind(delivery.SDKKeyServer, delivery.SDKKeyClient); err != nil {
		t.Errorf("a client key where both kinds are served: %v", err)
	}
	if err := client.CheckKind(delivery.SDKKeyServer); !hasCode(err, apperrors.KindForbidden, "key_kind_not_allowed") {
		t.Errorf("a client key where only server keys are served: %v, want key_kind_not_allowed", err)
	}
}

func hasCode(err error, kind apperrors.Kind, code string) bool {
	e, ok := apperrors.From(err)
	return ok && e.Kind == kind && e.Code == code
}

func TestAKeysUseIsRecordedAtMostOnceAMinute(t *testing.T) {
	k, _ := newKey(t, delivery.SDKKeyServer)
	if !k.UseToRecord(bumpedAt) {
		t.Error("the first use of a key is not recorded")
	}
	used := bumpedAt
	k.LastUsedAt = &used
	if k.UseToRecord(bumpedAt.Add(time.Minute - time.Millisecond)) {
		t.Error("a use within a minute of the last one recorded is recorded again")
	}
	if !k.UseToRecord(bumpedAt.Add(time.Minute)) {
		t.Error("a use a minute after the last one recorded is not recorded")
	}
}

func TestOnlyAClientKeyMayBePresentedInAURL(t *testing.T) {
	_, server := newKey(t, delivery.SDKKeyServer)
	_, client := newKey(t, delivery.SDKKeyClient)
	for _, v := range []string{server, "fn_srv_unknown"} {
		if err := delivery.CheckKeyInQuery(v); !errors.Is(err, delivery.ErrServerKeyInQuery) {
			t.Errorf("CheckKeyInQuery(%q) = %v, want server_key_in_query", v, err)
		}
	}
	for _, v := range []string{client, "anything else"} {
		if err := delivery.CheckKeyInQuery(v); err != nil {
			t.Errorf("CheckKeyInQuery(%q) = %v, want it left to authentication", v, err)
		}
	}
}
