package config_test

import (
	"flag"
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
	if cfg.ListenAddr != ":8080" || !cfg.AutoMigrate || cfg.ShutdownGrace != 15*time.Second ||
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
		{"unknown key", "database_url: postgres://x\nlisten_port: \":9090\"\n", "listen_port"},
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
