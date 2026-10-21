package cli_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

var tokenValue = regexp.MustCompile(`^fn_pat_[0-9A-Za-z]{40}$`)

func bootstrap(env map[string]string, args ...string) result {
	return run(env, nil, append([]string{"admin", "bootstrap"}, args...)...)
}

func meStatus(t *testing.T, baseURL, token string) int {
	t.Helper()
	c, err := management.NewClientWithResponses(baseURL+management.BaseURL, management.WithRequestEditorFn(
		func(_ context.Context, r *http.Request) error {
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode()
}

func TestServeMigratesThenBootstrappedAdminTokenOpensTheManagementAPI(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	r := serve(t, map[string]string{"DATABASE_URL": url})

	b := bootstrap(map[string]string{"DATABASE_URL": url}, "--email", "first@example.com")
	if b.code != 0 {
		t.Fatalf("bootstrap: exit code = %d; stderr:\n%s", b.code, b.stderr)
	}
	token := strings.TrimSpace(b.stdout)
	if !tokenValue.MatchString(token) {
		t.Fatalf("stdout = %q, want the token alone", b.stdout)
	}
	if got := meStatus(t, r.baseURL, token); got != http.StatusOK {
		t.Errorf("GET /me with the token = %d, want 200", got)
	}
	if got := meStatus(t, r.baseURL, ""); got != http.StatusUnauthorized {
		t.Errorf("GET /me without a token = %d, want 401", got)
	}
}

func TestAdminBootstrapRefusesASecondInstanceAdmin(t *testing.T) {
	env := map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)}
	if r := migrate(env["DATABASE_URL"]); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	if r := bootstrap(env, "--email", "first@example.com"); r.code != 0 {
		t.Fatalf("first bootstrap: exit code = %d; stderr:\n%s", r.code, r.stderr)
	}
	r := bootstrap(env, "--email", "second@example.com")
	if r.code != 1 || r.stdout != "" {
		t.Errorf("second bootstrap: exit code = %d, stdout = %q; want 1 and nothing", r.code, r.stdout)
	}
	if !strings.Contains(r.stderr, "instance admin already exists") {
		t.Errorf("stderr does not say an instance admin exists:\n%s", r.stderr)
	}
}

func TestAdminBootstrapIfAbsentWritesTheTokenFileOnce(t *testing.T) {
	env := map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_ADMIN_PASSWORD": "a long enough password"}
	if r := migrate(env["DATABASE_URL"]); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	file := filepath.Join(t.TempDir(), "token")
	r := bootstrap(env, "--email", "first@example.com", "--token-file", file, "--if-absent")
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("first run: exit code = %d, stdout = %q; want 0 and the token in the file only; stderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "Password:") {
		t.Errorf("a password was printed although FLAGNETIC_ADMIN_PASSWORD gives it:\n%s", r.stderr)
	}
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !tokenValue.MatchString(strings.TrimSpace(string(written))) {
		t.Errorf("token file = %q, want the token", written)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}

	r = bootstrap(env, "--email", "second@example.com", "--token-file", file, "--if-absent")
	if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "nothing done") {
		t.Errorf("second run: exit code = %d, stdout = %q, stderr:\n%s\nwant 0, nothing, and nothing done", r.code, r.stdout, r.stderr)
	}
	if again, _ := os.ReadFile(file); string(again) != string(written) {
		t.Error("the second run changed the token file")
	}
}

func TestAdminBootstrapCreatesNothingWhenItCannotCreateTheTokenFile(t *testing.T) {
	env := map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_ADMIN_PASSWORD": "a long enough password"}
	if r := migrate(env["DATABASE_URL"]); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{
		"a file that exists":       existing,
		"a directory that doesn't": filepath.Join(dir, "missing", "token"),
	} {
		r := bootstrap(env, "--email", "first@example.com", "--token-file", file)
		if r.code != 1 || r.stdout != "" || strings.Contains(r.stderr, "fn_pat_") || !strings.Contains(r.stderr, file) {
			t.Errorf("%s: exit code = %d, stdout = %q, stderr:\n%s\nwant 1, the file named and the token nowhere", name, r.code, r.stdout, r.stderr)
		}
	}
	if kept, _ := os.ReadFile(existing); string(kept) != "kept\n" {
		t.Errorf("the existing file was changed: %q", kept)
	}
	if r := bootstrap(env, "--email", "first@example.com"); r.code != 0 {
		t.Errorf("a bootstrap after the refusals: exit code = %d, want 0 since nothing was created; stderr:\n%s", r.code, r.stderr)
	}
}

func TestAdminBootstrapPrintsAGeneratedPasswordOnce(t *testing.T) {
	env := map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)}
	if r := migrate(env["DATABASE_URL"]); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	r := bootstrap(env, "--email", "first@example.com", "--name", "First Admin")
	if r.code != 0 {
		t.Fatalf("exit code = %d; stderr:\n%s", r.code, r.stderr)
	}
	if !regexp.MustCompile(`(?m)^Password: [0-9A-Za-z]{24}$`).MatchString(r.stderr) {
		t.Errorf("stderr does not show a generated password:\n%s", r.stderr)
	}
	if strings.Contains(r.stdout, "Password") {
		t.Errorf("the password reached stdout: %q", r.stdout)
	}
}

func TestAdminBootstrapRefusesAWrongCommandLine(t *testing.T) {
	env := map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)}
	for name, args := range map[string][]string{
		"no email":        {"admin", "bootstrap"},
		"unknown flag":    {"admin", "bootstrap", "--email", "a@example.com", "--admin"},
		"stray argument":  {"admin", "bootstrap", "--email", "a@example.com", "extra"},
		"invalid email":   {"admin", "bootstrap", "--email", "not-an-email"},
		"unknown command": {"admin", "unlock"},
		"no command":      {"admin"},
	} {
		if name == "invalid email" {
			if r := migrate(env["DATABASE_URL"]); r.code != 0 {
				t.Fatalf("migrate: %s", r.stderr)
			}
		}
		if r := run(env, nil, args...); r.code != 2 || r.stdout != "" {
			t.Errorf("%s: exit code = %d, stdout = %q; want 2 and nothing", name, r.code, r.stdout)
		}
	}
	if r := run(nil, nil, "admin", "help"); r.code != 0 || !strings.Contains(r.stdout, "bootstrap") {
		t.Errorf("admin help: exit code = %d, stdout = %q", r.code, r.stdout)
	}
}

func TestAdminBootstrapRefusesAnUnmigratedDatabase(t *testing.T) {
	r := bootstrap(map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)}, "--email", "first@example.com")
	if r.code != 1 || !strings.Contains(r.stderr, "flagnetic migrate") {
		t.Errorf("exit code = %d, stderr:\n%s\nwant 1 and the advice to migrate", r.code, r.stderr)
	}
}
