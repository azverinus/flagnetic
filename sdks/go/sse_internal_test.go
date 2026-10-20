package flagnetic

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// readAll decodes every frame of a transcript as "event/id/data", or
// "comment" for a heartbeat, and returns them with the reconnection time
// the server asked for.
func readAll(t *testing.T, transcript string) ([]string, time.Duration) {
	t.Helper()
	r := newSSEReader(strings.NewReader(transcript))
	var out []string
	for {
		f, err := r.next()
		if errors.Is(err, io.EOF) {
			return out, r.retry
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if f.Comment {
			out = append(out, "comment")
			continue
		}
		out = append(out, fmt.Sprintf("%s/%s/%s", f.Event, f.ID, f.Data))
	}
}

func TestSSEReaderReadsTheEventStreamFormat(t *testing.T) {
	// The preamble and first events of a real stream, with a comment
	// heartbeat, multi-line data, CRLF and CR line endings, an unknown
	// field and a line with no colon at all.
	const transcript = "retry: 3000\n" +
		": hb\n" +
		"\n" +
		"event: ruleset\r\n" +
		"id: 41\r\n" +
		"data: {\"version\":\r" +
		"data: 41}\r" +
		"unknown: ignored\n" +
		"comment\n" +
		"\n" +
		": hb\n\n" +
		"id: 42\n" +
		"data: {\"version\":42}\n\n"

	frames, retry := readAll(t, transcript)
	want := []string{
		"comment",
		"ruleset/41/{\"version\":\n41}",
		"comment",
		// The id field carries over to every later frame, and a block
		// naming no event dispatches with an empty name.
		"/42/{\"version\":42}",
	}
	if !reflect.DeepEqual(frames, want) {
		t.Errorf("frames = %q, want %q", frames, want)
	}
	if retry != 3*time.Second {
		t.Errorf("retry = %v, want 3s", retry)
	}
}

func TestSSEReaderIgnoresBlocksWithoutData(t *testing.T) {
	// A retry or an id on its own updates the reader and dispatches
	// nothing; the value after the colon keeps every space but the first.
	frames, retry := readAll(t, "retry: 500\n\nid: 7\n\ndata:  two spaces\n\n")
	if want := []string{"/7/ two spaces"}; !reflect.DeepEqual(frames, want) {
		t.Errorf("frames = %q, want %q", frames, want)
	}
	if retry != 500*time.Millisecond {
		t.Errorf("retry = %v, want 500ms", retry)
	}
}

func TestSSEReaderEndsOnAnUnterminatedBlock(t *testing.T) {
	// The server closed mid-block: nothing is dispatched, and the caller
	// sees the end of the stream rather than half an event.
	frames, _ := readAll(t, "event: ruleset\nid: 9\ndata: {\"vers")
	if len(frames) != 0 {
		t.Errorf("frames = %q, want none", frames)
	}
}

func TestScanSSELinesSplitsOnEveryLineEnding(t *testing.T) {
	// A CR at the end of what has arrived may yet be half of a CRLF, so
	// the split waits for the next byte unless the stream is over.
	if advance, token, _ := scanSSELines([]byte("a\r"), false); advance != 0 || token != nil {
		t.Errorf("a trailing CR split into (%d, %q), want a request for more", advance, token)
	}
	if advance, token, _ := scanSSELines([]byte("a\r"), true); advance != 2 || string(token) != "a" {
		t.Errorf("a trailing CR at the end split into (%d, %q), want (2, \"a\")", advance, token)
	}
	if advance, token, _ := scanSSELines([]byte("a\r\nb"), false); advance != 3 || string(token) != "a" {
		t.Errorf("CRLF split into (%d, %q), want (3, \"a\")", advance, token)
	}
}
