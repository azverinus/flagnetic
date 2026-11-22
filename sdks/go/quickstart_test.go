package flagnetic_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// quickstartDoc is the page whose Go snippets this file compiles.
const quickstartDoc = "../../docs/sdks/go.md"

// quickstartDocument is the ruleset the quickstart evaluates: a boolean
// flag turned on for Germany and a running experiment on the user.
const quickstartDocument = `{
  "schema_version": 1, "version": 3, "scope": "server",
  "environment": {"key": "production", "protected": false},
  "unit_types": ["user", "anonymous"],
  "attributes": [{"key": "country", "type": "string"}, {"key": "plan", "type": "string"}],
  "segments": [],
  "flags": [
    {"key": "new_checkout", "type": "boolean", "automatic_exposure": false, "on": true,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "off",
     "rules": [{"id": "de", "conditions": [{"kind": "attribute", "attribute": "country", "op": "eq", "value": "DE"}],
                "serve": {"kind": "variation", "variation": "on"}}]},
    {"key": "checkout_copy", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "control", "value": "Buy now"}, {"key": "short", "value": "Buy"}],
     "off_variation": "control", "fallthrough": "control",
     "rules": [{"id": "exp", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "checkout_copy_test", "unit_type": "user",
                          "inclusion_salt": "in", "variation_salt": "var", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "short", "weight": 10000}]}}]}
  ]
}`

// connectQuickstart is compiled, not run: it names a host that is not there.
// The text between the docs markers is what the page shows.
func connectQuickstart(ctx context.Context) error {
	// docs:begin connect
	c, err := flagnetic.New(os.Getenv("FLAGNETIC_SDK_KEY"), "https://flags.example.com")
	if err != nil {
		return err // a client SDK key: this SDK needs a server key
	}
	defer func() { _ = c.Close(context.Background()) }()

	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stage := c.Ready(readyCtx) // fresh, last_good or built_in
	// docs:end connect
	_ = stage
	return nil
}

// shutdownQuickstart is compiled, not run, for the same reason.
func shutdownQuickstart(c *flagnetic.Client) {
	// docs:begin shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(shutdownCtx); err != nil {
		log.Printf("flagnetic: events not delivered: %v", err)
	}
	// docs:end shutdown
}

// The two functions above are compiled and never called.
var _, _ = connectQuickstart, shutdownQuickstart

// evaluateQuickstart is run by ExampleClient_quickstart against the
// ruleset above.
func evaluateQuickstart(ctx context.Context, c *flagnetic.Client) {
	// docs:begin context
	fc := flagnetic.Context{
		Units:      map[string]string{"user": "u-1001"},
		Attributes: map[string]any{"country": "DE", "plan": "pro"},
	}
	// docs:end context

	// docs:begin evaluate
	if c.Bool(ctx, fc, "new_checkout", false) {
		fmt.Println("serving the new checkout")
	}
	// docs:end evaluate

	// docs:begin detail
	d := c.Detail(ctx, fc, "checkout_copy", "Buy now")
	fmt.Println(d.Value, d.Reason, d.Variation)
	// docs:end detail

	// docs:begin exposure
	label := c.String(ctx, fc, "checkout_copy", "Buy now")
	// ... render the button with label, then record that the unit saw it:
	c.Exposure(ctx, fc, "checkout_copy")
	// docs:end exposure
	fmt.Println(label)

	// docs:begin alias
	c.Alias(ctx, "anonymous", "d-8f1c", "user", "u-1001")
	// docs:end alias
}

func ExampleClient_quickstart() {
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(quickstartDocument)))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	evaluateQuickstart(ctx, c)
	fmt.Println(len(c.Recorder().Exposures()), "exposure,", len(c.Recorder().Aliases()), "alias")
	_ = c.Close(ctx)
	// Output:
	// serving the new checkout
	// Buy experiment short
	// Buy
	// 1 exposure, 1 alias
}

var docsMarker = regexp.MustCompile(`(?s)// docs:begin (\w+)\n(.*?)\t// docs:end \w+\n`)

// dedent removes one tab of indentation from every line and trims blank
// edges, so a snippet from a function body reads as the page shows it.
func dedent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "\t")
	}
	return strings.Join(lines, "\n")
}

// TestTheGoPageShowsOnlySnippetsThisPackageCompiles holds the page and the
// compiled code together: every Go block of the page is a marked region of
// this file, verbatim, and every region is on the page.
func TestTheGoPageShowsOnlySnippetsThisPackageCompiles(t *testing.T) {
	src, err := os.ReadFile("quickstart_test.go")
	if err != nil {
		t.Fatal(err)
	}
	regions := map[string]string{}
	for _, m := range docsMarker.FindAllStringSubmatch(string(src), -1) {
		regions[m[1]] = dedent(m[2])
	}
	if len(regions) == 0 {
		t.Fatal("no docs region found")
	}
	page, err := os.ReadFile(quickstartDoc)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	for _, m := range regexp.MustCompile("(?s)```go\n(.*?)```").FindAllStringSubmatch(string(page), -1) {
		blocks = append(blocks, strings.TrimRight(m[1], "\n"))
	}
	if len(blocks) == 0 {
		t.Fatalf("%s shows no Go block", quickstartDoc)
	}
	for _, b := range blocks {
		found := false
		for _, r := range regions {
			if b == r {
				found = true
			}
		}
		if !found {
			t.Errorf("a Go block of %s is not a compiled region of quickstart_test.go:\n%s", quickstartDoc, b)
		}
	}
	for name, r := range regions {
		if !strings.Contains(string(page), "```go\n"+r+"\n```") {
			t.Errorf("region %s is not on the page", name)
		}
	}
}
