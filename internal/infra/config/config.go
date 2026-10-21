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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"

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
	ConfigFile              string
	ListenAddr              string
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
	HTTPMaxBodyBytes      int64
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
			Description: "PostgreSQL connection string, as a URL or as keyword/value pairs. PostgreSQL 16 or newer.",
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
				"even when the proxy rewrites `Host`. Invitation links start with it. When unset, the scheme and origin of each request are used.",
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
			apply: func(cfg *Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 {
					return errors.New("not a whole number of at least 1")
				}
				cfg.PasswordHashConcurrency = n
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_CONFIG_FILE",
			Description: "YAML file of settings, keyed by the names in the File key column. Environment only.",
			Example:     "/etc/flagnetic/flagnetic.yaml",
			apply: func(cfg *Config, v string) error {
				cfg.ConfigFile = v
				return nil
			},
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
			Default:     ":9090",
			Description: "Address of the metrics listener, which serves Prometheus metrics on `/metrics` and nothing else. Keep it off the internet. Set it empty to switch metrics off and open no port.",
			Example:     "127.0.0.1:9090",
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
			apply: func(cfg *Config, v string) error {
				d, err := time.ParseDuration(v)
				if err != nil || d < 0 {
					return errors.New("not a non-negative duration")
				}
				cfg.ShutdownDrain = d
				return nil
			},
		},
		{
			Name:        "FLAGNETIC_SHUTDOWN_GRACE",
			Key:         "shutdown_grace",
			Default:     "15s",
			Unit:        "duration",
			Description: "How long in-flight requests may run once the drain is over and the replica stops accepting new ones; then they are cancelled.",
			Example:     "15s",
			apply: func(cfg *Config, v string) error {
				d, err := time.ParseDuration(v)
				if err != nil || d < 0 {
					return errors.New("not a non-negative duration")
				}
				cfg.ShutdownGrace = d
				return nil
			},
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
			apply:       durationSetting(func(c *Config, d time.Duration) { c.HTTPReadHeaderTimeout = d }),
		},
		{
			Name:        "FLAGNETIC_HTTP_IDLE_TIMEOUT",
			Key:         "http_idle_timeout",
			Default:     "120s",
			Unit:        "duration",
			Description: "How long a keep-alive connection may stay idle before the server closes it.",
			Example:     "120s",
			apply:       durationSetting(func(c *Config, d time.Duration) { c.HTTPIdleTimeout = d }),
		},
		{
			Name:        "FLAGNETIC_HTTP_BODY_READ_TIMEOUT",
			Key:         "http_body_read_timeout",
			Default:     "30s",
			Unit:        "duration",
			Description: "How long a client may take to send a request's body; a body still arriving then is answered 408. Long-lived streams are excepted.",
			Example:     "30s",
			apply:       durationSetting(func(c *Config, d time.Duration) { c.HTTPBodyReadTimeout = d }),
		},
		{
			Name:        "FLAGNETIC_HTTP_MAX_BODY_BYTES",
			Key:         "http_max_body_bytes",
			Default:     "1048576",
			Unit:        "bytes",
			Description: "Largest request body accepted, unless a route declares a larger limit of its own; a larger body is answered 413.",
			Example:     "1048576",
			apply: func(cfg *Config, v string) error {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 1 {
					return errors.New("not a whole number of bytes of at least 1")
				}
				cfg.HTTPMaxBodyBytes = n
				return nil
			},
		},
	}
}

// durationSetting parses a positive duration into the field set picks.
func durationSetting(set func(*Config, time.Duration)) func(*Config, string) error {
	return func(cfg *Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return errors.New("not a positive duration")
		}
		set(cfg, d)
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
	if path, _ := lookupEnv("FLAGNETIC_CONFIG_FILE"); path != "" {
		var err error
		if file, err = readFile(path, settings); err != nil {
			return Config{}, err
		}
	}

	var cfg Config
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
			if s.Secret {
				shown = "the value"
			}
			return Config{}, fmt.Errorf("%s: %s is %w; for example %s", s.Name, shown, err, s.Example)
		}
	}
	return cfg, nil
}

// readFile reads a configuration file: a YAML mapping from file keys to
// scalars. An unknown key is refused, so a typo never silently leaves a
// default in place.
func readFile(path string, settings []Setting) (map[string]string, error) {
	src, err := os.ReadFile(path) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, fmt.Errorf("FLAGNETIC_CONFIG_FILE: %w", err)
	}
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(src)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) { // an empty file sets nothing
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: want a mapping of settings at the top level", path)
	}
	known := make(map[string]bool, len(settings))
	for _, s := range settings {
		if s.Key != "" {
			known[s.Key] = true
		}
	}
	values := make(map[string]string, len(root.Content)/2)
	seen := make(map[string]bool, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if !known[k.Value] {
			return nil, fmt.Errorf("%s: line %d: unknown setting %q", path, k.Line, k.Value)
		}
		if seen[k.Value] {
			return nil, fmt.Errorf("%s: line %d: %s is set twice", path, k.Line, k.Value)
		}
		seen[k.Value] = true
		if v.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("%s: line %d: %s must be a single value", path, k.Line, k.Value)
		}
		if v.Tag != "!!null" {
			values[k.Value] = v.Value
		}
	}
	return values, nil
}

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
