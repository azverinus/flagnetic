package testclock_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/out/testclock"
)

// The test clock reads ahead of the wall clock by the duration its file
// holds, read again on every call, so the harness moves a running binary
// by rewriting the file; no file, an empty one or a malformed one is no
// offset.
func TestOffsetClockReadsItsFileOnEveryCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clock-offset")
	c := testclock.New(path)

	for _, step := range []struct {
		name, content string
		want          time.Duration
		write         bool
	}{
		{name: "no file", want: 0},
		{name: "empty", content: "", write: true, want: 0},
		{name: "two minutes ahead", content: "2m\n", write: true, want: 2 * time.Minute},
		{name: "moved on", content: "3m30s", write: true, want: 3*time.Minute + 30*time.Second},
		{name: "malformed", content: "soon", write: true, want: 0},
	} {
		if step.write {
			if err := os.WriteFile(path, []byte(step.content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := c.Offset(); got != step.want {
			t.Errorf("%s: Offset = %s, want %s", step.name, got, step.want)
		}
		before := time.Now()
		now := c.Now()
		if lead := now.Sub(before); lead < step.want || lead > step.want+time.Second {
			t.Errorf("%s: Now is %s ahead of the wall clock, want %s", step.name, lead, step.want)
		}
	}
}
