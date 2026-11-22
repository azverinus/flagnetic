package flagnetic

import (
	"log/slog"
	"net/http"
)

// The operations whose Deprecation header the client reads: the ruleset
// fetch, the ruleset stream and the events post.
const (
	operationRuleset = "ruleset"
	operationStream  = "stream"
	operationEvents  = "events"
)

// noteDeprecation logs operation_deprecated the first time operation's
// answer carries a Deprecation header, with the Sunset when there is one.
// It never fails or delays a call: a header of any form is logged
// as received, and every later answer to the operation is silent.
func (c *Client) noteDeprecation(operation string, h http.Header) {
	value := h.Get("Deprecation")
	if value == "" {
		return
	}
	if _, seen := c.deprecations.LoadOrStore(operation, struct{}{}); seen {
		return
	}
	fields := map[string]any{"operation": operation, "deprecation": value}
	if sunset := h.Get("Sunset"); sunset != "" {
		fields["sunset"] = sunset
	}
	c.log(slog.LevelWarn, EventOperationDeprecated, fields)
}
