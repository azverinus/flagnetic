package sinks

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

const maxNameLength = 200

// What a sink takes by default and at most, by kind. A zero bound is none.
type batching struct {
	size, maxSize             int
	flush, minFlush, maxFlush time.Duration
}

func batchingOf(k Kind) batching {
	switch k {
	case KindClickHouse:
		return batching{size: 5_000, flush: 10 * time.Second}
	case KindObjectStorage:
		return batching{size: 500_000, flush: 5 * time.Minute, minFlush: time.Minute, maxFlush: time.Hour}
	default:
		return batching{size: 500, maxSize: 5_000, flush: 5 * time.Second}
	}
}

// DefaultTablePrefix prefixes the tables of a ClickHouse sink that names no
// prefix.
const DefaultTablePrefix = "flagnetic"

// reservedClickHouseSettings are the query parameters delivery sets itself,
// which a sink's settings may not name.
var reservedClickHouseSettings = []string{
	"query", "query_id", "database", "user", "password", "async_insert", "date_time_input_format",
}

// identifier is what a table prefix may be: a name needing no quoting, so
// the table names a data engineer types are the ones the platform creates.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// bucketName is what a bucket may be called on the services this speaks to,
// which differ in how strict they are.
var bucketName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var errInsecureURL = apperrors.Invalid("insecure_url",
	"the target must be https; the operator allows http with FLAGNETIC_WEBHOOK_ALLOW_HTTP",
	apperrors.Violation{Pointer: "/webhook/url", Code: "insecure_url", Detail: "the target must be https"})

// Check returns d with its names trimmed, its repeats dropped and everything
// it left to a default filled in, or the refusal of the first rule it breaks,
// in this order: a malformed field (invalid_sink, naming every one), an
// environment the project does not have (unknown_environment), a webhook
// target that is not https unless policy allows it (insecure_url).
// environments are the keys of the project's environments. Where a target's
// name resolves to is judged apart, by the checks of addresses.
func Check(d Draft, environments []string, policy governance.TargetPolicy) (Draft, error) {
	var violations []apperrors.Violation
	add := func(pointer, code, detail string, params map[string]any) {
		violations = append(violations, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || utf8.RuneCountInString(d.Name) > maxNameLength {
		add("/name", "invalid_name", "a name has 1 to 200 characters and is not blank",
			map[string]any{"min": 1, "max": maxNameLength})
	}
	if slices.Contains(Kinds, d.Kind) {
		d = checkConfig(d, add)
	} else {
		add("/kind", "invalid_kind", "a sink is clickhouse, object_storage or webhook", nil)
	}
	for i, k := range d.EventKinds {
		if !slices.Contains(EventKinds, k) {
			add(fmt.Sprintf("/event_kinds/%d", i), "invalid_event_kind",
				"an event kind is exposure, alias or evaluation_summary", nil)
		}
	}
	d.EventKinds = distinct(d.EventKinds)
	d = checkBatching(d, add)
	if len(violations) > 0 {
		return d, apperrors.Invalid("invalid_sink", "the sink is not valid", violations...)
	}
	// Pointers index the list as the request wrote it, so they are judged
	// before repeats are dropped.
	var unknown []apperrors.Violation
	for i, e := range d.Environments {
		if !slices.Contains(environments, e) && !slices.Contains(d.Environments[:i], e) {
			unknown = append(unknown, apperrors.Violation{
				Pointer: fmt.Sprintf("/environments/%d", i), Code: "unknown_environment",
				Detail: "the project has no environment with this key",
			})
		}
	}
	d.Environments = distinct(d.Environments)
	if len(unknown) > 0 {
		return d, apperrors.Invalid("unknown_environment", "the project has no environment with this key", unknown...)
	}
	if d.Kind == KindWebhook && !policy.AllowHTTP {
		if u, err := url.Parse(d.Config.Webhook.URL); err == nil && u.Scheme == "http" {
			return d, errInsecureURL
		}
	}
	return d, nil
}

// checkConfig checks the configuration and the secret of d against its
// kind, which is one of Kinds, and fills in what they leave to a default.
func checkConfig(d Draft, add func(pointer, code, detail string, params map[string]any)) Draft {
	d.Config = d.Config.Clone()
	c := d.Config
	for _, other := range []struct {
		kind Kind
		set  bool
	}{{KindClickHouse, c.ClickHouse != nil}, {KindObjectStorage, c.ObjectStorage != nil}, {KindWebhook, c.Webhook != nil}} {
		switch {
		case other.kind == d.Kind && !other.set:
			add("/"+string(other.kind), "required", "a sink of this kind carries its configuration", nil)
		case other.kind != d.Kind && other.set:
			add("/"+string(other.kind), "not_allowed", "a sink carries the configuration of its own kind only", nil)
		}
	}
	switch d.Kind {
	case KindClickHouse:
		if c.ClickHouse != nil {
			checkClickHouse(c.ClickHouse, add)
		}
	case KindObjectStorage:
		if c.ObjectStorage != nil {
			checkObjectStorage(c.ObjectStorage, add)
		}
	case KindWebhook:
		if c.Webhook != nil {
			checkWebhook(c.Webhook, add)
		}
	}
	if pointer := d.Kind.secretPointer(); pointer != "" {
		if !d.KeepsSecret && KeepsSecret(d.Secret) {
			add(pointer, "required", "a secret is required", nil)
		}
	} else if d.Secret != "" && d.Secret != Masked {
		add("/webhook/secret", "not_allowed", "the server draws a webhook sink's secret", nil)
	}
	return d
}

func checkClickHouse(c *ClickHouse, add func(pointer, code, detail string, params map[string]any)) {
	c.URL = strings.TrimSpace(c.URL)
	switch {
	case c.URL == "":
		add("/clickhouse/url", "required", "a ClickHouse sink names where its HTTP interface answers", nil)
	case !validTarget(c.URL, false):
		add("/clickhouse/url", "invalid_url",
			"the URL is absolute, http or https, without credentials, a query or a fragment; "+
				"the username and password have fields of their own", nil)
	}
	if c.Database = strings.TrimSpace(c.Database); c.Database == "" {
		add("/clickhouse/database", "required", "a ClickHouse sink names its database", nil)
	}
	if c.TablePrefix = strings.TrimSpace(c.TablePrefix); c.TablePrefix == "" {
		c.TablePrefix = DefaultTablePrefix
	} else if !identifier.MatchString(c.TablePrefix) {
		add("/clickhouse/table_prefix", "invalid_table_prefix",
			"a table prefix is letters, digits and underscores, and does not start with a digit", nil)
	}
	if c.Username = strings.TrimSpace(c.Username); c.Username == "" {
		add("/clickhouse/username", "required", "a ClickHouse sink names the user it writes as", nil)
	}
	for _, name := range sortedKeys(c.Settings) {
		if name == "" || slices.Contains(reservedClickHouseSettings, name) {
			add("/clickhouse/settings/"+pointerToken(name), "reserved", "delivery sets this setting itself", nil)
		}
	}
}

func checkObjectStorage(c *ObjectStorage, add func(pointer, code, detail string, params map[string]any)) {
	if c.Endpoint = strings.TrimSpace(c.Endpoint); c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if !validTarget(c.Endpoint, false) || err != nil || strings.Trim(u.Path, "/") != "" {
			add("/object_storage/endpoint", "invalid_endpoint",
				"the endpoint is only a scheme, a host and a port, http or https, or empty for AWS; "+
					"the credentials and the prefix have fields of their own", nil)
		}
	}
	c.Region = strings.TrimSpace(c.Region)
	switch c.Bucket = strings.TrimSpace(c.Bucket); {
	case c.Bucket == "":
		add("/object_storage/bucket", "required", "an object storage sink names its bucket", nil)
	case len(c.Bucket) > 255 || !bucketName.MatchString(c.Bucket):
		add("/object_storage/bucket", "invalid_bucket", "a bucket name is letters, digits, dots, hyphens and underscores", nil)
	}
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	if c.AccessKeyID = strings.TrimSpace(c.AccessKeyID); c.AccessKeyID == "" {
		add("/object_storage/access_key_id", "required", "an object storage sink names the access key it writes with", nil)
	}
	if c.Format == "" {
		c.Format = "jsonl"
	}
	if c.Format != "jsonl" && c.Format != "parquet" {
		add("/object_storage/format", "invalid_format", "a format is jsonl or parquet", nil)
	}
	switch c.Compression {
	case "":
		c.Compression = "gzip"
		if c.Format == "parquet" {
			c.Compression = "zstd"
		}
	case "gzip", "zstd", "none":
		if c.Format == "parquet" && c.Compression != "zstd" {
			add("/object_storage/compression", "invalid_compression", "parquet files are always zstd", nil)
		}
	default:
		add("/object_storage/compression", "invalid_compression", "a compression is gzip, zstd or none", nil)
	}
}

func checkWebhook(c *Webhook, add func(pointer, code, detail string, params map[string]any)) {
	c.URL = strings.TrimSpace(c.URL)
	switch {
	case c.URL == "":
		add("/webhook/url", "required", "a webhook sink names where it posts", nil)
	case !validTarget(c.URL, true):
		add("/webhook/url", "invalid_url", "the URL is absolute, http or https, without credentials or a fragment", nil)
	}
	for _, name := range sortedKeys(c.Headers) {
		pointer := "/webhook/headers/" + pointerToken(name)
		switch {
		case strings.HasPrefix(strings.ToLower(name), "x-flagnetic-"):
			add(pointer, "reserved", "the X-Flagnetic- headers are the platform's own", nil)
		case !headerName(name) || !headerValue(c.Headers[name]):
			add(pointer, "invalid_header", "a header has a name of token characters and a value without line breaks", nil)
		}
	}
}

// validTarget reports whether raw is an absolute http or https URL without
// credentials or a fragment. It may carry a query only when query is set: a
// webhook sink's URL may name a token there, while the platform builds the
// requests to the other kinds itself and a query would be lost.
func validTarget(raw string, query bool) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil &&
		!strings.Contains(raw, "#") && (query || !strings.Contains(raw, "?"))
}

// checkBatching fills in d's batching from its kind's defaults and refuses
// what is out of its kind's bounds.
func checkBatching(d Draft, add func(pointer, code, detail string, params map[string]any)) Draft {
	if !slices.Contains(Kinds, d.Kind) {
		return d
	}
	b := batchingOf(d.Kind)
	if d.BatchSize == 0 {
		d.BatchSize = b.size
	}
	if d.FlushInterval == 0 {
		d.FlushInterval = b.flush
	}
	maxSize := b.maxSize
	if maxSize == 0 {
		maxSize = int(^uint32(0) >> 1)
	}
	if d.BatchSize < 1 || d.BatchSize > maxSize {
		params := map[string]any{"min": 1}
		if b.maxSize > 0 {
			params["max"] = b.maxSize
		}
		add("/batch_size", "out_of_range", "a batch holds at least one event and at most the kind allows", params)
	}
	minFlush, maxFlush := max(b.minFlush, time.Second), b.maxFlush
	if d.FlushInterval < minFlush || (maxFlush > 0 && d.FlushInterval > maxFlush) {
		params := map[string]any{"min": int(minFlush / time.Second)}
		if maxFlush > 0 {
			params["max"] = int(maxFlush / time.Second)
		}
		add("/flush_interval", "out_of_range", "a flush interval is in whole seconds, within the kind's bounds", params)
	}
	return d
}

// With returns in with change applied, or the refusal of an edit that names
// another kind (kind_immutable) or enables the sink, which is an operation of
// its own because it probes the target first. The change is not checked: the
// caller runs Check on the result.
func (in Input) With(change Change) (Input, error) {
	in.Config = in.Config.Clone()
	if change.Kind != nil && *change.Kind != in.Kind {
		return in, errKindImmutable
	}
	if change.Config != nil {
		if !change.Config.Is(in.Kind) {
			return in, errKindImmutable
		}
		in.Config = change.Config.Clone()
	}
	if change.Name != nil {
		in.Name = *change.Name
	}
	if change.EventKinds != nil {
		in.EventKinds = *change.EventKinds
	}
	if change.Environments != nil {
		in.Environments = *change.Environments
	}
	if change.BatchSize != nil {
		in.BatchSize = *change.BatchSize
	}
	if change.FlushInterval != nil {
		in.FlushInterval = *change.FlushInterval
	}
	if change.Enabled != nil {
		if *change.Enabled && !in.Enabled {
			return in, errUseEnable
		}
		in.Enabled = *change.Enabled
	}
	return in, nil
}

var (
	errKindImmutable = apperrors.Invalid("kind_immutable",
		"a sink's kind never changes; create a sink of the other kind instead")
	errUseEnable = apperrors.Invalid("invalid_sink", "the sink is not valid", apperrors.Violation{
		Pointer: "/enabled", Code: "use_enable",
		Detail: "a sink is enabled by an operation of its own, which checks its target first; an edit only disables",
	})
)

// LinkLocal reports whether a is a link-local address, where cloud metadata
// services answer: a ClickHouse or object storage sink may reach loopback and
// private addresses, in a cluster's own network, and never these.
func LinkLocal(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast()
}

// CheckNotLinkLocal refuses a target any of whose addresses is link-local
// (link_local_target), naming pointer. No setting lifts it.
func CheckNotLinkLocal(addrs []netip.Addr, pointer string) error {
	for _, a := range addrs {
		if LinkLocal(a) {
			return apperrors.Invalid("link_local_target",
				"the target resolves to a link-local address, where cloud metadata services answer",
				apperrors.Violation{Pointer: pointer, Code: "link_local_target", Detail: "the target is a link-local address"})
		}
	}
	return nil
}

// TargetPointer returns where, in a sink as the API writes it, the target
// URL of a sink of kind k stands.
func TargetPointer(k Kind) string {
	switch k {
	case KindClickHouse:
		return "/clickhouse/url"
	case KindObjectStorage:
		return "/object_storage/endpoint"
	default:
		return "/webhook/url"
	}
}

// pointerToken escapes s as a segment of a JSON Pointer (RFC 6901).
func pointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// headerName reports whether s is an HTTP field name: a token.
func headerName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// headerValue reports whether s is an HTTP field value that cannot end the
// line it stands on.
func headerValue(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

// sortedKeys returns the keys of m in order, so violations come in one.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// distinct returns values without repeats, the first of each kept in place.
func distinct[T comparable](values []T) []T {
	out := make([]T, 0, len(values))
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
