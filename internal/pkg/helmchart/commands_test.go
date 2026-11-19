package helmchart_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helm waits for a hook Job within --timeout, five minutes unless set, while
// the migration Job may run for 15: a command shown without one fails an
// upgrade the Job then survives, and the next attempt deletes the running Job.
func TestHelmCommandsInTheDocumentationSetATimeout(t *testing.T) {
	files := []string{chartDir + "README.md"}
	err := filepath.WalkDir("../../../docs", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		inBlock := false
		var command []string
		flush := func() {
			if len(command) == 0 {
				return
			}
			full := strings.Join(command, " ")
			command = nil
			if !strings.HasPrefix(full, "helm install ") && !strings.HasPrefix(full, "helm upgrade ") {
				return
			}
			seen++
			if !strings.Contains(full, "--timeout") {
				t.Errorf("%s shows a command without --timeout: %s", file, full)
			}
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "```") {
				flush()
				inBlock = !inBlock
				continue
			}
			if !inBlock {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				flush()
				continue
			}
			continued := strings.HasSuffix(trimmed, "\\")
			command = append(command, strings.TrimSpace(strings.TrimSuffix(trimmed, "\\")))
			if !continued {
				flush()
			}
		}
	}
	if seen == 0 {
		t.Fatal("found no helm install or helm upgrade command in the documentation")
	}
}
