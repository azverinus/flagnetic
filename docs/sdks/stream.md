---
title: "The ruleset stream"
description: "Each new ruleset version as soon as a replica has it, heartbeats, reconnection and propagation."
---

# The ruleset stream

An SDK holds the ruleset stream to learn of each new ruleset version of its environment within seconds, instead of waiting for its next poll.

```
GET /sdk/v1/stream
Authorization: Bearer fn_srv_…
Last-Event-ID: 1745040000000
```

Either kind of key may hold it: the stream carries versions only, never a document. On each version a server SDK downloads the [ruleset](ruleset.md) and a client SDK evaluates again. A browser's `EventSource` cannot set headers, so a client key may instead be sent as the query parameter `sdk_key` (`/sdk/v1/stream?sdk_key=fn_cli_…`). A server key in the query, known or not, is refused `403 server_key_in_query`: it would end up in logs and browser histories. Create a new key if one was ever sent that way.

## Frames

The answer is `text/event-stream` with `Cache-Control: no-cache` and `X-Accel-Buffering: no`. It is never compressed, whatever `Accept-Encoding` says (its frames are a few dozen bytes, and a compressor held for the stream's life would cost the replica memory for every open stream). Its first frame tells the SDK to reconnect after three seconds:

```
retry: 3000

event: ruleset
id: 1745040000000
data: {"version":1745040000000}

: hb

```

- The current version is sent at once when it is above `Last-Event-ID`, and not otherwise, so a reconnecting SDK neither misses nor repeats a version. A missing or unreadable `Last-Event-ID` counts as none.
- Each later version is sent as soon as the replica holding the stream has loaded it, whichever replica took the change, so the SDK's download right after finds it there. Versions sent only increase.
- Several changes arriving while the replica loads a ruleset are sent as one version, the newest.
- A heartbeat comment `: hb` every 15 seconds tells a quiet stream from a dead one; an SDK hearing nothing for 45 seconds reconnects. On each heartbeat the key is checked again: a revoked, expired or deleted key ends the stream, as does a client key that no longer lists the origin the stream was opened from, while a key that cannot be checked for now keeps it. A version the replica heard of but could not send, because loading the ruleset failed twice, is sent on the next heartbeat.

## How a stream ends

The server closes a stream after 60 minutes plus up to 5 minutes chosen at random, so streams spread over the replicas as they come and go and do not all reconnect at once. It also closes every stream when the replica begins to drain on shutdown, and answers a new stream that arrives meanwhile `503 unavailable` with `Retry-After` rather than open it only to close it, so the SDK backs off and its next attempt reaches a replica that stays. The SDK reconnects with `Last-Event-ID` and loses nothing. It treats a stream as stable once it has delivered a heartbeat: a stream that ends before the first one (whatever answered it) is a failure like a refused connection and backs off, doubling from 1 s up to 30 s with full jitter, and only a stable stream's close waits the routine 0–2 s and restarts the backoff. A stream refused `401` or `403` stays closed while the fetch is refused too, so an ended key never opens one. When the fetch still succeeds, the refusal was not the key's (a gateway that answers only the stream path, a replica misbehaving for a moment), and every SDK tries the stream again after the first successful fetch that follows the refusal (a client SDK: its first successful evaluate), so a service recovers its two-second propagation without a restart.

Behind a reverse proxy, turn response buffering off for `/sdk/v1/stream` (the `X-Accel-Buffering: no` header does it for nginx) and give it a read timeout above the 15-second heartbeat.

## Propagation

The declared propagation time of a server SDK, from the commit of a change until the SDK holds the new ruleset, whichever replica the change and the SDK each go through:

| The SDK | At most |
|---|---|
| holds the stream | 2 seconds at the 99th percentile |
| does without it (switched off, or blocked by a proxy) | its safety poll: 30 seconds for every server SDK (Go, JavaScript server mode, Python and Ruby) |

The first row holds while the replica holding the ruleset stream has its `rulesets` notification listener up, which needs a direct connection to PostgreSQL or a pooler in session mode. A kill switch is a change like any other and takes the same time. The end-to-end tests check the first row on every pull request to Flagnetic: a kill switch saved through one replica must reach a Go SDK streaming from another within 2 seconds, and the end-to-end tests of the JavaScript server mode, Python and Ruby SDKs assert the same 2 seconds on every pull request that changes them, the Dart client's asserting its 5 seconds.

While their stream is connected, every server SDK also polls every 5 minutes, which catches a version the ruleset stream missed, for example from a replica whose notification listener is down (see [Delivery across replicas](../operations.md#delivery-across-replicas)). `flagnetic_ruleset_propagation_seconds` measures the server's part, from the commit to each replica's first answer carrying the version. Client SDKs, which evaluate remotely, declare their own propagation time with their release.
