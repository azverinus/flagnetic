package delivery

import (
	"crypto/sha256"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// SDKKeyID identifies an SDK key.
type SDKKeyID = id.Of[SDKKey]

// SDKKeyKind says what an SDK key may receive.
type SDKKeyKind string

// The kinds of SDK key.
const (
	// SDKKeyServer downloads its environment's ruleset, private segments
	// included, so it belongs on a server and never in a browser or an app.
	SDKKeyServer SDKKeyKind = "server"
	// SDKKeyClient never receives a ruleset, only the values remote
	// evaluation serves it, so it may be shipped in a browser or an app.
	SDKKeyClient SDKKeyKind = "client"
)

// sdkKeyPrefixes start a key's value, so a leaked value is recognizable for
// what it is and its kind is known before any lookup.
var sdkKeyPrefixes = map[SDKKeyKind]string{SDKKeyServer: "fn_srv_", SDKKeyClient: "fn_cli_"}

const (
	sdkKeyBodyLength = 40
	sdkKeyIDLength   = 8
	maxSDKKeyName    = 200
	// MaxActiveSDKKeys is how many active keys of one kind an environment
	// holds at most, so keys nobody uses any more do not pile up unnoticed.
	MaxActiveSDKKeys = 10
	// MaxAllowedOrigins is how many origins a client key lists at most.
	MaxAllowedOrigins = 50
	// DefaultRotationOverlap is how long a rotated key keeps working when
	// the rotation names no overlap: a week, enough to roll a release out.
	DefaultRotationOverlap = 7 * 24 * time.Hour
	// MaxRotationOverlap bounds the overlap, so a rotated key always ends.
	MaxRotationOverlap = 90 * 24 * time.Hour
)

// SDKKeyHash is the SHA-256 of a key's whole value, the only form in which
// the value is stored.
type SDKKeyHash [sha256.Size]byte

// SDKKeyStatus is what an SDK key is at a time.
type SDKKeyStatus string

// The statuses of an SDK key.
const (
	// SDKKeyActive: accepted, neither rotated nor revoked.
	SDKKeyActive SDKKeyStatus = "active"
	// SDKKeyRotating: rotated, still accepted until its overlap ends.
	SDKKeyRotating SDKKeyStatus = "rotating"
	// SDKKeyExpired: rotated, and its overlap has ended.
	SDKKeyExpired SDKKeyStatus = "expired"
	// SDKKeyRevoked: refused from its revocation on, whatever its expiry.
	SDKKeyRevoked SDKKeyStatus = "revoked"
)

// SDKKey is the credential that identifies one environment to an SDK.
type SDKKey struct {
	ID          SDKKeyID
	Project     workspace.ProjectID
	Environment workspace.EnvironmentID
	Kind        SDKKeyKind
	Name        string
	// KeyID is the first 8 characters of the value after its prefix, kept in
	// the clear so a member can tell which key a service holds.
	KeyID string
	Hash  SDKKeyHash
	// AllowedOrigins are the browser origins a client key is accepted from,
	// normalized; empty allows any origin, and a server key holds none.
	AllowedOrigins []string
	CreatedAt      time.Time
	// LastUsedAt is when the key was last presented, as last recorded.
	LastUsedAt *time.Time
	// ExpiresAt is set when the key is rotated: the end of its overlap.
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

// NewSDKKeyInput is what an SDK key is made of.
type NewSDKKeyInput struct {
	Project        workspace.ProjectID
	Environment    workspace.EnvironmentID
	Kind           SDKKeyKind
	Name           string
	AllowedOrigins []string
	Now            time.Time
}

// NewSDKKey validates in and returns the key with the value drawn for it
// from random: the kind's prefix and 40 base62 characters, shown once and
// then kept only as its hash. The first 8 characters are shown as the key ID,
// so the other 32 carry the secret, about 190 bits.
func NewSDKKey(key SDKKeyID, in NewSDKKeyInput, random io.Reader) (SDKKey, secret.String, error) {
	c := checkSDKKey()
	if _, ok := sdkKeyPrefixes[in.Kind]; !ok {
		c.violations = append(c.violations, apperrors.Violation{
			Pointer: "/kind", Code: "unknown_kind", Detail: "an SDK key is a server key or a client key",
			Params: map[string]any{"allowed": []string{string(SDKKeyServer), string(SDKKeyClient)}},
		})
	}
	c.nameOf(in.Name).originsOf(in.Kind, in.AllowedOrigins)
	if err := c.err(); err != nil {
		return SDKKey{}, secret.String{}, err
	}
	k := SDKKey{
		ID: key, Project: in.Project, Environment: in.Environment, Kind: in.Kind, Name: c.name,
		AllowedOrigins: c.origins, CreatedAt: in.Now,
	}
	value := k.draw(random)
	return k, value, nil
}

// draw gives k a new value from random and returns it.
func (k *SDKKey) draw(random io.Reader) secret.String {
	prefix := sdkKeyPrefixes[k.Kind]
	body := secret.Base62(random, sdkKeyBodyLength)
	value := prefix + body
	k.KeyID = body[:sdkKeyIDLength]
	k.Hash = sha256.Sum256([]byte(value))
	return secret.New(value)
}

// ErrSDKKeyInvalid refuses a presented value that is no SDK key, malformed
// or unknown alike, so a caller cannot tell which.
var ErrSDKKeyInvalid = apperrors.Unauthenticated("sdk_key_invalid", "the SDK key is not valid")

// ParseSDKKeyValue returns the kind and hash of a well-formed SDK key value.
// Any other value is ErrSDKKeyInvalid, refused without a lookup.
func ParseSDKKeyValue(value string) (SDKKeyKind, SDKKeyHash, error) {
	for kind, prefix := range sdkKeyPrefixes {
		body, ok := strings.CutPrefix(value, prefix)
		if ok && len(body) == sdkKeyBodyLength && secret.IsBase62(body) {
			return kind, sha256.Sum256([]byte(value)), nil
		}
	}
	return "", SDKKeyHash{}, ErrSDKKeyInvalid
}

// ErrServerKeyInQuery refuses a server key presented in a URL, where logs,
// histories and referrers keep it.
var ErrServerKeyInQuery = apperrors.Forbidden("server_key_in_query",
	"a server key must never be put in a URL; send it in the Authorization header")

// CheckKeyInQuery refuses a value presented in a URL's query that is shaped
// like a server key, known or not: only a client key, which ships in apps
// and browsers anyway, may travel there, for a browser's EventSource that
// cannot set headers. The refusal does not depend on a lookup, so it tells
// nothing of which keys exist.
func CheckKeyInQuery(value string) error {
	if strings.HasPrefix(value, sdkKeyPrefixes[SDKKeyServer]) {
		return ErrServerKeyInQuery
	}
	return nil
}

// StatusAt returns what the key is at t: revoked once revoked, whatever its
// expiry; expired from the end of its overlap; rotating within it; active
// otherwise.
func (k SDKKey) StatusAt(t time.Time) SDKKeyStatus {
	switch {
	case k.RevokedAt != nil:
		return SDKKeyRevoked
	case k.ExpiresAt == nil:
		return SDKKeyActive
	case k.ExpiresAt.After(t):
		return SDKKeyRotating
	default:
		return SDKKeyExpired
	}
}

// ErrSDKKeyMissing refuses a request to the SDK API that presents no key.
var ErrSDKKeyMissing = apperrors.Unauthenticated("sdk_key_missing", "the request carries no SDK key")

var (
	errSDKKeyRevoked = apperrors.Unauthenticated("sdk_key_revoked", "the SDK key is revoked")
	errSDKKeyExpired = apperrors.Unauthenticated("sdk_key_expired",
		"the SDK key was rotated and its rotation overlap has ended")
	errKeyKindNotAllowed = apperrors.Forbidden("key_kind_not_allowed",
		"this operation does not serve this kind of SDK key")
)

// AcceptAt refuses the key presented at t when it no longer works: revoked
// (sdk_key_revoked), or rotated and past its overlap (sdk_key_expired). The
// time is the request's, so a rotation ends on time without anyone acting.
func (k SDKKey) AcceptAt(t time.Time) error {
	switch k.StatusAt(t) {
	case SDKKeyRevoked:
		return errSDKKeyRevoked
	case SDKKeyExpired:
		return errSDKKeyExpired
	default:
		return nil
	}
}

// CheckKind refuses the key, key_kind_not_allowed, unless it is of one of
// the kinds served. A client key ships inside apps anyone can read, so it
// never reaches what only a server key may, such as a ruleset carrying
// private segments.
func (k SDKKey) CheckKind(served ...SDKKeyKind) error {
	if slices.Contains(served, k.Kind) {
		return nil
	}
	return errKeyKindNotAllowed
}

// SDKKeyUseInterval is how often at most a key's last use is recorded: often
// enough for a member to see whether a key is still in use, rarely enough
// that an SDK polling every few seconds costs no write per request.
const SDKKeyUseInterval = time.Minute

// UseToRecord reports whether a use of the key at at is worth recording: its
// first, or one at least SDKKeyUseInterval after the last recorded.
func (k SDKKey) UseToRecord(at time.Time) bool {
	return k.LastUsedAt == nil || !at.Before(k.LastUsedAt.Add(SDKKeyUseInterval))
}

// IsActive reports whether the key is neither rotated nor revoked: what
// counts towards MaxActiveSDKKeys and what may be rotated.
func (k SDKKey) IsActive() bool { return k.RevokedAt == nil && k.ExpiresAt == nil }

// CheckActiveSDKKeys refuses a new key in an environment already holding
// active keys of its kind up to MaxActiveSDKKeys.
func CheckActiveSDKKeys(active int) error {
	if active >= MaxActiveSDKKeys {
		return apperrors.Conflict("sdk_key_limit_reached",
			"the environment already holds "+strconv.Itoa(MaxActiveSDKKeys)+" active keys of this kind; revoke or rotate one first").
			WithExtension("max", MaxActiveSDKKeys)
	}
	return nil
}

// RotationOverlap returns the overlap of a rotation given in seconds, or
// DefaultRotationOverlap when none is given; 0 ends the rotated key at once.
func RotationOverlap(seconds *int64) (time.Duration, error) {
	if seconds == nil {
		return DefaultRotationOverlap, nil
	}
	maxSeconds := int64(MaxRotationOverlap / time.Second)
	if *seconds < 0 || *seconds > maxSeconds {
		return 0, apperrors.Invalid("invalid_rotation", "the rotation is not valid", apperrors.Violation{
			Pointer: "/overlap_seconds", Code: "out_of_range",
			Detail: "the overlap is 0 to " + strconv.FormatInt(maxSeconds, 10) + " seconds (90 days)",
			Params: map[string]any{"min": 0, "max": maxSeconds},
		})
	}
	return time.Duration(*seconds) * time.Second, nil
}

var errSDKKeyNotActive = apperrors.Conflict("sdk_key_not_active",
	"only a key that is neither rotated nor revoked can be rotated, so an ended key is never brought back")

// Rotate ends the key overlap after now and returns it with its
// replacement, identified by replacement, of the same kind, name and
// allowed origins, and the replacement's value drawn from random. Only an
// active key is rotated.
func (k SDKKey) Rotate(replacement SDKKeyID, overlap time.Duration, now time.Time, random io.Reader) (SDKKey, SDKKey, secret.String, error) {
	if !k.IsActive() {
		return SDKKey{}, SDKKey{}, secret.String{}, errSDKKeyNotActive
	}
	next := SDKKey{
		ID: replacement, Project: k.Project, Environment: k.Environment, Kind: k.Kind, Name: k.Name,
		AllowedOrigins: slices.Clone(k.AllowedOrigins), CreatedAt: now,
	}
	value := next.draw(random)
	expires := now.Add(overlap)
	k.ExpiresAt = &expires
	return k, next, value, nil
}

// Revoke returns the key revoked at now and whether that changed it: a key
// revoked already keeps its revocation.
func (k SDKKey) Revoke(now time.Time) (SDKKey, bool) {
	if k.RevokedAt != nil {
		return k, false
	}
	k.RevokedAt = &now
	return k, true
}

// SDKKeyChange is a change to a key: a new name when Name is set, and the
// allowed origins, always given, so a change that forgets them cannot lift
// a restriction by accident.
type SDKKeyChange struct {
	Name           *string
	AllowedOrigins []string
}

// Update returns the key with change applied and whether that changed it.
func (k SDKKey) Update(change SDKKeyChange) (SDKKey, bool, error) {
	c := checkSDKKey()
	name := k.Name
	if change.Name != nil {
		c.nameOf(*change.Name)
		name = c.name
	}
	c.originsOf(k.Kind, change.AllowedOrigins)
	if err := c.err(); err != nil {
		return SDKKey{}, false, err
	}
	if name == k.Name && slices.Equal(c.origins, k.AllowedOrigins) {
		return k, false, nil
	}
	k.Name, k.AllowedOrigins = name, c.origins
	return k, true, nil
}

// sdkKeyFields checks what a key is given and answers every broken rule at
// once, so a form shows every field to fix in one round.
type sdkKeyFields struct {
	violations []apperrors.Violation
	name       string
	origins    []string
}

func checkSDKKey() *sdkKeyFields { return &sdkKeyFields{} }

func (c *sdkKeyFields) nameOf(name string) *sdkKeyFields {
	c.name = strings.TrimSpace(name)
	if c.name == "" || utf8.RuneCountInString(c.name) > maxSDKKeyName {
		c.violations = append(c.violations, apperrors.Violation{
			Pointer: "/name", Code: "invalid_name", Detail: "a name has 1 to 200 characters and is not blank",
			Params: map[string]any{"min": 1, "max": maxSDKKeyName},
		})
	}
	return c
}

// originsOf normalizes the allowed origins of a key of kind, dropping
// duplicates after normalizing and keeping the first one's place.
func (c *sdkKeyFields) originsOf(kind SDKKeyKind, origins []string) *sdkKeyFields {
	c.origins = []string{}
	switch {
	case len(origins) == 0:
		return c
	case kind == SDKKeyServer:
		// A server key is never used from a browser: an origin on it would
		// only make someone believe it is restricted.
		c.violations = append(c.violations, apperrors.Violation{
			Pointer: "/allowed_origins", Code: "server_key_origins",
			Detail: "allowed origins are for client keys; a server key is never used from a browser",
		})
		return c
	case len(origins) > MaxAllowedOrigins:
		c.violations = append(c.violations, apperrors.Violation{
			Pointer: "/allowed_origins", Code: "too_many", Detail: "a key lists at most 50 allowed origins",
			Params: map[string]any{"max": MaxAllowedOrigins},
		})
		return c
	}
	for i, o := range origins {
		n, ok := NormalizeOrigin(o)
		if !ok {
			c.violations = append(c.violations, apperrors.Violation{
				Pointer: "/allowed_origins/" + strconv.Itoa(i), Code: "invalid_origin",
				Detail: "an allowed origin is a scheme, http or https, a host and an optional port, such as https://app.example.com",
			})
			continue
		}
		if !slices.Contains(c.origins, n) {
			c.origins = append(c.origins, n)
		}
	}
	return c
}

func (c *sdkKeyFields) err() error {
	if len(c.violations) == 0 {
		return nil
	}
	return apperrors.Invalid("invalid_sdk_key", "the SDK key is not valid", c.violations...)
}

// NormalizeOrigin returns origin as a browser sends it in an Origin header,
// scheme://host[:port] in lower case without the scheme's default port, so
// an origin listed in any spelling matches; false for anything that is no
// http or https origin, such as a URL with a path, a query or credentials.
func NormalizeOrigin(origin string) (string, bool) {
	u, err := url.Parse(origin)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	defaultPort := map[string]string{"http": "80", "https": "443"}[scheme]
	if defaultPort == "" {
		return "", false
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", false
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" || port == defaultPort {
		return scheme + "://" + host, true
	}
	return scheme + "://" + host + ":" + port, true
}
