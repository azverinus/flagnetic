package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// transferInstance is a running instance with an instance admin's token in a
// file, which export and import reach over HTTP alone.
type transferInstance struct {
	baseURL   string
	tokenFile string
	dir       string
}

func newTransferInstance(t *testing.T) transferInstance {
	t.Helper()
	env := map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t)}
	r := serve(t, env)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if b := bootstrap(env, "--email", "first@example.com", "--token-file", tokenFile); b.code != 0 {
		t.Fatalf("bootstrap: exit %d: %s", b.code, b.stderr)
	}
	return transferInstance{baseURL: r.baseURL, tokenFile: tokenFile, dir: dir}
}

// cli runs a client command against the instance, with the URL in the
// environment and the token in its file.
func (i transferInstance) cli(args ...string) result {
	return run(map[string]string{"FLAGNETIC_URL": i.baseURL}, nil, append(args, "--token-file", i.tokenFile)...)
}

func (i transferInstance) createProject(t *testing.T, key, name string) {
	t.Helper()
	token, err := os.ReadFile(i.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	c, err := management.NewClientWithResponses(i.baseURL+management.BaseURL, management.WithRequestEditorFn(
		func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: name})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("create project %s: %v %s", key, err, res.Body)
	}
}

func (i transferInstance) export(t *testing.T, project, format string) string {
	t.Helper()
	r := i.cli("export", "--project", project, "--format", format)
	if r.code != 0 {
		t.Fatalf("export %s: exit %d: %s", project, r.code, r.stderr)
	}
	return r.stdout
}

func TestExportWritesAFileAndImportCreatesACopyUnderAnotherKey(t *testing.T) {
	i := newTransferInstance(t)
	i.createProject(t, "shop", "Shop")
	original := i.export(t, "shop", "json")

	file := filepath.Join(i.dir, "shop.yaml")
	// A file already there loses its old mode and content: the document
	// names owners by email address.
	if err := os.WriteFile(file, []byte("old content that is longer than nothing\n"), 0o644); err != nil { //nolint:gosec // the test needs a readable file
		t.Fatal(err)
	}
	if r := i.cli("export", "--project", "shop", "--output", file); r.code != 0 || r.stdout != "" {
		t.Fatalf("export to a file: exit %d, stdout %q, stderr %s", r.code, r.stdout, r.stderr)
	}
	if b, err := os.ReadFile(file); err != nil || strings.HasPrefix(string(b), "{") || strings.Contains(string(b), "old content") {
		t.Fatalf("the file holds %q (%v); want a YAML document", b, err)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the file's mode is %v (%v); want 0600", st.Mode().Perm(), err)
	}

	r := i.cli("import", "--file", file, "--project", "copy", "--name", "Shop copy")
	if r.code != 0 {
		t.Fatalf("import: exit %d: %s", r.code, r.stderr)
	}
	var out struct {
		DryRun  bool `json:"dry_run"`
		Mode    string
		Project struct{ Key, Name string }
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Mode != "create" || out.Project.Key != "copy" || out.Project.Name != "Shop copy" || out.DryRun {
		t.Fatalf("import printed %s (%v)", r.stdout, err)
	}
	if !strings.Contains(r.stdout, "\n  \"mode\"") {
		t.Errorf("the result is not indented:\n%s", r.stdout)
	}
	want := strings.Replace(strings.Replace(original, `"key": "shop"`, `"key": "copy"`, 1), `"name": "Shop"`, `"name": "Shop copy"`, 1)
	if got := i.export(t, "copy", "json"); got != want {
		t.Errorf("export of the copy differs from the original but for key and name:\n%s\nwant\n%s", got, want)
	}
}

func TestImportReadsJSONByExtensionAndStandardInputAsYAML(t *testing.T) {
	i := newTransferInstance(t)
	i.createProject(t, "shop", "Shop")
	jsonFile := filepath.Join(i.dir, "shop.json")
	if err := os.WriteFile(jsonFile, []byte(i.export(t, "shop", "json")), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := i.cli("import", "--file", jsonFile, "--project", "from_json"); r.code != 0 {
		t.Fatalf("import of a .json file: exit %d: %s", r.code, r.stderr)
	}
	// A JSON document without the extension is still read: JSON is YAML.
	noExt := filepath.Join(i.dir, "shop.doc")
	if err := os.WriteFile(noExt, []byte(i.export(t, "shop", "json")), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := i.cli("import", "--file", noExt, "--project", "from_doc"); r.code != 0 {
		t.Fatalf("import of a file with another extension: exit %d: %s", r.code, r.stderr)
	}
	stdin := cli.WithStdin(strings.NewReader(i.export(t, "shop", "yaml")))
	r := run(map[string]string{"FLAGNETIC_URL": i.baseURL}, []cli.Option{stdin}, "import", "--file", "-", "--project", "from_stdin", "--token-file", i.tokenFile)
	if r.code != 0 {
		t.Fatalf("import from standard input: exit %d: %s", r.code, r.stderr)
	}
}

func TestAMergeDryRunPrintsTheChangesAndWritesNothing(t *testing.T) {
	i := newTransferInstance(t)
	i.createProject(t, "shop", "Shop")
	before := i.export(t, "shop", "json")
	edited := filepath.Join(i.dir, "edited.json")
	if err := os.WriteFile(edited, []byte(strings.Replace(before, `"name": "Shop"`, `"name": "Store"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := i.cli("import", "--file", edited, "--mode", "merge", "--project", "shop", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, `"dry_run": true`) || !strings.Contains(r.stdout, `"mode": "merge"`) {
		t.Fatalf("dry run: exit %d, stdout %s, stderr %s", r.code, r.stdout, r.stderr)
	}
	if got := i.export(t, "shop", "json"); got != before {
		t.Error("a dry run changed the project")
	}
}

func TestImportRefusesATakenKeyWithExitOne(t *testing.T) {
	i := newTransferInstance(t)
	i.createProject(t, "shop", "Shop")
	file := filepath.Join(i.dir, "shop.json")
	if err := os.WriteFile(file, []byte(i.export(t, "shop", "json")), 0o600); err != nil {
		t.Fatal(err)
	}
	r := i.cli("import", "--file", file)
	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "409 already_exists: ") {
		t.Errorf("import of a taken key: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestImportRefusesAMalformedDocumentWithItsFaults(t *testing.T) {
	i := newTransferInstance(t)
	file := filepath.Join(i.dir, "broken.yaml")
	if err := os.WriteFile(file, []byte("schema_version: 1\nnot_a_field: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := i.cli("import", "--file", file, "--project", "broken")
	if r.code != 1 || !strings.Contains(r.stderr, "422 invalid_document: ") || !strings.Contains(r.stderr, "\n  /not_a_field unknown_field: ") {
		t.Errorf("import of a malformed document: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	file := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"replace without a project":     {[]string{"import", "--file", file, "--mode", "replace", "--token", "t"}, "--project"},
		"merge without a project":       {[]string{"import", "--file", file, "--mode", "merge", "--token", "t"}, "--project"},
		"a name outside create":         {[]string{"import", "--file", file, "--mode", "merge", "--project", "p", "--name", "N", "--token", "t"}, "--name"},
		"an unknown mode":               {[]string{"import", "--file", file, "--mode", "overwrite", "--project", "p", "--token", "t"}, "--mode"},
		"no file":                       {[]string{"import", "--token", "t"}, "--file"},
		"an if-match outside a project": {[]string{"import", "--file", file, "--if-match", `"3"`, "--token", "t"}, "--if-match"},
		"export without a project":      {[]string{"export", "--token", "t"}, "--project"},
		"export in an unknown format":   {[]string{"export", "--project", "p", "--format", "xml", "--token", "t"}, "--format"},
		"export without a token":        {[]string{"export", "--project", "p"}, "token"},
		"import without a token":        {[]string{"import", "--file", file}, "token"},
		"an unknown flag":               {[]string{"export", "--project", "p", "--nope", "--token", "t"}, "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			r := run(map[string]string{"FLAGNETIC_URL": "http://127.0.0.1:1"}, nil, tc.args...)
			if r.code != 2 || !strings.Contains(r.stderr, tc.want) {
				t.Errorf("exit %d, stderr %q; want exit 2 mentioning %q", r.code, r.stderr, tc.want)
			}
		})
	}
}

func TestTheTokenComesFromAFlagTheEnvironmentOrATrimmedFile(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("schema_version: 1\n"))
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("  from_file \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FLAGNETIC_URL": srv.URL, "FLAGNETIC_TOKEN": "from_env"}
	for _, r := range []result{
		run(env, nil, "export", "--project", "p"),
		run(env, nil, "export", "--project", "p", "--token", "from_flag"),
		run(env, nil, "export", "--project", "p", "--token", "from_flag", "--token-file", file),
		run(map[string]string{}, nil, "export", "--project", "p", "--url", srv.URL, "--token", "x"),
	} {
		if r.code != 0 {
			t.Fatalf("exit %d: %s", r.code, r.stderr)
		}
	}
	want := []string{"Bearer from_env", "Bearer from_flag", "Bearer from_file", "Bearer x"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("Authorization headers = %q, want %q", seen, want)
	}
}

func TestARefusalIsPrintedWithItsStatusCodeDetailAndReferences(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"in_use","detail":"a segment is still used","request_id":"r1",
			"references":[{"kind":"flag","key":"checkout","environment":"production"},{"kind":"segment","key":"beta","environment":null}]}`))
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FLAGNETIC_URL": srv.URL, "FLAGNETIC_TOKEN": "t"}
	r := run(env, nil, "import", "--file", file, "--mode", "replace", "--project", "p")
	want := "409 in_use: a segment is still used\n  referenced by flag checkout in production\n  referenced by segment beta\n"
	if r.code != 1 || r.stdout != "" || !strings.HasSuffix(r.stderr, want) {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 1 ending in %q", r.code, r.stdout, r.stderr, want)
	}
}

func TestWarningsGoToStandardErrorOneLineEach(t *testing.T) {
	var query, ifMatch, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, ifMatch, contentType = r.URL.RawQuery, r.Header.Get("If-Match"), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dry_run":false,"mode":"merge","source":"document","project":{"key":"p","name":"P"},
			"changes":{"attributes":{"created":[],"updated":[],"deleted":[]},"environments":{"created":[],"updated":[],"deleted":[]},
			"experiments":{"created":[],"updated":[],"deleted":[]},"flags":{"created":[],"updated":[],"deleted":[]},
			"segments":{"created":[],"updated":[],"deleted":[]},"unit_types":{"created":[],"updated":[],"deleted":[]}},
			"warnings":[{"code":"owner_replaced","pointer":"/flags/0/owner","detail":"gone@example.com cannot own"},{"code":"x","pointer":"","detail":"whole"}]}`))
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(file, []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FLAGNETIC_URL": srv.URL, "FLAGNETIC_TOKEN": "t"}
	r := run(env, nil, "import", "--file", file, "--mode", "merge", "--project", "p", "--if-match", `"7"`)
	wantErr := "warning: owner_replaced /flags/0/owner: gone@example.com cannot own\nwarning: x : whole\n"
	if r.code != 0 || r.stderr != wantErr {
		t.Errorf("exit %d, stderr %q; want warnings %q", r.code, r.stderr, wantErr)
	}
	if !strings.Contains(r.stdout, `"mode": "merge"`) {
		t.Errorf("stdout %q lacks the result", r.stdout)
	}
	if query != "mode=merge" || ifMatch != `"7"` || contentType != "application/yaml" {
		t.Errorf("request: query %q, If-Match %q, Content-Type %q", query, ifMatch, contentType)
	}
}

func TestADocumentIsSentAsJSONForAJSONFileAndAsYAMLOtherwise(t *testing.T) {
	var contentTypes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"already_exists","request_id":"r1"}`))
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	env := map[string]string{"FLAGNETIC_URL": srv.URL, "FLAGNETIC_TOKEN": "t"}
	for _, name := range []string{"doc.json", "DOC.JSON", "doc.yaml", "doc"} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		run(env, nil, "import", "--file", file)
	}
	run(env, []cli.Option{cli.WithStdin(strings.NewReader("{}"))}, "import", "--file", "-")
	want := "application/json|application/json|application/yaml|application/yaml|application/yaml"
	if got := strings.Join(contentTypes, "|"); got != want {
		t.Errorf("Content-Type = %s, want %s", got, want)
	}
}

func TestHelpListsTheTransferCommands(t *testing.T) {
	r := run(nil, nil, "help")
	for _, want := range []string{"export", "import"} {
		if r.code != 0 || !strings.Contains(r.stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestFailuresBeforeAndAfterTheRequestExitOne(t *testing.T) {
	i := newTransferInstance(t)
	if r := i.cli("export", "--project", "missing"); r.code != 1 || !strings.Contains(r.stderr, "404 ") {
		t.Errorf("export of a missing project: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := i.cli("import", "--file", filepath.Join(i.dir, "absent.yaml")); r.code != 1 || !strings.Contains(r.stderr, "read document") {
		t.Errorf("import of an absent file: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := run(map[string]string{"FLAGNETIC_URL": "http://127.0.0.1:1"}, nil, "export", "--project", "p", "--token", "t"); r.code != 1 {
		t.Errorf("export from an unreachable instance: exit %d, stderr %q", r.code, r.stderr)
	}
	i.createProject(t, "shop", "Shop")
	if r := i.cli("export", "--project", "shop", "--output", filepath.Join(i.dir, "no", "such", "dir", "d.yaml")); r.code != 1 {
		t.Errorf("export to an unwritable path: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := run(map[string]string{"FLAGNETIC_URL": i.baseURL}, nil, "export", "--project", "shop", "--token-file", filepath.Join(i.dir, "absent")); r.code != 2 {
		t.Errorf("export with an absent token file: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestTheETagOfAnExportAndOfADryRunCanBeSentBackAsIfMatch(t *testing.T) {
	i := newTransferInstance(t)
	i.createProject(t, "shop", "Shop")
	r := i.cli("export", "--project", "shop", "--format", "json")
	etag := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(r.stderr, "\n", 2)[0], "etag: "))
	if r.code != 0 || !strings.HasPrefix(r.stderr, "etag: ") || etag == "" {
		t.Fatalf("export: exit %d, stderr %q; want an etag line", r.code, r.stderr)
	}
	file := filepath.Join(i.dir, "shop.json")
	if err := os.WriteFile(file, []byte(r.stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := i.cli("import", "--file", file, "--mode", "merge", "--project", "shop", "--dry-run"); d.code != 0 || !strings.Contains(d.stderr, "etag: "+etag) {
		t.Errorf("dry run: exit %d, stderr %q; want the same etag %s", d.code, d.stderr, etag)
	}
	if m := i.cli("import", "--file", file, "--mode", "merge", "--project", "shop", "--if-match", etag); m.code != 0 || strings.Contains(m.stderr, "etag:") {
		t.Errorf("merge with the etag: exit %d, stderr %q", m.code, m.stderr)
	}
	if m := i.cli("import", "--file", file, "--mode", "merge", "--project", "shop", "--if-match", `"0"`); m.code != 1 || !strings.Contains(m.stderr, "412 version_mismatch") {
		t.Errorf("merge with a stale etag: exit %d, stderr %q", m.code, m.stderr)
	}
}
