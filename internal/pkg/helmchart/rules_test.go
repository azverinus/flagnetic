package helmchart_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A renamed metric would silently disable the alert that selects it, so every
// metric a rule names must be one the operations guide documents, and that
// table is kept in step with the code by the metrics documentation tests.
func TestEveryMetricTheAlertsSelectIsDocumented(t *testing.T) {
	rules, err := os.ReadFile(chartDir + "templates/prometheusrule.yaml")
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile("../../../docs/operations.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `(flagnetic_[a-z_]+)`").FindAllStringSubmatch(string(guide), -1) {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("docs/operations.md has no metrics table")
	}
	var named int
	for _, m := range regexp.MustCompile(`flagnetic_[a-z_]+`).FindAllString(string(rules), -1) {
		named++
		// A histogram's series carry a suffix the table does not list.
		base := m
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			base = strings.TrimSuffix(base, suffix)
		}
		if !documented[base] {
			t.Errorf("prometheusrule.yaml selects %s, which docs/operations.md does not list", m)
		}
	}
	if named == 0 {
		t.Fatal("prometheusrule.yaml names no metric")
	}
}
