package load

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// ReadVersions reads a ruleset stream and calls onVersion with the version
// of every `ruleset` frame, until the stream ends. Comments, the `retry`
// frame and frames of other events are skipped. It returns the error that
// ended the stream, nil for a clean end.
func ReadVersions(r io.Reader, onVersion func(version int64)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			event = ""
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:") && event == "ruleset":
			var d struct {
				Version int64 `json:"version"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &d) == nil && d.Version > 0 {
				onVersion(d.Version)
			}
		}
	}
	return sc.Err()
}
