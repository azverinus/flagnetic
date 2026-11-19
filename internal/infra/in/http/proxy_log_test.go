package http_test

import (
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sso"
)

// nginxExample returns the nginx configuration docs/install.md ships.
func nginxExample(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile("../../../../docs/install.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(doc), "```nginx\n")
	if !found {
		t.Fatal("docs/install.md has no nginx example")
	}
	config, _, _ := strings.Cut(rest, "```")
	return config
}

// The configuration the docs ship must keep every one-time secret out of the
// proxy's access log: out of the path it writes, and out of anything else it
// writes (the query string, the Referer, the raw request line).
func TestTheDocumentedNginxLogHoldsNoOneTimeSecret(t *testing.T) {
	config := nginxExample(t)

	// The masking map, as nginx evaluates it: its regular expression against
	// the decoded path, with the named capture its value uses.
	mapRe := regexp.MustCompile(`(?s)map \$uri \$flagnetic_log_path \{.*?~(\S+)\s+"([^"]+)";`)
	m := mapRe.FindStringSubmatch(config)
	if m == nil {
		t.Fatal("the nginx example has no map of $uri to $flagnetic_log_path")
	}
	masked, err := regexp.Compile(m[1])
	if err != nil {
		t.Fatalf("the map's expression %q: %v", m[1], err)
	}
	logged := func(path string) string {
		match := masked.FindStringSubmatchIndex(path)
		if match == nil {
			return path
		}
		return string(masked.ExpandString(nil, m[2], path, match))
	}

	// The routes the server itself masks in its log.
	edge := flaghttp.New(flaghttp.Options{Logger: slog.New(slog.DiscardHandler)})
	management.Mount(edge, management.Handlers{}, prometheus.NewRegistry(), management.Options{})
	sso.Mount(edge, sso.Handlers{}, sso.Options{})
	patterns := edge.SecretPaths()
	if len(patterns) < 5 {
		t.Fatalf("the server masks %v, want the invitation and reset pages and routes and the sso completion", patterns)
	}
	wildcard := regexp.MustCompile(`\{[a-z_]+\}`)
	for _, pattern := range patterns {
		const secret = "c2VjcmV0LXRoYXQtbXVzdC1uZXZlci1iZS1sb2dnZWQ"
		path := wildcard.ReplaceAllString(pattern, secret)
		if got := logged(path); strings.Contains(got, secret) || got == path {
			t.Errorf("the server masks %s, but the documented log writes %q for %s", pattern, got, path)
		}
		// An encoded spelling is the same path to nginx's $uri, so it is the
		// decoded one that is matched.
		if decoded, err := url.PathUnescape(strings.Replace(path, "/invitations/", "/%69nvitations/", 1)); err == nil {
			if got := logged(decoded); strings.Contains(got, secret) {
				t.Errorf("an encoded spelling of %s is logged as %q", path, got)
			}
		}
	}

	// What the log_format writes: no variable that holds the secret.
	format := regexp.MustCompile(`(?s)log_format flagnetic (.*?);`).FindStringSubmatch(config)
	if format == nil {
		t.Fatal("the nginx example has no log_format named flagnetic")
	}
	for _, variable := range []string{"$request ", "$request\"", "$request_uri", "$args", "$query_string", "$is_args", "$http_referer", "$http_", "$uri", "$request_line"} {
		if variable == "$http_" {
			if strings.Count(format[1], "$http_") != strings.Count(format[1], "$http_user_agent") {
				t.Errorf("the log_format writes a header other than the user agent: %s", format[1])
			}
			continue
		}
		if strings.Contains(format[1]+" ", variable) {
			t.Errorf("the log_format writes %s, which can hold a secret: %s", variable, format[1])
		}
	}
	if !strings.Contains(format[1], "$flagnetic_log_path") {
		t.Errorf("the log_format does not write the masked path: %s", format[1])
	}

	// Every access_log of the example uses it, so no server falls back to the
	// default combined format.
	accessLogs := regexp.MustCompile(`access_log\s+(\S+)\s+(\S+);`).FindAllStringSubmatch(config, -1)
	servers := strings.Count(config, "server {")
	if len(accessLogs) != servers {
		t.Errorf("%d access_log lines for %d server blocks, want one in each", len(accessLogs), servers)
	}
	for _, a := range accessLogs {
		if a[2] != "flagnetic" {
			t.Errorf("access_log %s uses the format %s, want flagnetic", a[1], a[2])
		}
	}
}
