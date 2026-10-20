package flagnetic

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
	"time"
)

// maxFrameBytes bounds one line of the change stream. The only event the
// stream carries is a version, so a longer line is a broken or hostile
// server and ends the connection.
const maxFrameBytes = 64 << 10

// sseFrame is one block of the change stream: a dispatched event, or a
// comment such as the server's heartbeat.
type sseFrame struct {
	// Comment is true for a line starting with a colon. Such a frame
	// carries no event; it proves the connection is alive.
	Comment bool
	// Event is the event name, empty when the block named none.
	Event string
	// ID is the most recent id field, carried on every frame after it.
	ID string
	// Data is the block's data lines joined by newlines.
	Data string
}

// sseReader decodes the event stream format over a response body: the
// fields event, id, data and retry, comments, multi-line data, and lines
// ended by LF, CRLF or CR. It is written by hand,
// so the SDK depends on the standard library alone.
type sseReader struct {
	lines *bufio.Scanner
	// retry is the reconnection time the server last asked for; zero until
	// it sends one.
	retry time.Duration
	// id is the last event ID seen, carried across blocks as the format
	// prescribes.
	id string
	// event and data accumulate the block being read.
	event   string
	data    bytes.Buffer
	hasData bool
}

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxFrameBytes)
	sc.Split(scanSSELines)
	return &sseReader{lines: sc}
}

// next returns the next frame: a dispatched event, or a comment. It blocks
// until the server writes one, and reports io.EOF once the server closes
// the stream.
func (r *sseReader) next() (sseFrame, error) {
	for {
		if !r.lines.Scan() {
			if err := r.lines.Err(); err != nil {
				return sseFrame{}, err
			}
			return sseFrame{}, io.EOF
		}
		line := r.lines.Text()
		switch {
		case line == "":
			// A blank line dispatches, unless the block held no data: an
			// id or a retry on its own only updates the reader.
			if !r.hasData {
				r.event = ""
				r.data.Reset()
				continue
			}
			f := sseFrame{
				Event: r.event,
				ID:    r.id,
				Data:  strings.TrimSuffix(r.data.String(), "\n"),
			}
			r.event = ""
			r.data.Reset()
			r.hasData = false
			return f, nil
		case strings.HasPrefix(line, ":"):
			// A comment is ignored by the format and never interrupts the
			// block being accumulated, but it proves the server is there.
			return sseFrame{Comment: true}, nil
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				r.event = value
			case "data":
				r.data.WriteString(value)
				r.data.WriteByte('\n')
				r.hasData = true
			case "id":
				if !strings.ContainsRune(value, 0) {
					r.id = value
				}
			case "retry":
				if ms, err := strconv.Atoi(value); err == nil && ms >= 0 {
					r.retry = time.Duration(ms) * time.Millisecond
				}
			}
		}
	}
}

// scanSSELines splits the stream into lines ended by LF, CRLF or CR, the
// three the event stream format allows, and yields the line without its
// ending.
func scanSSELines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		switch b {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			// A CR at the end of what has arrived may yet be half of a
			// CRLF: wait for the next byte.
			return 0, nil, nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
