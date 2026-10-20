# Flagnetic SDK for Go

`github.com/azverinus/flagnetic/sdks/go` evaluates Flagnetic flags inside a Go service from a ruleset held in memory. It depends on the evaluator module `github.com/azverinus/flagnetic/eval` and the standard library, nothing else, and needs Go 1.26 or newer.

## Install

```bash
go get github.com/azverinus/flagnetic/sdks/go
```

## Quick start

An offline client reads a ruleset document, exactly as the ruleset endpoint of a Flagnetic instance serves it, from a file, a byte slice or a function of yours, and never touches the network:

```go
import flagnetic "github.com/azverinus/flagnetic/sdks/go"

c, err := flagnetic.New("", "", flagnetic.WithOfflineFile("ruleset.json"))
if err != nil {
	return err // the document could not be read or decoded
}
defer c.Close(context.Background())

fc := flagnetic.Context{
	Units:      map[string]string{"user": "u-1001"},
	Attributes: map[string]any{"country": "DE", "plan": "pro"},
}
if c.Bool(ctx, fc, "new_checkout", false) {
	// ...
}
```

`WithOffline(document)` takes the document itself and `WithOfflineSource(read)` a function returning it; `c.Reload()` reads the source again and swaps the ruleset in.

## Connecting to a server

```go
c, err := flagnetic.New(os.Getenv("FLAGNETIC_SDK_KEY"), "https://flags.example.com",
	flagnetic.WithStore("/var/lib/myservice/flagnetic-ruleset.json"))
if err != nil {
	return err // a client SDK key: the Go SDK needs a server key
}
defer c.Close(context.Background())

readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
stage := c.Ready(readyCtx) // fresh, last_good (from the store) or built_in
cancel()
```

- `New` fails only for a client SDK key (`fn_cli_`), which belongs in a browser or an app, never in a backend; network trouble never fails construction. It returns at once at stage `built_in`, or `last_good` when the store holds a ruleset, and fetches in the background. `Ready(ctx)` waits for the first fetch result or the context's deadline and returns the stage reached.
- **The fallback chain.** A 200 is accepted only when it parses, its `schema_version` is supported, its scope is `server` and its version is at least the held one; then the ruleset swaps atomically, the stage is `fresh`, the store is rewritten and `OnChange` fires. A 304 is `fresh`. A timeout, a network error, a 5xx, a 429, a corrupt, empty or lower-versioned document, a client-scope document or an unsupported schema (logged once as `schema_unsupported`) logs `fetch_failed` with a `cause`, keeps the held ruleset and moves `fresh` to `last_good`; the SDK retries with exponential backoff and full jitter from 1 s to 5 min. A refused key (401, 403) logs `key_revoked` once, keeps serving what it holds, and retries with the backoff capped at 15 min until the key works again, so rotating keys never flips your flags to their defaults.
- **Stages.** `Stage()`, `OnStage`, the `stage_changed` event and `Detail.Stage` report the same transitions; `Status()` adds `StaleSince`, set at the first failure after a success and cleared by the next success, for your own health page.
- **The change stream** is an SSE connection to `/sdk/v1/stream` carrying nothing but new ruleset versions, opened before the first fetch so that no change falls between the two, so a kill switch reaches the service within about two seconds. A version above the held one causes a fetch with `If-None-Match`; equal or lower is ignored; a replica still behind is asked again after 1, 2 and 4 s, and the held version never goes down. A drop logs `sse_dropped` and reconnects with `Last-Event-ID` set to the held version, backing off from 1 s to 30 s with full jitter (0–2 s after a server-side close), and 45 s without a frame reconnects; every reconnect is followed by a fetch. 401 and 403 close the stream for good and leave the fetch loop retrying at its capped interval. `WithChangeStream(false)` turns it off, for a proxy that blocks `text/event-stream`.
- **The safety poll** fetches with `If-None-Match` every 5 minutes while the change stream is connected (`WithPollInterval`, at least 30 s) and every 30 s while it is down or off. `WithRequestTimeout` (10 s) bounds each request; `WithHTTPClient` sends them through your client, and the stream runs through a copy of it without its `Timeout`. `Close(ctx)` stops both loops promptly, even during a backoff or a request.
- **Persistence.** `WithStore(path)` writes every accepted ruleset to a file readable by its owner only and loads it at construction as `last_good`, so a restart during an outage of the flag service still serves real flags. A corrupt file logs `store_failed` and starts at `built_in` rather than failing.
- The client is safe for concurrent use while it fetches: the ruleset swaps through an atomic pointer and evaluations never lock.

## Getters

Every getter takes a `context.Context` first, as any Go call on a request path does, then the flag context, the flag key and the built-in default:

```go
c.Bool(ctx, fc, "new_checkout", false)      // bool
c.String(ctx, fc, "greeting", "Hello")      // string
c.Number(ctx, fc, "discount", 0.1)          // float64
c.Int(ctx, fc, "page_size", 20)             // int64
c.JSON(ctx, fc, "theme", nil)               // json.RawMessage
d := c.Detail(ctx, fc, "new_checkout", false)
```

- Getters are **total**: they never panic and never return an error. A missing flag, a flag of another type (logged once per flag as `type_mismatch`), an evaluator error or a client holding no ruleset serves the built-in default with reason `fallback` and an error code.
- `Int` serves a number flag only when its value is whole and fits in an `int64`; otherwise it serves the default with `type_mismatch` rather than a truncated value.
- `Detail` tells why a value was served: the value, the evaluation reason, the variation, the rule, the error code, the ruleset version and when it was received, the stage of the fallback chain, whether a platform override decided it, and the experiment block when an experiment assigned it. The Go type of the default names the flag type asked for: `bool`, `string`, any Go number, or anything else for JSON.
- `OnChange` registers a listener called with the keys of the flags whose value may have changed, through their own configuration, a segment, a declared attribute or unit type, or a prerequisite they reach; `OnStage` one called on every stage transition. Both return a function that removes the listener, and a panicking listener is logged as `listener_failed`.
- Local overrides need `flagnetic.WithLocalOverrides()`: `c.LocalOverrides().Set/Remove/Clear/List` substitute a value inside this process for a developer's machine, read with reason `local_override`. A ruleset whose environment is protected refuses them.
- An offline client counts every evaluation in `c.Recorder()`, by flag, variation and reason.
- `flagnetic.WithLogger` receives every event with structured fields, or `flagnetic.SlogLogger` sends them to a `log/slog` logger; the default logs warnings and above through `log/slog`.

## Development

The repository root's `go.work` puts this module, the evaluator and the server in one workspace, so the SDK builds against the evaluator in the checkout.

```bash
cd sdks/go && go test -race ./...
```

`conformance_test.go` runs every fixture of `conformance/fixtures`, and the RE2 vectors, through `Detail` on an offline client. `protocol_test.go` runs the ruleset degradation cases and the server-mode scenarios of `conformance/protocol` against a mock server scripted from them, with the fetch loop's waits handed to the test, and fails when a server-mode scenario runs nowhere; `protocol_stream_test.go` runs the change stream scenarios, where two loops wait at once, on real time with every duration divided by one factor. `fetch_test.go` and `stream_test.go` drive the fallback chain and the stream against in-process mock servers, and `sse_internal_test.go` the line parser.
