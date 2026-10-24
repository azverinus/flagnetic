"""The log events of a client and the hook that receives them."""

from __future__ import annotations

import logging
from collections.abc import Callable, Mapping
from typing import Any

#: Receives every log event with its level (a ``logging`` level), its name and its fields.
#: It is called synchronously, from any thread, and must be safe for concurrent use.
Logger = Callable[[int, str, Mapping[str, Any]], None]

# Event names are stable: alert on them.

#: info: a ruleset was accepted. Fields: source (server or store), ruleset_version, flags.
RULESET_LOADED = "ruleset_loaded"
#: warning: a ruleset fetch failed and the held ruleset keeps serving. Fields: cause (timeout,
#: network, http_status, unauthorized, parse, schema_unsupported, wrong_scope, version_downgrade),
#: error, and status for an HTTP status.
FETCH_FAILED = "fetch_failed"
#: info: the change stream is open. Fields: last_event_id, the held version the stream resumes from.
SSE_CONNECTED = "sse_connected"
#: info for a server-side close, warning otherwise: the change stream ended. Fields: cause
#: (server_closed, silence, timeout, network, http_status, unauthorized, malformed), error,
#: reconnect (false where the stream stays down), and status for an HTTP status.
SSE_DROPPED = "sse_dropped"
#: info to fresh, warning otherwise: the fallback chain moved. Fields: from, to.
STAGE_CHANGED = "stage_changed"
#: error: the ruleset endpoint answered 401 or 403; logged once until a fetch succeeds again.
#: Values keep serving and the fetch retries with a backoff capped at 15 minutes. Fields: status.
KEY_REVOKED = "key_revoked"
#: error: a ruleset carried a schema_version this SDK cannot read; logged once. Fields:
#: schema_version, supported.
SCHEMA_UNSUPPORTED = "schema_unsupported"
#: error: a flag of an accepted ruleset holds an operator, condition kind or serve kind this SDK
#: does not know, typically because the server is newer; logged once per flag and ruleset version.
#: Fields: flag, constructs (each as "<kind> <name>"), ruleset_version.
RULE_UNSUPPORTED = "rule_unsupported"
#: warning: the store file could not be loaded or saved. Fields: path, op (load or save), cause.
STORE_FAILED = "store_failed"
#: warning: a getter asked for a type other than the flag's; logged once per flag key. Fields:
#: flag, flag_type, requested_type.
TYPE_MISMATCH = "type_mismatch"
#: error: an internal failure was caught at the API boundary and the built-in default served.
#: Fields: flag, cause.
EVALUATION_FAILED = "evaluation_failed"
#: error: a listener raised. Fields: cause.
LISTENER_FAILED = "listener_failed"


def standard_logger(level: int, event: str, fields: Mapping[str, Any]) -> None:
    """The default hook: the event name and its fields on the ``flagnetic`` logger of ``logging``."""
    logger = logging.getLogger("flagnetic")
    if logger.isEnabledFor(level):
        rendered = " ".join(f"{key}={fields[key]!r}" for key in sorted(fields))
        logger.log(level, "%s %s", event, rendered)
