package postgres_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A migration that has shipped in a release is already applied in databases
// the project cannot reach, and goose never runs it again, so editing,
// renaming or removing it would leave those databases on a schema no fresh
// install has. migrations/released.sum lists the SHA-256 of every released
// file, in the format of sha256sum; this test fails when a listed file
// changed or is gone. A release appends the files it ships (see
// migrations/README.md); a migration not yet listed is still free to change.
func TestReleasedMigrationsAreUnchanged(t *testing.T) {
	sum, err := os.ReadFile(filepath.Join("migrations", "released.sum"))
	if err != nil {
		t.Fatal(err)
	}

	listed := 0
	for line := range strings.Lines(string(sum)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		want, name, ok := strings.Cut(line, "  ")
		if !ok || len(want) != 2*sha256.Size || filepath.Base(name) != name {
			t.Errorf("released.sum: malformed line %q, want \"<sha256>  <file name>\"", line)
			continue
		}
		listed++
		content, err := os.ReadFile(filepath.Join("migrations", name))
		if os.IsNotExist(err) {
			t.Errorf("released migration %s is gone: a released migration is never removed or renamed", name)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(content)
		if hex.EncodeToString(got[:]) != want {
			t.Errorf("released migration %s changed: a released migration is never edited, add a new migration instead", name)
		}
	}
	if listed == 0 {
		t.Error("released.sum lists no migration")
	}
}
