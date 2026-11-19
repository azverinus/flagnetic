package config_test

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
)

var update = flag.Bool("update", false, "rewrite docs/configuration.md from the settings table")

const referencePath = "../../../docs/configuration.md"

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func TestTheReferenceIsTheSettingsTable(t *testing.T) {
	want := config.Reference()
	if *update {
		if err := os.WriteFile(referencePath, want, 0o644); err != nil { //nolint:gosec // a document for everyone to read
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("docs/configuration.md differs from the settings table; run `task generate`")
	}
}

func TestEverySettingIsDocumentedAndHasAValidExample(t *testing.T) {
	for _, s := range config.Settings() {
		if s.Description == "" || s.Example == "" {
			t.Errorf("%s has no description or example", s.Name)
		}
		vars := map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic", s.Name: s.Example}
		if s.Name == "FLAGNETIC_CONFIG_FILE" {
			continue // its example names a file that does not exist here
		}
		if s.Name == "FLAGNETIC_TEST_CLOCK_FILE" {
			// Accepted only where the end-to-end harness runs.
			vars["FLAGNETIC_TEST_HARNESS"], vars["FLAGNETIC_LISTEN_ADDR"] = config.TestHarness, "127.0.0.1:0"
		}
		if _, err := config.Load(env(vars)); err != nil {
			t.Errorf("%s=%s, its own example, is refused: %v", s.Name, s.Example, err)
		}
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || !cfg.AutoMigrate || cfg.ShutdownGrace != 15*time.Second || cfg.ShutdownDrain != 5*time.Second ||
		cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != config.LogJSON || cfg.DBMaxConns != 0 {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://localhost/flagnetic" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL.Reveal())
	}
}

func TestValuesAreParsed(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":             "postgres://localhost/flagnetic",
		"FLAGNETIC_LISTEN_ADDR":    "127.0.0.1:9000",
		"FLAGNETIC_AUTO_MIGRATE":   "false",
		"FLAGNETIC_DB_MAX_CONNS":   "25",
		"FLAGNETIC_SHUTDOWN_GRACE": "500ms",
		"FLAGNETIC_LOG_LEVEL":      "debug",
		"FLAGNETIC_LOG_FORMAT":     "text",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:9000" || cfg.AutoMigrate || cfg.DBMaxConns != 25 ||
		cfg.ShutdownGrace != 500*time.Millisecond || cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != config.LogText {
		t.Errorf("config = %+v", cfg)
	}
}

func TestMalformedValuesAreRefusedNamingTheVariableAndAnExample(t *testing.T) {
	for _, tt := range []struct{ name, value, example string }{
		{"DATABASE_URL", "postgres://localhost/flagnetic?pool_max_conns=many", "postgres://"},
		{"FLAGNETIC_LISTEN_ADDR", "8080", ":8080"},
		{"FLAGNETIC_AUTO_MIGRATE", "maybe", "false"},
		{"FLAGNETIC_DB_MAX_CONNS", "0", "25"},
		{"FLAGNETIC_DB_MAX_CONNS", "ten", "25"},
		{"FLAGNETIC_SHUTDOWN_GRACE", "15", "15s"},
		{"FLAGNETIC_SHUTDOWN_GRACE", "-1s", "15s"},
		{"FLAGNETIC_SHUTDOWN_DRAIN", "5", "5s"},
		{"FLAGNETIC_TRUSTED_PROXIES", "10.0.0.0/33", "10.0.0.0/8"},
		{"FLAGNETIC_TRUSTED_PROXIES", "10.0.0.0/8,,192.168.0.0/16", "10.0.0.0/8"},
		{"FLAGNETIC_TRUSTED_PROXIES", "proxy.internal", "10.0.0.0/8"},
		{"FLAGNETIC_TRUSTED_PROXIES", "::ffff:10.0.0.0/64", "10.0.0.0/8"},
		{"FLAGNETIC_HTTP_READ_HEADER_TIMEOUT", "0s", "10s"},
		{"FLAGNETIC_HTTP_IDLE_TIMEOUT", "forever", "120s"},
		{"FLAGNETIC_HTTP_BODY_READ_TIMEOUT", "-1s", "30s"},
		{"FLAGNETIC_HTTP_WRITE_TIMEOUT", "0s", "30s"},
		{"FLAGNETIC_AUDIT_EXPORT_MAX_CONCURRENT", "0", "4"},
		{"FLAGNETIC_HTTP_MAX_BODY_BYTES", "1MB", "1048576"},
		{"FLAGNETIC_LOG_LEVEL", "loud", "debug"},
		{"FLAGNETIC_LOG_FORMAT", "xml", "text"},
	} {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			vars := map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic", tt.name: tt.value}
			_, err := config.Load(env(vars))
			if err == nil {
				t.Fatal("accepted")
			}
			if msg := err.Error(); !strings.Contains(msg, tt.name) || !strings.Contains(msg, "for example "+tt.example) {
				t.Errorf("error %q does not name %s and show an example", msg, tt.name)
			}
		})
	}
}

func TestAMalformedDatabaseURLIsRefusedWithoutRepeatingIt(t *testing.T) {
	_, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://flagnetic:s3cret@db/flagnetic?pool_max_conns=many"}))
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("Load = %v, want a refusal that does not repeat the password", err)
	}
}

func TestTrustedProxiesAreUnmappedPrefixes(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":              "postgres://localhost/flagnetic",
		"FLAGNETIC_TRUSTED_PROXIES": " 10.1.2.3/8, 192.168.1.10 ,::ffff:172.16.0.0/108, fd00::/8",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range cfg.TrustedProxies {
		got = append(got, p.String())
	}
	if want := "10.0.0.0/8 192.168.1.10/32 172.16.0.0/12 fd00::/8"; strings.Join(got, " ") != want {
		t.Errorf("TrustedProxies = %v, want %s", got, want)
	}
}

func TestAnEmptyMetricsAddressSwitchesMetricsOff(t *testing.T) {
	load := func(vars map[string]string) string {
		t.Helper()
		vars["DATABASE_URL"] = "postgres://localhost/flagnetic"
		cfg, err := config.Load(env(vars))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.MetricsListenAddr
	}
	if got := load(map[string]string{}); got != "127.0.0.1:9090" {
		t.Errorf("unset: %q, want 127.0.0.1:9090", got)
	}
	if got := load(map[string]string{"FLAGNETIC_METRICS_LISTEN_ADDR": ""}); got != "" {
		t.Errorf("set empty: %q, want off", got)
	}
	if got := load(map[string]string{"FLAGNETIC_CONFIG_FILE": writeFile(t, "metrics_listen_addr: \"\"\n")}); got != "" {
		t.Errorf("empty in the file: %q, want off", got)
	}
}

func TestHTTPLimitDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPReadHeaderTimeout != 10*time.Second || cfg.HTTPIdleTimeout != 120*time.Second ||
		cfg.HTTPBodyReadTimeout != 30*time.Second || cfg.HTTPWriteTimeout != 30*time.Second || cfg.AuditExportMaxConcurrent != 4 || cfg.HTTPMaxBodyBytes != 1<<20 || cfg.TrustedProxies != nil {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestDatabaseURLIsRequired(t *testing.T) {
	_, err := config.Load(env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Errorf("Load = %v", err)
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "flagnetic.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheFileIsReadAndAVariableWinsOverIt(t *testing.T) {
	file := writeFile(t, "database_url: postgres://file/flagnetic\nlisten_addr: 127.0.0.1:9000\nauto_migrate: false\nlog_level: warn\n")
	cfg, err := config.Load(env(map[string]string{
		"FLAGNETIC_CONFIG_FILE":  file,
		"FLAGNETIC_AUTO_MIGRATE": "true",
		"FLAGNETIC_LOG_LEVEL":    "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://file/flagnetic" || cfg.ListenAddr != "127.0.0.1:9000" {
		t.Errorf("file values not applied: %+v", cfg)
	}
	if !cfg.AutoMigrate {
		t.Error("the variable did not win over the file")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("an empty variable did not win over the file: LogLevel = %v, want the default", cfg.LogLevel)
	}
}

func TestTheFileIsRefusedWhenItCannotBeTrusted(t *testing.T) {
	for _, tt := range []struct{ name, content, want string }{
		{"key that is no setting name", "database_url: postgres://x\nListen-Port: \":9090\"\n", "Listen-Port"},
		{"non-scalar value", "database_url: postgres://x\nlisten_addr: [a, b]\n", "listen_addr"},
		{"environment-only setting", "database_url: postgres://x\nconfig_file: other.yaml\n", "config_file"},
		{"not a mapping", "- database_url\n", "mapping"},
		{"malformed value", "database_url: postgres://x\nshutdown_grace: soon\n", "FLAGNETIC_SHUTDOWN_GRACE"},
		{"not YAML", "database_url: [\n", "flagnetic.yaml"},
		{"key set twice", "database_url: postgres://x\nlog_level: debug\nlog_level: warn\n", "log_level is set twice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(env(map[string]string{"FLAGNETIC_CONFIG_FILE": writeFile(t, tt.content)}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load = %v, want an error naming %q", err, tt.want)
			}
		})
	}
	if _, err := config.Load(env(map[string]string{"FLAGNETIC_CONFIG_FILE": "/does/not/exist.yaml"})); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestAnEmptyFileIsValid(t *testing.T) {
	file := writeFile(t, "")
	if _, err := config.Load(env(map[string]string{"FLAGNETIC_CONFIG_FILE": file, "DATABASE_URL": "postgres://x/y"})); err != nil {
		t.Errorf("Load = %v", err)
	}
}

func TestTheDatabaseURLIsLoggedWithoutItsPassword(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://flagnetic:s3cret@db:5432/flagnetic?sslmode=require"}))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.DatabaseURLForLog()
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "flagnetic@db:5432/flagnetic") {
		t.Errorf("DatabaseURLForLog = %q", got)
	}
	cfg, _ = config.Load(env(map[string]string{"DATABASE_URL": "host=db user=flagnetic password=s3cret"}))
	if strings.Contains(cfg.DatabaseURLForLog(), "s3cret") {
		t.Errorf("DatabaseURLForLog = %q", cfg.DatabaseURLForLog())
	}
}

func TestTheSecretKeyIsReadAndNeverPrinted(t *testing.T) {
	const key = "a secret key nobody may read"
	cfg, err := config.Load(func(k string) (string, bool) {
		v, ok := map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic", "FLAGNETIC_SECRET_KEY": key}[k]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretKey.Reveal() != key {
		t.Errorf("secret key = %q, want it read", cfg.SecretKey.Reveal())
	}
	if s := fmt.Sprintf("%v %+v", cfg, cfg); strings.Contains(s, key) {
		t.Errorf("the configuration printed shows the secret key: %s", s)
	}
}

func TestTheSecretKeyIsOptionalForCommandsOtherThanServe(t *testing.T) {
	cfg, err := config.Load(func(k string) (string, bool) {
		if k == "DATABASE_URL" {
			return "postgres://localhost/flagnetic", true
		}
		return "", false
	})
	if err != nil || !cfg.SecretKey.IsZero() {
		t.Errorf("Load without a secret key = %v, %v; want no error and no key", cfg.SecretKey, err)
	}
}

func TestRateLimitsDefaultToTheirDocumentedValuesAndOffSwitchesOneOff(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic"}))
	if err != nil {
		t.Fatal(err)
	}
	want := config.RateLimits{
		ClientEvaluate: 120, ClientEvents: 60, ClientStreamConnects: 30, ClientConcurrentStreams: 10, ClientStreamsPerReplica: 10000,
		ServerRuleset: 600, ServerEvaluate: 6000, ServerEvents: 600, ServerConcurrentStreams: 1000,
		FailedAuthentications: 30,
	}
	if cfg.RateLimits != want {
		t.Errorf("RateLimits = %+v, want %+v", cfg.RateLimits, want)
	}

	cfg, err = config.Load(env(map[string]string{
		"DATABASE_URL":                                "postgres://localhost/flagnetic",
		"FLAGNETIC_RATE_LIMIT_CLIENT_EVALUATE":        "off",
		"FLAGNETIC_RATE_LIMIT_SERVER_RULESET":         "1200",
		"FLAGNETIC_RATE_LIMIT_FAILED_AUTHENTICATIONS": "OFF",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimits.ClientEvaluate != 0 || cfg.RateLimits.ServerRuleset != 1200 || cfg.RateLimits.FailedAuthentications != 0 ||
		cfg.RateLimits.ClientEvents != 60 {
		t.Errorf("RateLimits = %+v, want client evaluate and failed authentications off, server ruleset 1200", cfg.RateLimits)
	}

	for value, want := range map[string]int{"off": 0, "250": 250} {
		cfg, err = config.Load(env(map[string]string{
			"DATABASE_URL": "postgres://localhost/flagnetic", "FLAGNETIC_RATE_LIMIT_CLIENT_STREAMS_PER_REPLICA": value,
		}))
		if err != nil || cfg.RateLimits.ClientStreamsPerReplica != want {
			t.Errorf("FLAGNETIC_RATE_LIMIT_CLIENT_STREAMS_PER_REPLICA=%s: %d, %v; want %d", value, cfg.RateLimits.ClientStreamsPerReplica, err, want)
		}
	}
}

func TestARateLimitOfZeroOrLessIsRefusedRatherThanSwitchedOff(t *testing.T) {
	for _, value := range []string{"0", "-5", "many", "1.5"} {
		_, err := config.Load(env(map[string]string{
			"DATABASE_URL": "postgres://localhost/flagnetic", "FLAGNETIC_RATE_LIMIT_SERVER_EVENTS": value,
		}))
		if err == nil || !strings.Contains(err.Error(), "FLAGNETIC_RATE_LIMIT_SERVER_EVENTS") {
			t.Errorf("FLAGNETIC_RATE_LIMIT_SERVER_EVENTS=%s: err = %v, want refused naming the variable", value, err)
		}
	}
}

// A bound is accepted at its edge and refused one step beyond, and the
// refusal names the variable and the range.
func TestEveryNumericSettingHasABoundedRange(t *testing.T) {
	for _, tt := range []struct{ name, belowMin, min, max, aboveMax, rng string }{
		{"FLAGNETIC_PASSWORD_HASH_CONCURRENCY", "0", "1", "64", "65", "1 to 64"},
		{"FLAGNETIC_SHUTDOWN_DRAIN", "-1s", "0s", "1h", "1h1s", "0s to 1h"},
		{"FLAGNETIC_SHUTDOWN_GRACE", "-1s", "0s", "1h", "1h1s", "0s to 1h"},
		{"FLAGNETIC_HTTP_READ_HEADER_TIMEOUT", "99ms", "100ms", "24h", "24h1s", "100ms to 24h"},
		{"FLAGNETIC_HTTP_IDLE_TIMEOUT", "99ms", "100ms", "24h", "24h1s", "100ms to 24h"},
		{"FLAGNETIC_HTTP_BODY_READ_TIMEOUT", "99ms", "100ms", "24h", "24h1s", "100ms to 24h"},
		{"FLAGNETIC_HTTP_WRITE_TIMEOUT", "99ms", "100ms", "24h", "24h1s", "100ms to 24h"},
		{"FLAGNETIC_HTTP_MAX_BODY_BYTES", "0", "1", "1073741824", "1073741825", "1 to 1073741824"},
		{"FLAGNETIC_EVENTS_RETENTION_DAYS", "0", "1", "3650", "3651", "1 to 3650"},
		{"FLAGNETIC_EVENTS_RETENTION_DAYS", "0", "1", "3650", "106752", "1 to 3650"},
		{"FLAGNETIC_EVENTS_LOSS_WARNING_HOURS", "0", "1", "87600", "87601", "1 to 87600"},
		{"FLAGNETIC_EVENTS_LOSS_WARNING_HOURS", "0", "1", "87600", "2562048", "1 to 87600"},
		{"FLAGNETIC_EVENTS_PARTITIONS_AHEAD", "0", "1", "31", "32", "1 to 31"},
		{"FLAGNETIC_SDK_INVENTORY_RETENTION_DAYS", "0", "1", "3650", "3651", "1 to 3650"},
		{"FLAGNETIC_SDK_INVENTORY_RETENTION_DAYS", "0", "1", "3650", "999999", "1 to 3650"},
		{"FLAGNETIC_ALIAS_LINKS_LIMIT", "0", "1", "1000000000", "1000000001", "1 to 1000000000"},
		{"FLAGNETIC_FIRST_EXPOSURES_LIMIT", "0", "1", "1000000000", "1000000001", "1 to 1000000000"},
		{"FLAGNETIC_WEBHOOK_TICK", "1ns", "100ms", "1h", "1h1s", "100ms to 1h"},
		{"FLAGNETIC_SINK_TICK", "1ns", "100ms", "1h", "1h1s", "100ms to 1h"},
		{"FLAGNETIC_SCHEDULE_TICK", "1ns", "100ms", "1h", "1h1s", "100ms to 1h"},
		{"FLAGNETIC_SINK_LAG_WARNING", "999ms", "1s", "720h", "720h1s", "1s to 720h"},
		{"FLAGNETIC_SINK_REJECTION_ATTEMPTS", "0", "1", "1000", "1001", "1 to 1000"},
		{"FLAGNETIC_SINK_STOP_AFTER_DEAD_LETTERS", "0", "1", "1000", "1001", "1 to 1000"},
		{"FLAGNETIC_WEBHOOK_SECRET_OVERLAP", "999ms", "1s", "8760h", "8760h1s", "1s to 8760h"},
		{"FLAGNETIC_SCHEDULE_MIN_LEAD", "999ms", "1s", "720h", "720h1s", "1s to 720h"},
		{"FLAGNETIC_SCHEDULE_MAX_FAILURES", "0", "1", "1000", "1001", "1 to 1000"},
		{"FLAGNETIC_AUDIT_EXPORT_MAX_ENTRIES", "0", "1", "100000000", "100000001", "1 to 100000000"},
		{"FLAGNETIC_AUDIT_EXPORT_MAX_CONCURRENT", "0", "1", "1000", "1001", "1 to 1000"},
		{"FLAGNETIC_RATE_LIMIT_CLIENT_EVALUATE", "0", "1", "10000000", "10000001", "1 to 10000000"},
		{"FLAGNETIC_RATE_LIMIT_CLIENT_STREAMS_PER_REPLICA", "0", "1", "10000000", "10000001", "1 to 10000000"},
		{"FLAGNETIC_RATE_LIMIT_SERVER_EVENTS", "0", "1", "10000000", "10000001", "1 to 10000000"},
	} {
		t.Run(tt.name+"="+tt.aboveMax, func(t *testing.T) {
			load := func(value string) error {
				_, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/flagnetic", tt.name: value}))
				return err
			}
			for _, ok := range []string{tt.min, tt.max} {
				if err := load(ok); err != nil {
					t.Errorf("%s=%s: %v, want it accepted", tt.name, ok, err)
				}
			}
			for _, bad := range []string{tt.belowMin, tt.aboveMax} {
				err := load(bad)
				if err == nil {
					t.Errorf("%s=%s accepted, want it refused", tt.name, bad)
					continue
				}
				if msg := err.Error(); !strings.Contains(msg, tt.name) || !strings.Contains(msg, tt.rng) {
					t.Errorf("%s=%s: error %q does not name the variable and the range %q", tt.name, bad, msg, tt.rng)
				}
			}
		})
	}
}

func TestTheReferenceStatesEachRange(t *testing.T) {
	ref := string(config.Reference())
	for _, s := range config.Settings() {
		if s.Range != "" && !strings.Contains(ref, "Range: "+s.Range+".") {
			t.Errorf("the reference does not state the range of %s: %s", s.Name, s.Range)
		}
	}
}

func TestTheMaximumRetentionStaysWithinADuration(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":                           "postgres://localhost/flagnetic",
		"FLAGNETIC_EVENTS_RETENTION_DAYS":        "3650",
		"FLAGNETIC_SDK_INVENTORY_RETENTION_DAYS": "3650",
		"FLAGNETIC_EVENTS_LOSS_WARNING_HOURS":    "87600",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string]time.Duration{
		"events retention":        time.Duration(cfg.EventsRetentionDays) * 24 * time.Hour,
		"SDK inventory retention": time.Duration(cfg.SDKInventoryRetentionDays) * 24 * time.Hour,
		"loss warning":            time.Duration(cfg.EventsLossWarningHours) * time.Hour,
	} {
		if d <= 0 {
			t.Errorf("the maximum %s is %v, want a positive duration", name, d)
		}
	}
}

func TestAFileKeyThisVersionDoesNotKnowIsReportedNotRefused(t *testing.T) {
	file := writeFile(t, "database_url: postgres://x/y\nlisten_port: \":9090\"\nfuture_setting: 3\n")
	cfg, err := config.Load(env(map[string]string{"FLAGNETIC_CONFIG_FILE": file}))
	if err != nil {
		t.Fatalf("Load = %v, want a binary rolled back to start with a file that has newer keys", err)
	}
	if got := strings.Join(cfg.IgnoredFileKeys, ","); got != "listen_port,future_setting" {
		t.Errorf("IgnoredFileKeys = %q, want the unknown keys in file order", got)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want the default: the unknown key sets nothing", cfg.ListenAddr)
	}
}

func TestAnEnvironmentOnlySettingIsRefusedInTheFile(t *testing.T) {
	_, err := config.Load(env(map[string]string{"FLAGNETIC_CONFIG_FILE": writeFile(t, "database_url: postgres://x\ntest_clock_file: /tmp/c\n")}))
	if err == nil || !strings.Contains(err.Error(), "test_clock_file") {
		t.Errorf("Load = %v, want a refusal naming test_clock_file: the environment alone sets it", err)
	}
}

func TestUnknownVariablesAreThoseNeitherSettingsNorTheCommandLines(t *testing.T) {
	got := config.UnknownVariables([]string{
		"PATH=/bin", "FLAGNETIC_LOG_LEVL=debug", "FLAGNETIC_LOG_LEVEL=debug", "DATABASE_URL=postgres://x",
		"FLAGNETIC_ADMIN_PASSWORD=x", "FLAGNETIC_URL=http://x", "FLAGNETIC_TOKEN=t", "FLAGNETIC_TEST_HARNESS=playwright",
		"FLAGNETIC_TRUSTED_PROXY=10.0.0.0/8", "FLAGNETIC_EMPTY",
	})
	if want := "FLAGNETIC_EMPTY,FLAGNETIC_LOG_LEVL,FLAGNETIC_TRUSTED_PROXY"; strings.Join(got, ",") != want {
		t.Errorf("UnknownVariables = %v, want %s, sorted", got, want)
	}
}

func TestEveryVariableTheCommandLineReadsIsDocumented(t *testing.T) {
	ref := string(config.Reference())
	for _, v := range config.CommandLineVariables() {
		if !strings.Contains(ref, "`"+v.Name+"`") {
			t.Errorf("the reference does not name %s", v.Name)
		}
	}
}

func TestARefusedBaseURLDoesNotRepeatItsPassword(t *testing.T) {
	_, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x/y", "FLAGNETIC_BASE_URL": "https://admin:pass@flags.example.com"}))
	if err == nil || !strings.Contains(err.Error(), "FLAGNETIC_BASE_URL") || strings.Contains(err.Error(), "pass") ||
		!strings.Contains(err.Error(), "flags.example.com") {
		t.Errorf("Load = %v, want a refusal naming the variable and the host, without the userinfo", err)
	}
}

func TestTheBaseURLIsRefusedUnlessItIsAnOrigin(t *testing.T) {
	for _, value := range []string{
		"flags.example.com", "ftp://flags.example.com", "https://", "https://flags.example.com/console",
		"https://flags.example.com?x=1", "https://flags.example.com#top", "https://u@flags.example.com",
	} {
		_, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x/y", "FLAGNETIC_BASE_URL": value}))
		if err == nil || !strings.Contains(err.Error(), "FLAGNETIC_BASE_URL") {
			t.Errorf("FLAGNETIC_BASE_URL=%s: %v, want a refusal naming the variable", value, err)
		}
	}
}

func TestTheBaseURLIsNormalisedToItsOrigin(t *testing.T) {
	for value, want := range map[string]string{
		"https://flags.example.com":      "https://flags.example.com",
		"https://flags.example.com/":     "https://flags.example.com",
		"http://localhost:8080/":         "http://localhost:8080",
		"HTTPS://Flags.Example.com:8443": "https://Flags.Example.com:8443",
	} {
		cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x/y", "FLAGNETIC_BASE_URL": value}))
		if err != nil || cfg.BaseURL == nil || cfg.BaseURL.String() != want {
			t.Errorf("FLAGNETIC_BASE_URL=%s: %v, %v; want %s", value, cfg.BaseURL, err, want)
		}
	}
}

func TestMoreMalformedValuesAreRefusedNamingTheVariable(t *testing.T) {
	for _, tt := range []struct{ name, value string }{
		{"FLAGNETIC_DEFAULT_LOCALE", "fr"},
		{"FLAGNETIC_PASSWORD_HASH_CONCURRENCY", "lots"},
		{"FLAGNETIC_DIGEST_HOUR", "24"},
		{"FLAGNETIC_DIGEST_HOUR", "-1"},
		{"FLAGNETIC_DIGEST_WEEKDAY", "someday"},
		{"FLAGNETIC_INSECURE_COOKIES", "sometimes"},
		{"FLAGNETIC_WEBHOOK_ALLOW_HTTP", "maybe"},
		{"FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS", "maybe"},
		{"FLAGNETIC_EVENTS_RETENTION_DAYS", "forever"},
		{"FLAGNETIC_EVENTS_LOSS_WARNING_HOURS", "-3"},
		{"FLAGNETIC_EVENTS_PARTITIONS_AHEAD", "1.5"},
		{"FLAGNETIC_SINK_TICK", "fast"},
		{"FLAGNETIC_SINK_LAG_WARNING", "15"},
		{"FLAGNETIC_SCHEDULE_TICK", "10"},
		{"FLAGNETIC_SCHEDULE_MIN_LEAD", "soon"},
	} {
		_, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x/y", tt.name: tt.value}))
		if err == nil || !strings.Contains(err.Error(), tt.name) {
			t.Errorf("%s=%s: %v, want a refusal naming the variable", tt.name, tt.value, err)
		}
	}
}

func TestTheDigestSlotAndTheBooleansAreRead(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":                             "postgres://x/y",
		"FLAGNETIC_DIGEST_WEEKDAY":                 "Friday",
		"FLAGNETIC_DIGEST_HOUR":                    "23",
		"FLAGNETIC_INSECURE_COOKIES":               "true",
		"FLAGNETIC_WEBHOOK_ALLOW_HTTP":             "true",
		"FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS": "true",
		"FLAGNETIC_DEFAULT_LOCALE":                 "ru",
		"FLAGNETIC_PASSWORD_HASH_CONCURRENCY":      "8",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DigestWeekday != time.Friday || cfg.DigestHour != 23 || !cfg.InsecureCookies || !cfg.WebhookAllowHTTP ||
		!cfg.WebhookAllowPrivateNetworks || cfg.DefaultLocale != "ru" || cfg.PasswordHashConcurrency != 8 {
		t.Errorf("config = %+v", cfg)
	}
}
