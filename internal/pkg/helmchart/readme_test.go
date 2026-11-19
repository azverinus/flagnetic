// Package helmchart holds the tests that keep the Helm chart's README in step
// with its values, its alerts on metrics the server exports, and the commands
// the documentation shows able to finish a long migration.
package helmchart_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/infra/config"
)

const chartDir = "../../../deploy/helm/flagnetic/"

// leafPaths lists the dotted paths of the values a README row must document:
// every scalar, and every empty map or list, which an operator fills in.
func leafPaths(prefix string, n *yaml.Node, out *[]string) {
	switch {
	case n.Kind == yaml.MappingNode && len(n.Content) > 0:
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if prefix != "" {
				key = prefix + "." + key
			}
			leafPaths(key, n.Content[i+1], out)
		}
	default:
		*out = append(*out, prefix)
	}
}

// defaults maps the dotted path of each value that leafPaths lists to the
// default as the README writes it: a string as itself, an empty string as
// "", an empty map as {} and an empty list as [].
func defaults(prefix string, n *yaml.Node, out map[string]string) {
	switch {
	case n.Kind == yaml.MappingNode && len(n.Content) > 0:
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if prefix != "" {
				key = prefix + "." + key
			}
			defaults(key, n.Content[i+1], out)
		}
	case n.Kind == yaml.MappingNode:
		out[prefix] = "{}"
	case n.Kind == yaml.SequenceNode && len(n.Content) == 0:
		out[prefix] = "[]"
	case n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value == "":
		out[prefix] = `""`
	default:
		out[prefix] = n.Value
	}
}

func TestReadmeDefaultsAreTheValuesDefaults(t *testing.T) {
	raw, err := os.ReadFile(chartDir + "values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	defaults("", doc.Content[0], want)

	readme, err := os.ReadFile(chartDir + "README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, values, found := strings.Cut(string(readme), "\n## Values\n")
	if !found {
		t.Fatal("README.md has no Values section")
	}
	row := regexp.MustCompile("(?m)^\\| `([A-Za-z0-9_.]+)` \\| `([^`]*)` \\|")
	for _, m := range row.FindAllStringSubmatch(values, -1) {
		path, got := m[1], m[2]
		if w, ok := want[path]; ok && w != got {
			t.Errorf("README.md gives %s the default %q, values.yaml %q", path, got, w)
		}
	}
}

func TestReadmeListsEveryValue(t *testing.T) {
	raw, err := os.ReadFile(chartDir + "values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var want []string
	leafPaths("", doc.Content[0], &want)

	readme, err := os.ReadFile(chartDir + "README.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `([A-Za-z0-9_.]+)` \\|")
	var got []string
	_, values, found := strings.Cut(string(readme), "\n## Values\n")
	if !found {
		t.Fatal("README.md has no Values section")
	}
	for _, m := range row.FindAllStringSubmatch(values, -1) {
		got = append(got, m[1])
	}

	sort.Strings(want)
	sort.Strings(got)
	if diff := difference(want, got); diff != "" {
		t.Errorf("README.md and values.yaml differ:\n%s", diff)
	}
}

// difference names the paths only one of the two lists has.
func difference(want, got []string) string {
	have := map[string]int{}
	for _, p := range want {
		have[p]++
	}
	for _, p := range got {
		have[p]--
	}
	var lines []string
	for p, n := range have {
		switch {
		case n > 0:
			lines = append(lines, "  in values.yaml, not in the README: "+p)
		case n < 0:
			lines = append(lines, "  in the README, not in values.yaml (or twice): "+p)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// chartManaged are the settings the chart renders itself, from migrations.mode,
// service.port, service.metrics.port and secrets; the keys of config leave
// them out.
var chartManaged = []string{"listen_addr", "metrics_listen_addr", "auto_migrate", "database_url", "secret_key"}

func TestTheChartRefusesExactlyTheConfigKeysTheServerLacks(t *testing.T) {
	raw, err := os.ReadFile(chartDir + "values.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Config struct {
				PropertyNames struct {
					Enum []string `json:"enum"`
				} `json:"propertyNames"`
			} `json:"config"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	got := schema.Properties.Config.PropertyNames.Enum

	var want []string
	for _, s := range config.Settings() {
		if s.Key != "" && !slices.Contains(chartManaged, s.Key) {
			want = append(want, s.Key)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("the keys of config in values.schema.json differ from the settings table's file keys minus the chart's own:\n"+
			"missing from the schema: %v\nin the schema but no setting: %v",
			missingFrom(got, want), missingFrom(want, got))
	}
}

// TestTheTemplateRefusesTheKeysTheGuardLeavesOut keeps chartManaged in step
// with the list the template refuses.
func TestTheTemplateRefusesTheKeysTheGuardLeavesOut(t *testing.T) {
	raw, err := os.ReadFile(chartDir + "templates/_helpers.tpl")
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`range \$key := list ([^-]*)-`).FindStringSubmatch(string(raw))
	if list == nil {
		t.Fatal("_helpers.tpl no longer lists the settings the chart owns")
	}
	got := regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(list[1], -1)
	var keys []string
	for _, m := range got {
		keys = append(keys, m[1])
	}
	sort.Strings(keys)
	want := slices.Clone(chartManaged)
	sort.Strings(want)
	if !slices.Equal(keys, want) {
		t.Errorf("_helpers.tpl refuses %v, the guard test expects %v", keys, want)
	}
}

// missingFrom lists what want has and have lacks.
func missingFrom(have, want []string) []string {
	var out []string
	for _, x := range want {
		if !slices.Contains(have, x) {
			out = append(out, x)
		}
	}
	return out
}
