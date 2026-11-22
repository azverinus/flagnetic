package flagnetic

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The operations whose Deprecation header the client reads: the ruleset
// fetch, the ruleset stream and the events post.
const (
	operationRuleset = "ruleset"
	operationStream  = "stream"
	operationEvents  = "events"
)

// noteDeprecation logs deprecation_received the first time operation's
// answer carries a Deprecation header, with the Sunset when there is one.
// It never fails or delays a call: a header that cannot be read is logged
// with what it said, and every later answer to the operation is silent.
func (c *Client) noteDeprecation(operation string, h http.Header) {
	value := h.Get("Deprecation")
	if value == "" {
		return
	}
	if _, seen := c.deprecations.LoadOrStore(operation, struct{}{}); seen {
		return
	}
	fields := map[string]any{"operation": operation, "deprecation": value}
	if since, ok := parseDeprecation(value); ok {
		fields["deprecated_since"] = since.UTC().Format(time.RFC3339)
	}
	if sunset := h.Get("Sunset"); sunset != "" {
		fields["sunset"] = sunset
		if at, err := http.ParseTime(sunset); err == nil {
			fields["sunset"] = at.UTC().Format(time.RFC3339)
		}
	}
	c.log(slog.LevelWarn, EventDeprecationReceived, fields)
}

// parseDeprecation reads a Deprecation header: "@<unix seconds>", the RFC
// 9745 form, and reports false for anything else, such as the draft's
// "true".
func parseDeprecation(value string) (time.Time, bool) {
	secs, ok := strings.CutPrefix(strings.TrimSpace(value), "@")
	if !ok {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}
