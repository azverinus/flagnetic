package config_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/config"
)

const chartDir = "../../../deploy/helm/flagnetic/"

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
