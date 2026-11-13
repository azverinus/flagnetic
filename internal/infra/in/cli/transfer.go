package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// requestTimeout bounds one request to the instance: an import of a large
// document runs as a single transaction and may take minutes.
const requestTimeout = 5 * time.Minute

// defaultURL is where the client commands look for the instance unless told.
const defaultURL = "http://localhost:8080"

// connection is how a client command reaches a running instance: its base
// URL and an API token. A client command never reads the server's
// configuration or its database.
type connection struct {
	url       string
	token     string
	tokenFile string
}

func (c *connection) register(fs *flag.FlagSet, lookupEnv func(string) (string, bool)) {
	url, ok := lookupEnv("FLAGNETIC_URL")
	if !ok || url == "" {
		url = defaultURL
	}
	token, _ := lookupEnv("FLAGNETIC_TOKEN")
	fs.StringVar(&c.url, "url", url, "base URL of the instance (FLAGNETIC_URL)")
	fs.StringVar(&c.token, "token", token, "API token (FLAGNETIC_TOKEN)")
	fs.StringVar(&c.tokenFile, "token-file", "", "read the API token from this file instead")
}

// client returns the management API client for c. A token file wins over a
// token given directly, and its surrounding whitespace is not part of the
// token.
func (c *connection) client() (*management.ClientWithResponses, error) {
	token := c.token
	if c.tokenFile != "" {
		b, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("read token: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return nil, errors.New("an API token is required: set FLAGNETIC_TOKEN, --token or --token-file")
	}
	return management.NewClientWithResponses(strings.TrimRight(c.url, "/")+management.BaseURL,
		management.WithHTTPClient(&http.Client{Timeout: requestTimeout}),
		management.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
}

const exportUsage = `usage: flagnetic export --project KEY [--format yaml|json] [--output FILE]
                        [--url URL] [--token TOKEN | --token-file FILE]`

// export writes a project's document to stdout or a file.
func export(ctx context.Context, args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, exportUsage); fs.PrintDefaults() }
	var conn connection
	conn.register(fs, lookupEnv)
	projectKey := fs.String("project", "", "key of the project to export (required)")
	format := fs.String("format", "yaml", "yaml or json")
	output := fs.String("output", "", "write the document to this file instead of standard output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "export: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	case *projectKey == "":
		fmt.Fprintln(stderr, "export: --project is required")
		return exitUsage
	case *format != "yaml" && *format != "json":
		fmt.Fprintf(stderr, "export: --format must be yaml or json, got %q\n", *format)
		return exitUsage
	}
	client, err := conn.client()
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return exitUsage
	}
	f := management.ExportProjectParamsFormat(*format)
	res, err := client.ExportProject(ctx, *projectKey, &management.ExportProjectParams{Format: &f})
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return exitFailure
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return exitFailure
	}
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "export: %s\n", describeRefusal(res.StatusCode, body))
		return exitFailure
	}
	printETag(stderr, res.Header)
	if *output == "" {
		_, err = stdout.Write(body)
	} else {
		err = writePrivateFile(*output, body)
	}
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return exitFailure
	}
	return exitOK
}

const importUsage = `usage: flagnetic import --file FILE|- [--mode create|merge|replace] [--project KEY] [--name NAME]
                        [--dry-run] [--if-match ETAG]
                        [--url URL] [--token TOKEN | --token-file FILE]`

// importDocument creates a project from a document, or merges a document
// into a project or replaces the project's configuration with it.
func importDocument(ctx context.Context, args []string, lookupEnv func(string) (string, bool), stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, importUsage); fs.PrintDefaults() }
	var conn connection
	conn.register(fs, lookupEnv)
	file := fs.String("file", "", "the document, JSON for a .json file and YAML otherwise; - reads standard input (required)")
	mode := fs.String("mode", "create", "create a project, or merge or replace into --project")
	projectKey := fs.String("project", "", "the project to merge or replace into; with create, the new project's key")
	name := fs.String("name", "", "with create, the new project's name")
	dryRunFlag := fs.Bool("dry-run", false, "print what would change and write nothing")
	ifMatch := fs.String("if-match", "", "with merge or replace, refuse when the project changed since this ETag")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "import: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	case *file == "":
		fmt.Fprintln(stderr, "import: --file is required")
		return exitUsage
	case *mode != "create" && *mode != "merge" && *mode != "replace":
		fmt.Fprintf(stderr, "import: --mode must be create, merge or replace, got %q\n", *mode)
		return exitUsage
	case *mode != "create" && *projectKey == "":
		fmt.Fprintf(stderr, "import: --mode %s needs --project\n", *mode)
		return exitUsage
	case *mode != "create" && *name != "":
		fmt.Fprintln(stderr, "import: --name applies to --mode create only")
		return exitUsage
	case *mode == "create" && *ifMatch != "":
		fmt.Fprintln(stderr, "import: --if-match applies to --mode merge and replace only")
		return exitUsage
	}
	client, err := conn.client()
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return exitUsage
	}
	document, contentType, err := readDocument(*file, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return exitFailure
	}

	var (
		status int
		body   []byte
		result *management.ImportResult
	)
	yes := true
	var header http.Header
	var dry *management.ImportDryRun
	if *dryRunFlag {
		dry = &yes
	}
	if *mode == "create" {
		res, err := client.ImportNewProjectWithBodyWithResponse(ctx, &management.ImportNewProjectParams{
			Key: optional(*projectKey), Name: optional(*name), DryRun: dry,
		}, contentType, bytes.NewReader(document))
		if err != nil {
			fmt.Fprintf(stderr, "import: %v\n", err)
			return exitFailure
		}
		status, body, result = res.StatusCode(), res.Body, res.JSON201
		if result == nil {
			result = res.JSON200
		}
		header = res.HTTPResponse.Header
	} else {
		res, err := client.ImportProjectWithBodyWithResponse(ctx, *projectKey, &management.ImportProjectParams{
			Mode: management.ImportProjectParamsMode(*mode), DryRun: dry, IfMatch: optional(*ifMatch),
		}, contentType, bytes.NewReader(document))
		if err != nil {
			fmt.Fprintf(stderr, "import: %v\n", err)
			return exitFailure
		}
		status, body, result = res.StatusCode(), res.Body, res.JSON200
		header = res.HTTPResponse.Header
	}
	if result == nil {
		fmt.Fprintf(stderr, "import: %s\n", describeRefusal(status, body))
		return exitFailure
	}
	if *dryRunFlag {
		printETag(stderr, header)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(stderr, "warning: %s %s: %s\n", w.Code, w.Pointer, w.Detail)
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return exitFailure
	}
	fmt.Fprintln(stdout, string(out))
	return exitOK
}

// readDocument reads the document at path, or standard input for "-", with
// its content type: JSON for a .json file, otherwise YAML, of which JSON is
// a subset.
func readDocument(path string, stdin io.Reader) ([]byte, string, error) {
	var (
		b   []byte
		err error
	)
	if path == "-" {
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(path) //nolint:gosec // the operator chooses the path
	}
	if err != nil {
		return nil, "", fmt.Errorf("read document: %w", err)
	}
	if path != "-" && strings.EqualFold(filepath.Ext(path), ".json") {
		return b, "application/json", nil
	}
	return b, "application/yaml", nil
}

// describeRefusal describes a refused request as `<status> <code>: <detail>`
// followed by each fault found in the document and each entity still
// naming what the write would remove. A body that is not a problem is shown
// as it came.
func describeRefusal(status int, body []byte) string {
	var p management.Problem
	if json.Unmarshal(body, &p) != nil || p.Code == "" {
		return fmt.Sprintf("%d: %s", status, strings.TrimSpace(string(body)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", status, p.Code)
	if p.Detail != nil {
		fmt.Fprintf(&b, ": %s", *p.Detail)
	}
	if p.Errors != nil {
		for _, v := range *p.Errors {
			fmt.Fprintf(&b, "\n  %s %s: %s", v.Pointer, v.Code, v.Detail)
		}
	}
	if p.References != nil {
		for _, r := range *p.References {
			fmt.Fprintf(&b, "\n  referenced by %s %s", r.Kind, r.Key)
			if r.Environment != nil {
				fmt.Fprintf(&b, " in %s", *r.Environment)
			}
		}
	}
	return b.String()
}

// writePrivateFile writes b to path readable by its owner alone: a document
// names owners by email address, so it is not for everyone. The mode is set
// on an existing file too, which os.WriteFile would leave as it was.
func writePrivateFile(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // the operator chooses the path
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// printETag tells the operator which state of the project a document or a dry
// run answers, so that --if-match can name it.
func printETag(stderr io.Writer, h http.Header) {
	if etag := h.Get("ETag"); etag != "" {
		fmt.Fprintf(stderr, "etag: %s\n", etag)
	}
}
