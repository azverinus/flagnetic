package testkit

import (
	"context"
	"log/slog"
	"sync"
)

// LogRecord is one record a LogSink captured.
type LogRecord struct {
	Level   slog.Level
	Message string
	// Attrs holds every attribute by its key, group names joined with dots
	// ("http.status"), with LogValuers resolved.
	Attrs map[string]any
}

// LogSink captures what a logger writes as structured records, at every level,
// so a test asserts on fields rather than on formatted text. It is safe for
// concurrent use: a server logs from many goroutines.
type LogSink struct {
	mu      sync.Mutex
	records []LogRecord
}

// NewLogSink returns an empty sink.
func NewLogSink() *LogSink { return &LogSink{} }

// Logger returns a logger writing into the sink.
func (s *LogSink) Logger() *slog.Logger { return slog.New(&sinkHandler{sink: s}) }

// Records returns every record captured so far.
func (s *LogSink) Records() []LogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogRecord(nil), s.records...)
}

// Find returns the records captured so far with the given message.
func (s *LogSink) Find(message string) []LogRecord {
	var out []LogRecord
	for _, r := range s.Records() {
		if r.Message == message {
			out = append(out, r)
		}
	}
	return out
}

type sinkHandler struct {
	sink   *LogSink
	attrs  []slog.Attr // already qualified by their groups
	prefix string
}

func (h *sinkHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *sinkHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		flatten(attrs, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		flatten(attrs, h.prefix, a)
		return true
	})
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	h.sink.records = append(h.sink.records, LogRecord{Level: r.Level, Message: r.Message, Attrs: attrs})
	return nil
}

func (h *sinkHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range as {
		a.Key = h.prefix + a.Key
		c.attrs = append(c.attrs, a)
	}
	return &c
}

func (h *sinkHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = h.prefix + name + "."
	return &c
}

func flatten(into map[string]any, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p = prefix + a.Key + "."
		}
		for _, g := range v.Group() {
			flatten(into, p, g)
		}
		return
	}
	if a.Key == "" {
		return
	}
	into[prefix+a.Key] = v.Any()
}
