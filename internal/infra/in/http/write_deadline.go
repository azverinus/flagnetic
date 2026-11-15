package http

import (
	"net/http"
	"time"
)

// writeChunk is the most one write hands the connection under one deadline.
// A reader that takes a large answer slowly but steadily keeps it alive chunk
// by chunk, while one that stops taking it is cut off after one timeout,
// however large the answer is.
const writeChunk = 64 << 10

// deadlineWriter gives every write and flush of a response the write timeout
// as its own deadline on the connection. The server sets none for the whole
// response, which would cut a stream off; rolling per write, a stalled client
// is dropped after one timeout whatever the route, and a stream lives as long
// as its frames are taken. The deadline is the connection's, so it is moved
// ahead again when the handler returns: net/http flushes what is still
// buffered after that, and a deadline left by the last write, perhaps long
// past, must not cut that flush short.
type deadlineWriter struct {
	http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
}

// withWriteDeadline returns w with every write bounded by timeout; w itself
// when timeout is not positive. conn is the connection's own writer, whose
// deadline is set. The returned func ends the response's deadlines and runs
// when the handler returns.
func withWriteDeadline(conn, w http.ResponseWriter, timeout time.Duration) (http.ResponseWriter, func()) {
	if timeout <= 0 {
		return w, func() {}
	}
	d := &deadlineWriter{ResponseWriter: w, rc: http.NewResponseController(conn), timeout: timeout}
	d.arm()
	return d, d.arm
}

// arm moves the connection's write deadline timeout ahead. A connection
// without deadlines, such as HTTP/2 in tests, is left as it is.
func (d *deadlineWriter) arm() { _ = d.rc.SetWriteDeadline(time.Now().Add(d.timeout)) }

func (d *deadlineWriter) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		chunk := b
		if len(chunk) > writeChunk {
			chunk = chunk[:writeChunk]
		}
		d.arm()
		n, err := d.ResponseWriter.Write(chunk)
		written += n
		if err != nil {
			return written, err
		}
		b = b[n:]
	}
	return written, nil
}

// Flush sends what is buffered, under a deadline of its own.
func (d *deadlineWriter) Flush() {
	d.arm()
	_ = http.NewResponseController(d.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (d *deadlineWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
