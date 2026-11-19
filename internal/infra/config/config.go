// Package config is the server's configuration: one table declares every
// setting, and the loader, the keys of the configuration file and the public
// reference (docs/configuration.md) are all derived from it, so the reference
// cannot fall behind what the server reads.
//
// A setting comes from its environment variable when that is set, even to the
// empty string, so one override never needs editing the file; otherwise from
// the YAML file named by FLAGNETIC_CONFIG_FILE; otherwise from its default. An
// empty value means the default. A value that does not parse, an unknown file
// key and a file value that is not a scalar are refused, naming the setting,
// so a bad deploy fails before it takes traffic.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

//go:generate go test -run ^TestTheReferenceIsTheSettingsTable$ -update .

// LogFormat is how log records are written.
type LogFormat string

// The log formats.
const (
	LogJSON LogFormat = "json"
	LogText LogFormat = "text"
)

// ConsoleLocales are the languages the console is translated into.
var ConsoleLocales = []string{"en", "ru"}

// Config is the configuration of one replica.
type Config struct {
	DatabaseURL secret.String
	// SecretKey is the instance's secret key, zero when not set; serve
	// requires it.
	SecretKey secret.String
	// BaseURL is the console's public origin, nil when not set.
	BaseURL *url.URL
	// InsecureCookies lets the session cookie go without Secure off
	// localhost.
	InsecureCookies bool
	// PasswordHashConcurrency bounds the password hashes a replica runs at
	// once.
	PasswordHashConcurrency int
	// DefaultLocale is the console's language when neither the member nor
	// the browser names one it offers.
	DefaultLocale string
	// TestClockFile names the offset file of the test clock, "" outside the
	// end-to-end harness.
	TestClockFile string
	// IgnoredFileKeys are the keys of the configuration file that are no
	// setting of this version, in file order. They are ignored, so that a
	// binary rolled back keeps starting with a file written for a newer one;
	// whoever starts the replica logs them, since a typo is one too.
	IgnoredFileKeys []string
	ListenAddr      string
	// MetricsListenAddr is "" when metrics are switched off.
	MetricsListenAddr string
	AutoMigrate       bool
	// DBMaxConns is the pool size, or 0 when it was not set: then
	// pool_max_conns of DatabaseURL applies, or postgres.DefaultMaxConns.
	DBMaxConns    int
	ShutdownDrain time.Duration
	ShutdownGrace time.Duration
	LogLevel      slog.Level
	LogFormat     LogFormat

	TrustedProxies        []netip.Prefix
	HTTPReadHeaderTimeout time.Duration
	HTTPIdleTimeout       time.Duration
	HTTPBodyReadTimeout   time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPMaxBodyBytes      int64

	// RateLimits are the limits of the SDK API.
	RateLimits RateLimits
	// EventsRetentionDays is how many days the raw events window spans: the window
	// within which an event's time, shifted by the batch's clock skew, is
	// accepted. Event IDs are kept longer, to recognise retries and replays.
	EventsRetentionDays int
	// EventsLossWarningHours is how many hours before retention drops a raw
	// event a sink that has not delivered it is flagged at risk.
	EventsLossWarningHours int
	// EventsPartitionsAhead is how many days after today the raw event
	// tables' partitions are created ahead of time.
	EventsPartitionsAhead int
	// SDKInventoryRetentionDays is how many days a row of the SDK
	// inventory is kept after its SDK was last seen.
	SDKInventoryRetentionDays int
	// AliasLinksLimit is the most alias links an environment keeps.
	AliasLinksLimit int
	// FirstExposuresLimit is the most first exposures an experiment keeps
	// in an environment.
	FirstExposuresLimit int
	// WebhookAllowHTTP accepts plain http webhook targets.
	WebhookAllowHTTP bool
	// WebhookAllowPrivateNetworks accepts webhook targets on loopback,
	// private, link-local, unspecified and multicast addresses.
	WebhookAllowPrivateNetworks bool
	// WebhookTick is how often a replica fans the outbox out to webhook
	// subscriptions and sends the deliveries that are due.
	WebhookTick time.Duration
	// SinkTick is how often a replica looks for sink streams due to flush.
	SinkTick time.Duration
	// SinkLagWarning is how old the oldest undelivered event of a sink may
	// be before the sink is lagging.
	SinkLagWarning time.Duration
	// SinkRejectionAttempts is how many times a target must reject a batch
	// for the sink to set it aside as a dead letter.
	SinkRejectionAttempts int
	// SinkStopAfterDeadLetters is how many dead letters in a row on one
	// stream stop its sink.
	SinkStopAfterDeadLetters int
	// WebhookSecretOverlap is how long the secret a rotation replaced keeps
	// signing beside the new one.
	WebhookSecretOverlap time.Duration
	// ScheduleMinLead is how far ahead a change request's apply time must
	// be at least.
	ScheduleMinLead time.Duration
	// ScheduleTick is the time between the scheduler's rounds.
	ScheduleTick time.Duration
	// ScheduleMaxFailures is how many failed scheduled applications in a
	// row stop the scheduler trying a change request.
	ScheduleMaxFailures int
	// AuditExportMaxEntries is the most entries one download of the audit
	// log may hold.
	AuditExportMaxEntries int
	// AuditExportMaxConcurrent is how many downloads of the audit log one
	// replica serves at once.
	AuditExportMaxConcurrent int
	// DigestWeekday and DigestHour, in UTC, are when the weekly stale-flag
	// digest is sent.
	DigestWeekday time.Weekday
	DigestHour    int
}

// RateLimits are the limits of the SDK API, each counted by every replica on
// its own; 0 is a limit switched off. The per-minute limits count requests
// in fixed one-minute windows, a client key's per key and client IP, a
// server key's per key. A client IP is an IPv4 address, or an IPv6 address
// with the rest of its /64.
type RateLimits struct {
	ClientEvaluate          int
	ClientEvents            int
	ClientStreamConnects    int
	ClientConcurrentStreams int
	// ClientStreamsPerReplica limits the streams all client keys together
	// hold open on the replica, whatever their keys and IPs.
	ClientStreamsPerReplica int
	ServerRuleset           int
	ServerEvaluate          int
	ServerEvents            int
	ServerConcurrentStreams int
	// FailedAuthentications limits the malformed or unknown SDK keys per
	// client IP and minute, checked before a key the replica does not hold
	// is looked up.
	FailedAuthentications int
}

// Setting is one row of the settings table.
type Setting struct {
	// Name is the environment variable.
	Name string
	// Key is the configuration file key, or "" when only the environment may
	// set it.
	Key string
	// Default is the value when the setting is not set, as documented.
	Default string
	// Unit says what a value counts, when it is a quantity.
	Unit string
	// Range is the span of values a numeric setting accepts, as the
	// reference states it, "" when the setting is no quantity.
	Range string
	// Description is the reference's explanation.
	Description string
	// Example is a valid value, shown in the reference and in the refusal of
	// a malformed one.
	Example string
	// Required settings have no default and must be set.
	Required bool
	// RequiredBy names the command that requires the setting, when only
	// that command does; the command checks it, so the others run without.
	RequiredBy string
	// Secret settings are never repeated in a message, not even a refusal.
	Secret bool

	// apply parses a non-empty value into cfg.
	apply func(cfg *Config, value string) error
	// emptyMeansOff marks a setting whose empty value, when set, switches
	// something off rather than taking the default.
	emptyMeansOff bool
	// defaultElsewhere marks a Default that Load leaves unapplied because the
	// code using the setting applies it, after weighing another source.
	defaultElsewhere bool
}

// Settings returns the settings table.
func Settings() []Setting {
	return []Setting{
		{
			Name:        "DATABASE_URL",
			Key:         "database_url",
			Required:    true,
			Secret:      true,
			Description: "PostgreSQL connection string, as a URL or as keyword/value pairs. PostgreSQL 16 or newer. What it leaves out is taken from the standard PostgreSQL variables, such as `PGPASSWORD`, so the password need not be in it.",
			Example:     "postgres://flagnetic@db:5432/flagnetic?sslmode=require",
			apply: func(cfg *Config, v string) error {
				if _, err := pgxpool.ParseConfig(v); err != nil {
					return errors.New("not a PostgreSQL connection string")
				}
				cfg.DatabaseURL = secret.New(v)
				return nil
			},
		},
		{
			Name:       "FLAGNETIC_SECRET_KEY",
			Key:        "secret_key",
			RequiredBy: "serve",
			Secret:     true,
			Description: "The instance's secret key: stored secrets are sealed under it, sessions are signed with it. " +
				"Keep it for the life of the instance: on its first start the instance stores a canary sealed under the key, " +
				"and `serve` refuses to start under a key that does not open it. At least 32 bytes: use a long random value, such as the output of `openssl rand -base64 32`.",
			Example: "q3S0k1p2Rk7Jt0E5m9xV8bYcN4wZ6hL2aU1dG7fH0eI=",
			apply: func(cfg *Config, v string) error {
				cfg.SecretKey = secret.New(v)
				return nil
			},
		},
		{
			Name: "FLAGNETIC_BASE_URL",
			Key:  "base_url",
			Description: "The public URL the console is reached at, such as the address of the reverse proxy in front. " +
				"Its scheme decides whether the session cookie is `Secure`, and its origin is trusted for requests signed in through the console " +
				"even when the proxy rewrites `Host`. Invitation links start with it. When unset, the scheme and origin of each request are used. Auth providers need it: a provider's redirect URI, which the identity provider is told, is built from it, so creating, editing or enabling one is refused with `base_url_required` while it is unset, and so is configuring an [SMTP server](mail.md#configuring-the-smtp-server), since every mailed link is built from it.",
			Example: "https://flags.example.com",
			apply: func(cfg *Config, v string) error {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
					u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
					return errors.New("not an http or https URL of a host, without a path")
				}
				cfg.BaseURL = &url.URL{Scheme: u.Scheme, Host: u.Host}
				return nil
			},
		},
		{
			Name:    "FLAGNETIC_INSECURE_COOKIES",
			Key:     "insecure_cookies",
			Default: "false",
			Description: "Send the session cookie without `Secure` over plain HTTP on a host other than localhost, for a trial without TLS. " +
				"On localhost the cookie works over plain HTTP without it. Never set it where the console is reached over a network you do not trust.",
			Example: "true",
			apply: func(cfg *Config, v string) error {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return errors.New("not true or false")
				}
				cfg.InsecureCookies = b
				return nil
			},
		},
		{
			Name:    "FLAGNETIC_PASSWORD_HASH_CONCURRENCY",
			Key:     "password_hash_concurrency",
			Default: "4",
			Description: "How many passwords one replica hashes or verifies at once. Each takes 64 MiB of memory for about a tenth of a second, " +
				"so the bound keeps a burst of sign-ins from exhausting the replica. A sign-in that waits more than 2 seconds for a slot is answered 503 `busy` with `Retry-After`.",
			Example: "8",
			Range:   hashConcurrency.String(),
			apply:   hashConcurrency.whole(func(c *Config, n int) { c.PasswordHashConcurrency = n }),
		},
		{
			Name:    "FLAGNETIC_DEFAULT_LOCALE",
			Key:     "default_locale",
			Default: "en",
			Description: "The console's language when the member has not chosen one and the browser asks for none the console offers: " +
				"`en` (English) or `ru` (Russian).",
			Example: "ru",
			apply: func(cfg *Config, v string) error {
				if !slices.Contains(ConsoleLocales, v) {
					return errors.New("not a language the console offers")
				}
				cfg.DefaultLocale = v
				return nil
			},
		},
		{
			Name: "FLAGNETIC_TEST_CLOCK_FILE",
			Description: "Test only: a file holding a duration, such as `25h`, that moves the server's clock ahead, read again on every use. " +
				"Accepted only by the end-to-end test harness, listening on loopback; refused anywhere else. Environment only.",
			Example: "/tmp/flagnetic-clock-offset",
			apply: func(cfg *Config, v string) error {
				cfg.TestClockFile = v
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_CONFIG_FILE",
			Description: "YAML file of settings, keyed by the names in the File key column. Environment only.",
			Example:     "/etc/flagnetic/flagnetic.yaml",
			// Load reads the file itself, before any setting.
			apply: func(*Config, string) error { return nil },
		},
		{
			Name:        "FLAGNETIC_LISTEN_ADDR",
			Key:         "listen_addr",
			Default:     ":8080",
			Description: "Address of the public listener: the APIs, the console, `/healthz` and `/readyz`.",
			Example:     ":8080",
			apply: func(cfg *Config, v string) error {
				if err := checkAddr(v); err != nil {
					return err
				}
				cfg.ListenAddr = v
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_METRICS_LISTEN_ADDR",
			Key:         "metrics_listen_addr",
			Default:     "127.0.0.1:9090",
			Description: "Address of the metrics listener, which serves Prometheus metrics on `/metrics` and nothing else. It listens on this host only by default; set an address on another interface for a scraper elsewhere, and keep it off the internet. Set it empty to switch metrics off and open no port.",
			Example:     "10.0.0.5:9090",
			apply: func(cfg *Config, v string) error {
				if v != "" {
					if err := checkAddr(v); err != nil {
						return err
					}
				}
				cfg.MetricsListenAddr = v
				return nil
			},
			emptyMeansOff: true,
		},
		{
			Name:        "FLAGNETIC_AUTO_MIGRATE",
			Key:         "auto_migrate",
			Default:     "true",
			Description: "Whether `serve` applies pending migrations on start. Set it to false when migrations run as a job (`flagnetic migrate`).",
			Example:     "false",
			apply: func(cfg *Config, v string) error {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return errors.New("not true or false")
				}
				cfg.AutoMigrate = b
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_DB_MAX_CONNS",
			Key:         "db_max_conns",
			Default:     strconv.Itoa(postgres.DefaultMaxConns),
			Unit:        "connections",
			Range:       "1 to 10000",
			Description: "Size of the PostgreSQL connection pool of one replica. When unset, `pool_max_conns` in `DATABASE_URL` applies if present; when both are set, this wins and a notice is logged.",
			Example:     "25",
			apply: func(cfg *Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 10000 {
					return errors.New("not a whole number from 1 to 10000")
				}
				cfg.DBMaxConns = n
				return nil
			},
			// The pool applies the default, and only when DATABASE_URL has no
			// pool_max_conns of its own.
			defaultElsewhere: true,
		},
		{
			Name:        "FLAGNETIC_SHUTDOWN_DRAIN",
			Key:         "shutdown_drain",
			Default:     "5s",
			Unit:        "duration",
			Description: "How long a replica told to stop keeps serving while `/readyz` answers 503, so load balancers route away from it before it stops accepting requests. `0` skips the drain.",
			Example:     "5s",
			Range:       stopPeriod.String(),
			apply:       stopPeriod.within(func(c *Config, d time.Duration) { c.ShutdownDrain = d }),
		},
		{
			Name:        "FLAGNETIC_SHUTDOWN_GRACE",
			Key:         "shutdown_grace",
			Default:     "15s",
			Unit:        "duration",
			Description: "How long in-flight requests may run once the drain is over and the replica stops accepting new ones; then they are cancelled.",
			Example:     "15s",
			Range:       stopPeriod.String(),
			apply:       stopPeriod.within(func(c *Config, d time.Duration) { c.ShutdownGrace = d }),
		},
		{
			Name:        "FLAGNETIC_LOG_LEVEL",
			Key:         "log_level",
			Default:     "info",
			Description: "Lowest level logged: debug, info, warn or error.",
			Example:     "debug",
			apply: func(cfg *Config, v string) error {
				levels := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
				l, ok := levels[strings.ToLower(v)]
				if !ok {
					return errors.New("not debug, info, warn or error")
				}
				cfg.LogLevel = l
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_LOG_FORMAT",
			Key:         "log_format",
			Default:     "json",
			Description: "Format of the log written to standard error: json, one object per line, or text.",
			Example:     "text",
			apply: func(cfg *Config, v string) error {
				switch f := LogFormat(strings.ToLower(v)); f {
				case LogJSON, LogText:
					cfg.LogFormat = f
					return nil
				}
				return errors.New("not json or text")
			},
		},
		{
			Name:        "FLAGNETIC_TRUSTED_PROXIES",
			Key:         "trusted_proxies",
			Description: "Comma-separated CIDR prefixes or single addresses of the reverse proxies in front of the server. Their `X-Forwarded-For` and `X-Forwarded-Proto` headers are believed, so the client's address and scheme are the real client's; from any other peer the headers are ignored, so a client cannot choose the address it is seen with.",
			Example:     "10.0.0.0/8,192.168.1.10",
			apply: func(cfg *Config, v string) error {
				p, err := parsePrefixes(v)
				if err != nil {
					return fmt.Errorf("not a list of prefixes: %w", err)
				}
				cfg.TrustedProxies = p
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_HTTP_READ_HEADER_TIMEOUT",
			Key:         "http_read_header_timeout",
			Default:     "10s",
			Unit:        "duration",
			Description: "How long a client may take to send a request's headers on the public listener.",
			Example:     "10s",
			Range:       httpTimeout.String(),
			apply:       httpTimeout.within(func(c *Config, d time.Duration) { c.HTTPReadHeaderTimeout = d }),
		},
		{
			Name:        "FLAGNETIC_HTTP_IDLE_TIMEOUT",
			Key:         "http_idle_timeout",
			Default:     "120s",
			Unit:        "duration",
			Description: "How long a keep-alive connection may stay idle before the server closes it.",
			Example:     "120s",
			Range:       httpTimeout.String(),
			apply:       httpTimeout.within(func(c *Config, d time.Duration) { c.HTTPIdleTimeout = d }),
		},
		{
			Name:        "FLAGNETIC_HTTP_BODY_READ_TIMEOUT",
			Key:         "http_body_read_timeout",
			Default:     "30s",
			Unit:        "duration",
			Description: "How long a client may take to send a request's body; a body still arriving then is answered 408. Long-lived streams are excepted.",
			Example:     "30s",
			Range:       httpTimeout.String(),
			apply:       httpTimeout.within(func(c *Config, d time.Duration) { c.HTTPBodyReadTimeout = d }),
		},
		{
			Name:    "FLAGNETIC_HTTP_WRITE_TIMEOUT",
			Key:     "http_write_timeout",
			Default: "30s",
			Unit:    "duration",
			Description: "How long a client may take to accept what the server writes to it: each chunk of a response, or each frame of a stream, " +
				"must be taken within it, or the server drops the connection. It bounds a stalled reader, not the length of a response or a stream.",
			Example: "30s",
			Range:   httpTimeout.String(),
			apply:   httpTimeout.within(func(c *Config, d time.Duration) { c.HTTPWriteTimeout = d }),
		},
		{
			Name:        "FLAGNETIC_HTTP_MAX_BODY_BYTES",
			Key:         "http_max_body_bytes",
			Default:     "1048576",
			Unit:        "bytes",
			Description: "Largest request body accepted, unless a route declares a larger limit of its own; a larger body is answered 413.",
			Example:     "1048576",
			Range:       fmt.Sprintf("1 to %d", maxBodyBytes),
			apply: func(cfg *Config, v string) error {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 1 || n > maxBodyBytes {
					return fmt.Errorf("not a whole number of bytes from 1 to %d", maxBodyBytes)
				}
				cfg.HTTPMaxBodyBytes = n
				return nil
			},
		},
		{
			Name:    "FLAGNETIC_EVENTS_RETENTION_DAYS",
			Key:     "events_retention_days",
			Default: "30",
			Unit:    "days",
			Description: "The window of the raw events SDKs post. An event whose time is older, after the batch's clock-skew shift, is rejected `out_of_window`. " +
				"Raw events are kept as long: each day is dropped once it is wholly older. An event ID is recognised as a retry and the event stored once " +
				"for a year, or the window plus a day when that is longer, so it outlives the raw events.",
			Example: "30",
			Range:   retentionDays.String(),
			apply:   retentionDays.whole(func(c *Config, n int) { c.EventsRetentionDays = n }),
		},
		{
			Name:    "FLAGNETIC_EVENTS_LOSS_WARNING_HOURS",
			Key:     "events_loss_warning_hours",
			Default: "72",
			Unit:    "hours",
			Description: "How many hours before retention drops a raw event a sink that has not delivered it is flagged at risk: " +
				"the sink's `at_risk` is set and `sink.at_risk` is told once, for disabled and stopped sinks too. " +
				"A sink that falls behind by less than the retention window minus this loses nothing. " +
				"A value of the retention window or more flags every sink with an undelivered event.",
			Example: "72",
			Range:   lossWarning.String(),
			apply:   lossWarning.whole(func(c *Config, n int) { c.EventsLossWarningHours = n }),
		},
		{
			Name:    "FLAGNETIC_EVENTS_PARTITIONS_AHEAD",
			Key:     "events_partitions_ahead",
			Default: "3",
			Unit:    "days",
			Description: "How many days after today the raw event tables' daily partitions are created ahead of time, " +
				"so that events received after midnight never wait for one.",
			Example: "3",
			Range:   partitionsAhead.String(),
			apply:   partitionsAhead.whole(func(c *Config, n int) { c.EventsPartitionsAhead = n }),
		},
		{
			Name:    "FLAGNETIC_SDK_INVENTORY_RETENTION_DAYS",
			Key:     "sdk_inventory_retention_days",
			Default: "90",
			Unit:    "days",
			Description: "How long an environment's SDK inventory keeps an SDK after it was last seen. " +
				"An SDK seen again afterwards appears again, with a new first-seen time.",
			Example: "90",
			Range:   retentionDays.String(),
			apply:   retentionDays.whole(func(c *Config, n int) { c.SDKInventoryRetentionDays = n }),
		},
		{
			Name:    "FLAGNETIC_ALIAS_LINKS_LIMIT",
			Key:     "alias_links_limit",
			Default: "5000000",
			Unit:    "links",
			Description: "The most alias links an environment keeps. Links are kept as long as the environment exists, so the limit is what stops " +
				"a client key from growing them without end. Once an environment holds this many, an alias linking a new pair is refused " +
				"`quota_exceeded`, while one for a pair already linked is still accepted. The count is exact for batches one after another " +
				"and may be exceeded by the batches in flight at the moment the limit is reached.",
			Example: "5000000",
			Range:   quota.String(),
			apply:   quota.whole(func(c *Config, n int) { c.AliasLinksLimit = n }),
		},
		{
			Name:    "FLAGNETIC_FIRST_EXPOSURES_LIMIT",
			Key:     "first_exposures_limit",
			Default: "10000000",
			Unit:    "units",
			Description: "The most units whose first exposure an experiment keeps in an environment. Once the rollup has counted this many, " +
				"an exposure of another unit is refused `quota_exceeded`, while one of a unit already counted is still accepted. " +
				"Exposures received before the rollup has counted them are not refused, so the limit is approximate by the rollup's lag.",
			Example: "10000000",
			Range:   quota.String(),
			apply:   quota.whole(func(c *Config, n int) { c.FirstExposuresLimit = n }),
		},
		{
			Name:    "FLAGNETIC_WEBHOOK_ALLOW_HTTP",
			Key:     "webhook_allow_http",
			Default: "false",
			Description: "Accept webhook subscriptions and webhook sinks whose target is plain `http`, which sends events and their signatures unencrypted. " +
				"Without it such a target is refused `insecure_url`. Meant for a receiver on the same host or a trusted network.",
			Example: "true",
			apply:   boolSetting(func(c *Config, b bool) { c.WebhookAllowHTTP = b }),
		},
		{
			Name:    "FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS",
			Key:     "webhook_allow_private_networks",
			Default: "false",
			Description: "Accept webhook targets that resolve to anything but a global unicast address: loopback, private, link-local, " +
				"carrier-grade NAT (100.64.0.0/10), benchmarking, reserved, documentation and other special-purpose ranges, " +
				"and NAT64 or 6to4 addresses that embed one. " +
				"Without it such a target is refused `private_network_target` when a subscription or a webhook sink is written, " +
				"so that whoever manages webhooks or sinks cannot reach the network the instance runs in. " +
				"ClickHouse and object storage sinks may reach private networks regardless, and never a link-local address, " +
				"EC2's IPv6 metadata address `fd00:ec2::254`, or a NAT64 or 6to4 address embedding a link-local one.",
			Example: "true",
			apply:   boolSetting(func(c *Config, b bool) { c.WebhookAllowPrivateNetworks = b }),
		},
		{
			Name:    "FLAGNETIC_WEBHOOK_TICK",
			Key:     "webhook_tick",
			Default: "5s",
			Unit:    "duration",
			Description: "How often a replica turns new events into webhook deliveries and sends the deliveries that are due. " +
				"An event reaches a receiver about this long after its change at the earliest.",
			Example: "5s",
			Range:   workerTick.String(),
			apply:   workerTick.within(func(c *Config, d time.Duration) { c.WebhookTick = d }),
		},
		{
			Name:    "FLAGNETIC_SINK_TICK",
			Key:     "sink_tick",
			Default: "1s",
			Unit:    "duration",
			Description: "How often a replica looks for sink streams that are due and delivers their next batch. " +
				"A stream still waits its sink's `flush_interval` between batches once it has caught up, and goes on at once while batches are full.",
			Example: "1s",
			Range:   workerTick.String(),
			apply:   workerTick.within(func(c *Config, d time.Duration) { c.SinkTick = d }),
		},
		{
			Name:    "FLAGNETIC_SINK_LAG_WARNING",
			Key:     "sink_lag_warning",
			Default: "15m",
			Unit:    "duration",
			Description: "How old the oldest event a sink has not delivered may be before the sink's health is `lagging`. " +
				"A long transaction anywhere in the database holds every sink back, and shows as lag.",
			Example: "15m",
			Range:   longPeriod.String(),
			apply:   longPeriod.within(func(c *Config, d time.Duration) { c.SinkLagWarning = d }),
		},
		{
			Name:    "FLAGNETIC_SINK_REJECTION_ATTEMPTS",
			Key:     "sink_rejection_attempts",
			Default: "5",
			Unit:    "attempts",
			Description: "How many times a sink's target must reject one batch before the sink sets it aside as a dead letter and goes on with the next. " +
				"Only a rejection counts: a target that is down or busy is retried without limit, with a growing wait, and the batch keeps its place.",
			Example: "3",
			Range:   attempts.String(),
			apply:   attempts.whole(func(c *Config, n int) { c.SinkRejectionAttempts = n }),
		},
		{
			Name:    "FLAGNETIC_SINK_STOP_AFTER_DEAD_LETTERS",
			Key:     "sink_stop_after_dead_letters",
			Default: "3",
			Unit:    "dead letters",
			Description: "How many dead letters in a row on one stream stop its sink, so that a target that keeps refusing is not fed the rest of the event store. " +
				"A batch the target acknowledges ends the run. A stopped sink delivers nothing until a member resumes it.",
			Example: "5",
			Range:   attempts.String(),
			apply:   attempts.whole(func(c *Config, n int) { c.SinkStopAfterDeadLetters = n }),
		},
		{
			Name:    "FLAGNETIC_WEBHOOK_SECRET_OVERLAP",
			Key:     "webhook_secret_overlap",
			Default: "24h",
			Unit:    "duration",
			Description: "How long the secret a rotation replaced, a webhook subscription's or a webhook sink's, keeps signing beside the new one, " +
				"so a receiver can take the new secret without refusing a single event.",
			Example: "24h",
			Range:   overlapPeriod.String(),
			apply:   overlapPeriod.within(func(c *Config, d time.Duration) { c.WebhookSecretOverlap = d }),
		},
		{
			Name:    "FLAGNETIC_SCHEDULE_MIN_LEAD",
			Key:     "schedule_min_lead",
			Default: "60s",
			Unit:    "duration",
			Description: "How far ahead a change request's apply time must be at least when it is set; " +
				"a sooner time is refused `invalid_change_request`.",
			Example: "5m",
			Range:   longPeriod.String(),
			apply:   longPeriod.within(func(c *Config, d time.Duration) { c.ScheduleMinLead = d }),
		},
		{
			Name:    "FLAGNETIC_SCHEDULE_TICK",
			Key:     "schedule_tick",
			Default: "10s",
			Unit:    "duration",
			Description: "How often every replica looks for approved change requests whose apply time has come and applies them; " +
				"a change request is applied at most this long after its time, or later when no replica was running.",
			Example: "10s",
			Range:   workerTick.String(),
			apply:   workerTick.within(func(c *Config, d time.Duration) { c.ScheduleTick = d }),
		},
		{
			Name:    "FLAGNETIC_SCHEDULE_MAX_FAILURES",
			Key:     "schedule_max_failures",
			Default: "5",
			Unit:    "attempts",
			Description: "How many scheduled applications of a change request may fail in a row before the scheduler stops trying it " +
				"and records `change_request.schedule_failed`.",
			Example: "5",
			Range:   attempts.String(),
			apply:   attempts.whole(func(c *Config, n int) { c.ScheduleMaxFailures = n }),
		},
		{
			Name:    "FLAGNETIC_AUDIT_EXPORT_MAX_ENTRIES",
			Key:     "audit_export_max_entries",
			Default: "100000",
			Unit:    "entries",
			Description: "The most entries one download of the audit log may hold. A download whose filters match more is refused " +
				"`export_too_large` before anything is sent; narrowing it by time brings it under.",
			Example: "100000",
			Range:   exportEntries.String(),
			apply:   exportEntries.whole(func(c *Config, n int) { c.AuditExportMaxEntries = n }),
		},
		{
			Name:    "FLAGNETIC_AUDIT_EXPORT_MAX_CONCURRENT",
			Key:     "audit_export_max_concurrent",
			Default: "4",
			Unit:    "downloads",
			Description: "How many downloads of the audit log one replica serves at once. A download asked while every slot is taken is refused " +
				"`too_many_exports` (429) with `Retry-After`.",
			Example: "4",
			Range:   exportDownloads.String(),
			apply:   exportDownloads.whole(func(c *Config, n int) { c.AuditExportMaxConcurrent = n }),
		},
		{
			Name:    "FLAGNETIC_DIGEST_WEEKDAY",
			Key:     "digest_weekday",
			Default: "monday",
			Unit:    "weekday",
			Description: "The weekday, in UTC, on which the stale-flag digest (`report.stale_flags`) is sent to each project's webhook subscribers, " +
				"in English, in any case. A digest not sent within the hour of its slot because no replica was running is skipped, not sent late.",
			Example: "friday",
			apply: func(c *Config, v string) error {
				d, ok := governance.ParseDigestWeekday(v)
				if !ok {
					return errors.New("not a weekday: sunday, monday, tuesday, wednesday, thursday, friday or saturday")
				}
				c.DigestWeekday = d
				return nil
			},
		},
		{
			Name:    "FLAGNETIC_DIGEST_HOUR",
			Key:     "digest_hour",
			Default: "8",
			Unit:    "hour, 0 to 23",
			Description: "The hour of the day, in UTC, at which the stale-flag digest is sent on its weekday; " +
				"it is sent at the first minute of that hour that a replica runs.",
			Example: "14",
			apply: func(c *Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 || n > 23 {
					return errors.New("not a whole number from 0 to 23")
				}
				c.DigestHour = n
				return nil
			},
		},
		rateLimit("FLAGNETIC_RATE_LIMIT_CLIENT_EVALUATE", "120", "requests per minute",
			"Remote evaluations a client key may ask of one replica per minute, counted per key and client IP, an IPv6 client by its /64.",
			func(l *RateLimits) *int { return &l.ClientEvaluate }),
		rateLimit("FLAGNETIC_RATE_LIMIT_CLIENT_EVENTS", "60", "requests per minute",
			"Event batches a client key may post to one replica per minute, counted per key and client IP, an IPv6 client by its /64.",
			func(l *RateLimits) *int { return &l.ClientEvents }),
		rateLimit("FLAGNETIC_RATE_LIMIT_CLIENT_STREAM_CONNECTS", "30", "requests per minute",
			"Ruleset streams a client key may open on one replica per minute, counted per key and client IP, an IPv6 client by its /64.",
			func(l *RateLimits) *int { return &l.ClientStreamConnects }),
		rateLimit("FLAGNETIC_RATE_LIMIT_CLIENT_CONCURRENT_STREAMS", "10", "streams",
			"Ruleset streams a client key may hold open on one replica at once, counted per key and client IP, an IPv6 client by its /64.",
			func(l *RateLimits) *int { return &l.ClientConcurrentStreams }),
		rateLimit("FLAGNETIC_RATE_LIMIT_CLIENT_STREAMS_PER_REPLICA", "10000", "streams",
			"Ruleset streams all client keys together may hold open on one replica at once, whatever their keys and client IPs, "+
				"which bounds the connections a public client key can hold the replica to. "+
				"One client key may hold a quarter of them (at least one), and a stream beyond its share is refused with that share as `RateLimit-Limit`.",
			func(l *RateLimits) *int { return &l.ClientStreamsPerReplica }),
		rateLimit("FLAGNETIC_RATE_LIMIT_SERVER_RULESET", "600", "requests per minute",
			"Ruleset downloads a server key may ask of one replica per minute, counted per key.",
			func(l *RateLimits) *int { return &l.ServerRuleset }),
		rateLimit("FLAGNETIC_RATE_LIMIT_SERVER_EVALUATE", "6000", "requests per minute",
			"Remote evaluations a server key may ask of one replica per minute, counted per key.",
			func(l *RateLimits) *int { return &l.ServerEvaluate }),
		rateLimit("FLAGNETIC_RATE_LIMIT_SERVER_EVENTS", "600", "requests per minute",
			"Event batches a server key may post to one replica per minute, counted per key.",
			func(l *RateLimits) *int { return &l.ServerEvents }),
		rateLimit("FLAGNETIC_RATE_LIMIT_SERVER_CONCURRENT_STREAMS", "1000", "streams",
			"Ruleset streams a server key may hold open on one replica at once, counted per key. A server key's stream connects are not limited per minute.",
			func(l *RateLimits) *int { return &l.ServerConcurrentStreams }),
		rateLimit("FLAGNETIC_RATE_LIMIT_FAILED_AUTHENTICATIONS", "30", "refusals per minute",
			"SDK API requests from one client IP (an IPv6 client by its /64) whose key is malformed or unknown that one replica takes per minute. "+
				"Past it, an SDK API request from that IP or /64 is answered 429 before its key is looked up, which keeps guessing keys slow, "+
				"unless the replica already holds its key from an earlier lookup, so the valid keys in use behind a shared address keep working. "+
				"A revoked or expired key, and a request without a key, are not counted.",
			func(l *RateLimits) *int { return &l.FailedAuthentications }),
	}
}

// rateLimit is the setting of one limit of the SDK API, which field picks
// from the limits. A value of off switches the limit off; 0 or less is
// refused, so a typo never silently lifts a limit.
func rateLimit(name, def, unit, description string, field func(*RateLimits) *int) Setting {
	return Setting{
		Name:    name,
		Key:     strings.ToLower(strings.TrimPrefix(name, "FLAGNETIC_")),
		Default: def,
		Unit:    unit,
		Range:   requestRate.String(),
		Description: description + " Each replica counts on its own, so an instance of n replicas lets up to n times as much through. " +
			"A refused request is answered 429 `rate_limited` with `Retry-After`. `off` switches the limit off.",
		Example: def,
		apply: func(cfg *Config, v string) error {
			if strings.EqualFold(v, "off") {
				*field(&cfg.RateLimits) = 0
				return nil
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < requestRate.min || n > requestRate.max {
				return fmt.Errorf("not a whole number from %s, or off", requestRate)
			}
			*field(&cfg.RateLimits) = n
			return nil
		},
	}
}

// intRange is the inclusive range a whole-number setting accepts. Every
// numeric setting has an upper bound: an unbounded value overflows the
// duration it is turned into, or exhausts what it sizes.
type intRange struct{ min, max int }

func (r intRange) String() string { return fmt.Sprintf("%d to %d", r.min, r.max) }

// whole parses a whole number within the range into the field set picks.
func (r intRange) whole(set func(*Config, int)) func(*Config, string) error {
	return func(cfg *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < r.min || n > r.max {
			return fmt.Errorf("not a whole number from %s", r)
		}
		set(cfg, n)
		return nil
	}
}

// durRange is the inclusive range a duration setting accepts.
type durRange struct{ min, max time.Duration }

func (r durRange) String() string { return shortDuration(r.min) + " to " + shortDuration(r.max) }

// shortDuration writes d as 1h, 15m or 100ms rather than 1h0m0s.
func shortDuration(d time.Duration) string {
	out := d.String()
	if strings.HasSuffix(out, "m0s") {
		out = strings.TrimSuffix(out, "0s")
	}
	if strings.HasSuffix(out, "h0m") {
		out = strings.TrimSuffix(out, "0m")
	}
	return out
}

// within parses a duration within the range into the field set picks.
func (r durRange) within(set func(*Config, time.Duration)) func(*Config, string) error {
	return func(cfg *Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil || d < r.min || d > r.max {
			return fmt.Errorf("not a duration from %s", r)
		}
		set(cfg, d)
		return nil
	}
}

// The ranges of the settings. Retention is capped at ten years, and the
// periods are kept to what a time.Duration holds with room to spare.
var (
	retentionDays   = intRange{1, 3650}
	lossWarning     = intRange{1, 87600}
	partitionsAhead = intRange{1, 31}
	hashConcurrency = intRange{1, 64}
	quota           = intRange{1, 1_000_000_000}
	attempts        = intRange{1, 1000}
	exportEntries   = intRange{1, 100_000_000}
	exportDownloads = intRange{1, 1000}
	requestRate     = intRange{1, 10_000_000}
	stopPeriod      = durRange{0, time.Hour}
	httpTimeout     = durRange{100 * time.Millisecond, 24 * time.Hour}
	workerTick      = durRange{100 * time.Millisecond, time.Hour}
	longPeriod      = durRange{time.Second, 720 * time.Hour}
	overlapPeriod   = durRange{time.Second, 8760 * time.Hour}
)

const maxBodyBytes = 1 << 30

// boolSetting parses true or false into the field set picks.
func boolSetting(set func(*Config, bool)) func(*Config, string) error {
	return func(cfg *Config, v string) error {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return errors.New("not true or false")
		}
		set(cfg, b)
		return nil
	}
}

// parsePrefixes reads a comma-separated list of CIDR prefixes and single
// addresses. IPv4-mapped IPv6 forms are unmapped, so a prefix matches a peer
// however its address is written.
func parsePrefixes(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		if strings.Contains(entry, "/") {
			p, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("%q is not a CIDR prefix", entry)
			}
			if p.Addr().Is4In6() {
				// Shorter than 96 bits it reaches outside the IPv4-mapped
				// range, and unmapped it would trust every IPv4 peer.
				if p.Bits() < 96 {
					return nil, fmt.Errorf("%q is shorter than the IPv4-mapped range", entry)
				}
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or a CIDR prefix", entry)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// checkAddr accepts host:port with an empty host or one, and a numeric port.
func checkAddr(v string) error {
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return errors.New("not host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return errors.New("not a port from 0 to 65535")
	}
	return nil
}

// Load reads the configuration through lookupEnv, which behaves like
// os.LookupEnv.
func Load(lookupEnv func(string) (string, bool)) (Config, error) {
	settings := Settings()
	var file map[string]string
	var ignored []string
	if path, _ := lookupEnv("FLAGNETIC_CONFIG_FILE"); path != "" {
		var err error
		if file, ignored, err = readFile(path, settings); err != nil {
			return Config{}, err
		}
	}

	cfg := Config{IgnoredFileKeys: ignored}
	for _, s := range settings {
		value, set := lookupEnv(s.Name)
		if !set && s.Key != "" {
			value, set = file[s.Key]
		}
		if value == "" && set && s.emptyMeansOff {
			_ = s.apply(&cfg, "") // an empty value is always accepted where it means off
			continue
		}
		if value == "" {
			if s.Required {
				return Config{}, fmt.Errorf("%s is required; for example %s", s.Name, s.Example)
			}
			if s.Default == "" || s.defaultElsewhere {
				continue
			}
			value = s.Default
		}
		if err := s.apply(&cfg, value); err != nil {
			shown := strconv.Quote(value)
			switch {
			case s.Secret:
				shown = "the value"
			case s.Name == "FLAGNETIC_BASE_URL":
				shown = strconv.Quote(redactUserinfo(value))
			}
			return Config{}, fmt.Errorf("%s: %s is %w; for example %s", s.Name, shown, err, s.Example)
		}
	}
	if cfg.TestClockFile != "" {
		if err := requireTestHarness(lookupEnv, cfg.ListenAddr); err != nil {
			return Config{}, fmt.Errorf("FLAGNETIC_TEST_CLOCK_FILE: %w", err)
		}
	}
	return cfg, nil
}

// TestHarness is the value of FLAGNETIC_TEST_HARNESS the end-to-end harness
// sets, the one marker under which test-only settings are accepted.
const TestHarness = "playwright"

// requireTestHarness refuses a test-only setting outside the end-to-end
// harness: without its FLAGNETIC_TEST_HARNESS marker, or listening beyond
// loopback, where a deployment would be. The marker is read from the
// environment alone and is no setting of its own, so no configuration file
// can turn it on.
func requireTestHarness(lookupEnv func(string) (string, bool), listenAddr string) error {
	if v, _ := lookupEnv("FLAGNETIC_TEST_HARNESS"); v != TestHarness {
		return fmt.Errorf("a test-only setting, accepted only with FLAGNETIC_TEST_HARNESS=%s", TestHarness)
	}
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("a test-only setting: FLAGNETIC_LISTEN_ADDR %q: %w", listenAddr, err)
	}
	if addr, err := netip.ParseAddr(host); host != "localhost" && (err != nil || !addr.IsLoopback()) {
		return fmt.Errorf("a test-only setting, accepted only while listening on loopback, not %q", listenAddr)
	}
	return nil
}

// fileKey is what a key of the configuration file looks like: the lower-case
// name of a setting.
var fileKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// readFile reads a configuration file: a YAML mapping from file keys to
// scalars. A key that is no setting name is refused, and so is the name of a
// setting that only the environment sets. A well-formed key this version does
// not know is returned, not refused: a binary rolled back after an upgrade
// has to start with the file written for the newer one, and the caller logs
// the keys so that a typo is still noticed.
func readFile(path string, settings []Setting) (values map[string]string, ignored []string, err error) {
	src, err := os.ReadFile(path) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, nil, fmt.Errorf("FLAGNETIC_CONFIG_FILE: %w", err)
	}
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(src)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) { // an empty file sets nothing
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s: want a mapping of settings at the top level", path)
	}
	known, environmentOnly := make(map[string]bool, len(settings)), make(map[string]bool)
	for _, s := range settings {
		if s.Key != "" {
			known[s.Key] = true
		} else {
			environmentOnly[strings.ToLower(strings.TrimPrefix(s.Name, "FLAGNETIC_"))] = true
		}
	}
	values = make(map[string]string, len(root.Content)/2)
	seen := make(map[string]bool, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if !fileKey.MatchString(k.Value) || environmentOnly[k.Value] {
			return nil, nil, fmt.Errorf("%s: line %d: %q is not a key of the file; the settings are in the File key column of the reference", path, k.Line, k.Value)
		}
		if seen[k.Value] {
			return nil, nil, fmt.Errorf("%s: line %d: %s is set twice", path, k.Line, k.Value)
		}
		seen[k.Value] = true
		if !known[k.Value] {
			ignored = append(ignored, k.Value)
			continue
		}
		if v.Kind != yaml.ScalarNode {
			return nil, nil, fmt.Errorf("%s: line %d: %s must be a single value", path, k.Line, k.Value)
		}
		if v.Tag != "!!null" {
			values[k.Value] = v.Value
		}
	}
	return values, ignored, nil
}

// userinfo is the credentials part of a URL, up to the host.
var userinfo = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@`)

// redactUserinfo hides the credentials of a URL, so a refusal that quotes the
// value never repeats a password.
func redactUserinfo(v string) string { return userinfo.ReplaceAllString(v, "${1}[redacted]@") }

var keywordPassword = regexp.MustCompile(`password\s*=\s*('(?:[^'\\]|\\.)*'|\S+)`)

// DatabaseURLForLog returns DATABASE_URL with its password removed, for the
// log of a starting replica.
func (c Config) DatabaseURLForLog() string {
	raw := c.DatabaseURL.Reveal()
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return keywordPassword.ReplaceAllString(raw, "password=[redacted]")
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	q := u.Query()
	if q.Has("password") {
		q.Set("password", "[redacted]")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// CommandLineVariable is an environment variable the command line reads
// outside the settings table: a setting configures the replica, these
// address an instance or tell a command what to create.
type CommandLineVariable struct {
	Name        string
	Description string
}

// CommandLineVariables lists the variables the commands read themselves.
func CommandLineVariables() []CommandLineVariable {
	return []CommandLineVariable{
		{"FLAGNETIC_ADMIN_PASSWORD", "The password of the instance admin that `flagnetic admin bootstrap` and `flagnetic admin create` create. When unset, a password is generated and printed once."},
		{"FLAGNETIC_URL", "The instance that `flagnetic export`, `import` and `import-firebase` talk to, when `--url` is not given."},
		{"FLAGNETIC_TOKEN", "The access token those commands present, when neither `--token` nor `--token-file` is given."},
		{"FLAGNETIC_TEST_HARNESS", "Test only: the marker, set to `" + TestHarness + "` by the end-to-end harness, under which `FLAGNETIC_TEST_CLOCK_FILE` is accepted."},
	}
}

// UnknownVariables returns the names among environ, in the form of
// os.Environ, that start with FLAGNETIC_ but are neither a setting nor a
// variable of the command line: a mistyped setting that would otherwise
// change nothing. They are sorted.
func UnknownVariables(environ []string) []string {
	known := make(map[string]bool)
	for _, s := range Settings() {
		known[s.Name] = true
	}
	for _, v := range CommandLineVariables() {
		known[v.Name] = true
	}
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "FLAGNETIC_") && !known[name] {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
