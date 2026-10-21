package flagnetic_test

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

var updateMatrix = flag.Bool("update-matrix", false, "rewrite the Go section of docs/sdks/behaviour-matrix.md")

// matrixPath is the behaviour matrix every SDK writes its section of.
const matrixPath = "../../docs/sdks/behaviour-matrix.md"

const (
	matrixBegin = "<!-- generated:go:begin -->"
	matrixEnd   = "<!-- generated:go:end -->"
)

// TestBehaviourMatrixIsCurrent fails when the Go section of the behaviour
// matrix differs from what this SDK's protocol runners cover, so the page
// can never claim more or less than the runners prove. Every server-mode
// scenario runs (TestEveryServerScenarioIsCovered), so the section lists
// them all, in the order of the scenario files. Run with -update-matrix to
// rewrite the section.
func TestBehaviourMatrixIsCurrent(t *testing.T) {
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, matrixBegin), strings.Index(doc, matrixEnd)
	if start < 0 || end < start {
		t.Fatalf("%s has no Go section between %s and %s", matrixPath, matrixBegin, matrixEnd)
	}
	var b strings.Builder
	b.WriteString("\n| Scenario | Mode |\n|---|---|\n")
	for _, sc := range readScenarios(t).Scenarios {
		if sc.Mode == "server" {
			fmt.Fprintf(&b, "| %s | %s |\n", sc.Name, sc.Mode)
		}
	}
	want := doc[:start+len(matrixBegin)] + b.String() + doc[end:]
	if want == doc {
		return
	}
	if *updateMatrix {
		if err := os.WriteFile(matrixPath, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Errorf("docs/sdks/behaviour-matrix.md is out of date: run go test -run TestBehaviourMatrixIsCurrent -update-matrix . in sdks/go")
}
