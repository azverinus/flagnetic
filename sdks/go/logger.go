package flagnetic

import (
	"context"
	"log/slog"
	"sort"
)

// Event names a log event. Names are stable: alert on them.
type Event string

// Log events.
const (
	// EventRulesetLoaded (info): a ruleset was accepted. Fields: source
	// (server, store or offline), ruleset_version, flags.
	EventRulesetLoaded Event = "ruleset_loaded"
	// EventFetchFailed (warn): a ruleset fetch failed and the held ruleset
	// keeps serving. Fields: cause (timeout, network, http_status,
	// unauthorized, parse, schema_unsupported, wrong_scope,
	// version_downgrade), error, and status for an HTTP status.
	EventFetchFailed Event = "fetch_failed"
	// EventSSEConnected (info): the change stream is open. Fields:
	// last_event_id, the held version the stream resumes from, 0 for none.
	EventSSEConnected Event = "sse_connected"
	// EventSSEDropped (info for a server-side close, warn otherwise): the
	// change stream ended. Fields: cause (server_closed, silence, timeout,
	// network, http_status, unauthorized, malformed), error, reconnect
	// (false where the stream stays down), and status for an HTTP status.
	EventSSEDropped Event = "sse_dropped"
	// EventStageChanged (info to fresh, warn otherwise): the fallback chain
	// moved. Fields: from, to.
	EventStageChanged Event = "stage_changed"
	// EventKeyRevoked (error): the ruleset endpoint answered 401 or 403;
	// logged once until a fetch succeeds again. Values keep serving and the
	// fetch retries with a backoff capped at 15 minutes. Fields: status.
	EventKeyRevoked Event = "key_revoked"
	// EventSchemaUnsupported (error): a ruleset carried a schema_version this
	// SDK cannot read; logged once. Fields: schema_version, supported
	// (every schema_version from schemaVersionMin to schemaVersionMax).
	EventSchemaUnsupported Event = "schema_unsupported"
	// EventIngestFailed (warn): an ingest batch was answered 429, a 5xx or
	// nothing at all, and will be sent again with the same event IDs after
	// retry_in. Fields: kind, events (how many), retry_in, status for an
	// HTTP status and error.
	// It exists because a retry nobody can see is a retry nobody can alert on.
	EventIngestFailed Event = "ingest_failed"
	// EventExposureSkipped (debug): an exposure call sent nothing.
	// Fields: flag, cause (no_experiment when no recent evaluation of that
	// flag for that context carried an experiment block, repeat when the
	// same unit was already recorded seeing this variation).
	// It exists because "nothing was sent" is what a developer
	// who called Exposure needs to see.
	EventExposureSkipped Event = "exposure_skipped"
	// EventExposuresDisabled (warn): the client was constructed with
	// WithExposures(false), so no exposure and no alias leaves the
	// process.
	EventExposuresDisabled Event = "exposures_disabled"
	// EventQueueDropped (warn): counts were given up on rather than sent
	// again — a batch the platform refused with a 4xx other than 429,
	// events it rejected, rows it dropped for naming a flag it does not
	// have, or a window whose interval began over an hour ago. Fields:
	// kind, cause, the event IDs or row count given up on, and the counts
	// of a 202 answer where there was one.
	EventQueueDropped Event = "queue_dropped"
	// EventStoreFailed (warn): the store file could not be loaded or saved.
	// Fields: path, op (load or save), cause.
	EventStoreFailed Event = "store_failed"
	// EventOverrideSet (warn): a local override took effect. Fields: flag.
	// The substituted value is never logged.
	EventOverrideSet Event = "override_set"
	// EventOverrideRefused (warn): a local override was refused because the
	// environment is protected. Fields: flag, environment.
	EventOverrideRefused Event = "override_refused"
	// EventLocalOverridesAllowed (warn): the client was constructed with
	// WithLocalOverrides.
	EventLocalOverridesAllowed Event = "local_overrides_allowed"
	// EventTypeMismatch (warn): a getter asked for a type other than the
	// flag's; logged once per flag key. Fields: flag, flag_type,
	// requested_type.
	EventTypeMismatch Event = "type_mismatch"
	// EventEvaluationFailed (error): an internal failure was caught at the
	// API boundary and the built-in default served. Fields: flag, cause.
	EventEvaluationFailed Event = "evaluation_failed"
	// EventListenerFailed (error): a change listener panicked. Fields: cause.
	EventListenerFailed Event = "listener_failed"
)

// Logger receives every log event with its level and structured fields.
// It is called synchronously and must be safe for concurrent use.
type Logger func(level slog.Level, event Event, fields map[string]any)

// SlogLogger sends events at min and above to l, the event name as the
// message and the fields as attributes. A nil l means slog.Default() at the
// time of each event. The default Logger is SlogLogger(nil, slog.LevelWarn).
func SlogLogger(l *slog.Logger, minLevel slog.Level) Logger {
	return func(level slog.Level, event Event, fields map[string]any) {
		if level < minLevel {
			return
		}
		logger := l
		if logger == nil {
			logger = slog.Default()
		}
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrs := make([]slog.Attr, 0, len(keys))
		for _, k := range keys {
			attrs = append(attrs, slog.Any(k, fields[k]))
		}
		logger.LogAttrs(context.Background(), level, string(event), attrs...)
	}
}

// log calls the configured Logger; a panicking Logger is swallowed so that
// no public code path panics.
func (c *Client) log(level slog.Level, event Event, fields map[string]any) {
	defer func() { _ = recover() }()
	c.logger(level, event, fields)
}
