// Package helmchart holds the test that keeps the Helm chart's README in step
// with its values.
package helmchart_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
