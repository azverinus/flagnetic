package flagnetic_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
)

// scenarioReport names the file a test run writes the protocol scenarios
// that passed to, the report .github/scripts/behaviour-matrix.sh builds the
// Go column of docs/sdks/behaviour-matrix.md from. Unset, no report is
// written.
const scenarioReport = "FLAGNETIC_SCENARIO_REPORT"

var passed struct {
	sync.Mutex
	names []string
}

// reportScenario records the scenario t runs as passed once t ends neither
// failed nor skipped, so the report holds only what this run proved.
func reportScenario(t *testing.T, name string) {
	t.Cleanup(func() {
		if t.Failed() || t.Skipped() {
			return
		}
		passed.Lock()
		defer passed.Unlock()
		passed.names = append(passed.names, name)
	})
}

func TestMain(m *testing.M) {
	code := m.Run()
	if path := os.Getenv(scenarioReport); path != "" {
		passed.Lock()
		names := slices.Sorted(slices.Values(passed.names))
		passed.Unlock()
		if names == nil {
			names = []string{}
		}
		data, _ := json.Marshal(names)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write the scenario report: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}
