package cli_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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

// adminPassword is the FLAGNETIC_ADMIN_PASSWORD of the admin command tests.
const adminPassword = "lantern-quartz-meadow-2291"

// signIn signs in to the replica at baseURL, failing the test unless the
// member signs in.
func signIn(t *testing.T, baseURL, email, password string) *testkit.Browser {
	t.Helper()
	b := testkit.NewBrowser(t, baseURL)
	b.SignIn(t, email, password)
	return b
}

// cliAudit returns the audit entries the command line made, as
// "actor action entity-key", read by the instance admin b is signed in as.
func cliAudit(t *testing.T, b *testkit.Browser) []string {
	t.Helper()
	kind := management.AuditActorKind("cli")
	res, err := b.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{ActorKind: &kind})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit: %v", err)
	}
	var out []string
	for _, e := range res.JSON200.Items {
		out = append(out, e.Actor.Name+" "+e.Action+" "+e.Entity.Key)
	}
	return out
}

func TestAdminCreateMakesInstanceAdminsWhoCanSignIn(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	env := map[string]string{"DATABASE_URL": url, "FLAGNETIC_ADMIN_PASSWORD": adminPassword}
	r := run(env, nil, "admin", "create", "--email", "first@example.com", "--name", "First Admin")
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("create with a password: exit code = %d, stdout = %q; want 0 and nothing; stderr:\n%s", r.code, r.stdout, r.stderr)
	}
	generated := run(map[string]string{"DATABASE_URL": url}, nil, "admin", "create", "--email", "second@example.com")
	password := strings.TrimSpace(generated.stdout)
	testkit.RecordSecret(password)
	if generated.code != 0 || !regexp.MustCompile(`^[0-9A-Za-z]{24}$`).MatchString(password) {
		t.Fatalf("create without a password: exit code = %d, stdout = %q; want the generated password alone", generated.code, generated.stdout)
	}
	dup := run(env, nil, "admin", "create", "--email", "FIRST@example.com")
	if dup.code != 1 || dup.stdout != "" || !strings.Contains(dup.stderr, "already") {
		t.Errorf("a second member with the email: exit code = %d, stdout = %q, stderr:\n%s\nwant 1 and already", dup.code, dup.stdout, dup.stderr)
	}

	srv := serve(t, map[string]string{"DATABASE_URL": url})
	first := signIn(t, srv.baseURL, "first@example.com", adminPassword)
	me, err := first.GetMeWithResponse(t.Context())
	if err != nil || me.JSON200 == nil || me.JSON200.Member.DisplayName != "First Admin" || !me.JSON200.Member.InstanceAdmin ||
		me.JSON200.Member.Status != management.MemberStatusActive {
		t.Errorf("GET /me = %+v, %v; want the active instance admin First Admin", me.JSON200, err)
	}
	signIn(t, srv.baseURL, "second@example.com", password)
	audit := cliAudit(t, first)
	for _, want := range []string{"flagnetic admin create member.created first@example.com", "flagnetic admin create member.created second@example.com"} {
		if !slices.Contains(audit, want) {
			t.Errorf("command line audit = %q, want %q", audit, want)
		}
	}
}

func TestAdminCreateRefusesAWeakPassword(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	for password, rule := range map[string]string{
		"short-pw-11":                  "length",
		strings.Repeat("x", 129):       "length",
		strings.Repeat("ж", 129):       "length",
		"Marguerite.Duras":             "email",
		"xx-MARGUERITE.DURAS-password": "email",
		"password1234":                 "common",
		"QWERTYUIOPASDFGHJKL":          "common",
	} {
		r := run(map[string]string{"DATABASE_URL": url, "FLAGNETIC_ADMIN_PASSWORD": password}, nil,
			"admin", "create", "--email", "marguerite.duras@example.com")
		if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "weak_password ("+rule+")") {
			t.Errorf("%.20q: exit code = %d, stdout = %q, stderr:\n%s\nwant 2 naming the %s rule", password, r.code, r.stdout, r.stderr, rule)
		}
	}
	for i, password := range []string{strings.Repeat("ж", 128), strings.Repeat("ж", 12)} {
		r := run(map[string]string{"DATABASE_URL": url, "FLAGNETIC_ADMIN_PASSWORD": password}, nil,
			"admin", "create", "--email", "admin"+strconv.Itoa(i)+"@example.com")
		if r.code != 0 {
			t.Errorf("%d code points: exit code = %d, stderr:\n%s; want accepted", len([]rune(password)), r.code, r.stderr)
		}
	}
}

func TestAdminPromoteMakesAMemberAnInstanceAdmin(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	plain := testkit.NewMember(t, pool, testkit.WithEmail("plain@example.com"))
	srv := serve(t, map[string]string{"DATABASE_URL": url})
	isAdmin := func() bool {
		me, err := signIn(t, srv.baseURL, plain.Email, plain.Password).GetMeWithResponse(t.Context())
		if err != nil || me.JSON200 == nil {
			t.Fatalf("GET /me: %v", err)
		}
		return me.JSON200.Member.InstanceAdmin
	}
	if isAdmin() {
		t.Fatal("the member is an instance admin before being promoted")
	}
	for range 2 {
		if r := run(map[string]string{"DATABASE_URL": url}, nil, "admin", "promote", "--email", "Plain@Example.com"); r.code != 0 || r.stdout != "" {
			t.Fatalf("promote: exit code = %d, stdout = %q; stderr:\n%s", r.code, r.stdout, r.stderr)
		}
	}
	if !isAdmin() {
		t.Error("the member is no instance admin after being promoted")
	}
	if r := run(map[string]string{"DATABASE_URL": url}, nil, "admin", "promote", "--email", "nobody@example.com"); r.code != 1 ||
		!strings.Contains(r.stderr, "no member") {
		t.Errorf("an unknown email: exit code = %d, stderr:\n%s\nwant 1 and no member", r.code, r.stderr)
	}
	audit := cliAudit(t, signIn(t, srv.baseURL, plain.Email, plain.Password))
	if want := []string{"flagnetic admin promote member.promoted plain@example.com"}; !slices.Equal(audit, want) {
		t.Errorf("command line audit = %q, want %q: promoting an instance admin again records nothing", audit, want)
	}
}

func TestAdminBootstrapSetsALocalCredential(t *testing.T) {
	t.Run("from FLAGNETIC_ADMIN_PASSWORD", func(t *testing.T) {
		url := testkit.EmptyDatabase(t)
		if r := migrate(url); r.code != 0 {
			t.Fatalf("migrate: %s", r.stderr)
		}
		r := bootstrap(map[string]string{"DATABASE_URL": url, "FLAGNETIC_ADMIN_PASSWORD": adminPassword}, "--email", "first@example.com")
		if r.code != 0 || !tokenValue.MatchString(strings.TrimSpace(r.stdout)) {
			t.Fatalf("exit code = %d, stdout = %q; want the token alone", r.code, r.stdout)
		}
		if strings.Contains(r.stdout+r.stderr, adminPassword) {
			t.Error("the password was printed")
		}
		srv := serve(t, map[string]string{"DATABASE_URL": url})
		audit := cliAudit(t, signIn(t, srv.baseURL, "first@example.com", adminPassword))
		for _, want := range []string{"flagnetic admin bootstrap member.created first@example.com", "flagnetic admin bootstrap api_token.created bootstrap"} {
			if !slices.Contains(audit, want) {
				t.Errorf("command line audit = %q, want %q", audit, want)
			}
		}
	})
	t.Run("generated", func(t *testing.T) {
		url := testkit.EmptyDatabase(t)
		if r := migrate(url); r.code != 0 {
			t.Fatalf("migrate: %s", r.stderr)
		}
		r := bootstrap(map[string]string{"DATABASE_URL": url}, "--email", "first@example.com")
		m := regexp.MustCompile(`(?m)^Password: ([0-9A-Za-z]{24})$`).FindStringSubmatch(r.stderr)
		if r.code != 0 || m == nil {
			t.Fatalf("exit code = %d, stderr:\n%s\nwant a generated password", r.code, r.stderr)
		}
		signIn(t, serve(t, map[string]string{"DATABASE_URL": url}).baseURL, "first@example.com", m[1])
	})
}

func TestAdminBootstrapRefusesAWeakPasswordBeforeCreatingAnything(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url); r.code != 0 {
		t.Fatalf("migrate: %s", r.stderr)
	}
	file := filepath.Join(t.TempDir(), "token")
	r := bootstrap(map[string]string{"DATABASE_URL": url, "FLAGNETIC_ADMIN_PASSWORD": "first-password"}, "--email", "first@example.com", "--token-file", file)
	if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "weak_password (email)") {
		t.Errorf("exit code = %d, stdout = %q, stderr:\n%s\nwant 2 naming the email rule", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the token file was left behind: %v", err)
	}
	if r := bootstrap(map[string]string{"DATABASE_URL": url, "FLAGNETIC_ADMIN_PASSWORD": adminPassword}, "--email", "first@example.com"); r.code != 0 {
		t.Errorf("a bootstrap after the refusal: exit code = %d, want 0 since nothing was created; stderr:\n%s", r.code, r.stderr)
	}
}
