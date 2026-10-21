# Flagnetic SDK for Go

`github.com/azverinus/flagnetic/sdks/go` evaluates Flagnetic flags inside a Go service from a ruleset held in memory. It depends on the evaluator module `github.com/azverinus/flagnetic/eval` and the standard library, nothing else, and needs Go 1.26 or newer.

## Install

```bash
go get github.com/azverinus/flagnetic/sdks/go
```

On OpenFeature? Install the [OpenFeature provider](../go-openfeature/README.md) instead, `github.com/azverinus/flagnetic/sdks/go-openfeature`, a module of its own over this SDK.

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
- An evaluation stays **under 1 ms at the 99th percentile** on a 1000-flag ruleset with attribute conditions of every type, segments, prerequisites, rollouts and experiments; CI measures it on every push and fails above it, so a getter belongs in a hot path. `go test -bench . ./...` profiles one evaluation and every flag at once.
- `Int` serves a number flag only when its value is whole and fits in an `int64`; otherwise it serves the default with `type_mismatch` rather than a truncated value.
- `Detail` tells why a value was served: the value, the evaluation reason, the variation, the rule, the error code, the ruleset version and when it was received, the stage of the fallback chain, whether a platform override decided it, and the experiment block when an experiment assigned it. The Go type of the default names the flag type asked for: `bool`, `string`, any Go number, or anything else for JSON.
- `OnChange` registers a listener called with the keys of the flags whose value may have changed, through their own configuration, a segment, a declared attribute or unit type, or a prerequisite they reach; `OnStage` one called on every stage transition. Both return a function that removes the listener, and a panicking listener is logged as `listener_failed`.
- Local overrides need `flagnetic.WithLocalOverrides()`: `c.LocalOverrides().Set/Remove/Clear/List` substitute a value inside this process for a developer's machine, read with reason `local_override`. A ruleset whose environment is protected refuses them.
- An offline client counts every evaluation in `c.Recorder()`, by flag, variation and reason.
- `flagnetic.WithLogger` receives every event with structured fields, or `flagnetic.SlogLogger` sends them to a `log/slog` logger; the default logs warnings and above through `log/slog`.

## Testing your code with `flagnetictest`

`github.com/azverinus/flagnetic/sdks/go/flagnetictest` builds offline clients for your unit tests, so they need no server:

```go
func TestCheckout(t *testing.T) {
	c := flagnetictest.FromValues(t, map[string]any{"new_checkout": true}) // each value served as the fallthrough
	handler := NewHandler(c.Client)
	// ... the new checkout

	c.Set("new_checkout", false) // one variation for everyone; a JSON flag stays JSON
	// ... the old checkout

	c.Reload() // back to what the client was built from
}
```

- `flagnetictest.New(t, document)` takes a ruleset document as the ruleset endpoint serves it, `FromFile(t, path)` reads one, and `FromValues(t, values)` builds one flag per value, its type from the value's Go type.
- Options pass through to the client, so `flagnetic.WithLocalOverrides()` makes local overrides available as in your service.
- `c.AssertExposure("checkout_v2")` fails the test unless exactly one exposure of that flag was recorded and returns it; `c.Recorder()` holds every count, exposure and alias.

## Logger events

Event names are stable: filter and alert on them.

| Event | Level | When |
|---|---|---|
| `ruleset_loaded` | info | a ruleset was accepted (`source`: server, store or offline) |
| `fetch_failed` | warn | a fetch failed and the held ruleset keeps serving (`cause`) |
| `stage_changed` | info, warn | the fallback chain moved (`from`, `to`) |
| `key_revoked` | error | the SDK key was refused; values keep serving |
| `schema_unsupported` | error | a ruleset of a schema this SDK cannot read; logged once |
| `rule_unsupported` | error | a flag holds an operator, condition kind or serve kind this SDK does not know; logged once per flag and ruleset version |
| `sse_connected`, `sse_dropped` | info, warn | the change stream opened or ended |
| `ingest_failed` | warn | an events batch will be sent again |
| `queue_dropped` | warn | events or counts were given up on |
| `exposure_skipped` | debug | an exposure call sent nothing (`cause`) |
| `exposures_disabled` | warn | the client was built with `WithExposures(false)` |
| `store_failed` | warn | the store file could not be loaded or saved |
| `override_set`, `override_refused`, `local_overrides_allowed` | warn | local overrides |
| `type_mismatch` | warn | a getter asked for another type than the flag's; once per flag |
| `evaluation_failed`, `listener_failed` | error | a failure was contained at the API boundary or in a listener |

**`rule_unsupported`** means the server is newer than this SDK: a flag uses an operator, a condition kind or a serve kind this SDK's evaluator does not know. The line names the flag, its constructs (for example `operator glob_matches`) and the ruleset version. Wherever evaluating the flag reaches the construct it serves your built-in default with `inconsistent_ruleset`; every other flag keeps working. The fix is to upgrade the SDK.

## Exposures, aliases and evaluation summaries

```go
d := c.Detail(ctx, fc, "checkout_v2", false)
if d.Value == true {
	renderNewCheckout()
	c.Exposure(ctx, fc, "checkout_v2") // where the variation changes what the unit sees
}
c.Alias(ctx, "anonymous", "d-8f1c", "user", "u-1001") // at sign-up
```

- **An evaluation is never an exposure**, so being assigned a variation is never mistaken for having seen it. `Exposure(ctx, fc, key)` looks up the most recent detail of that flag for that flag context and records an exposure only when it carries an experiment block; otherwise nothing is sent and `exposure_skipped` names the cause at debug: `no_experiment` (no evaluation of this flag has carried one), `no_detail` (one has, but not for this context) or `repeat`. A later ruleset does not invalidate what was remembered, but an evaluation that no longer carries an experiment block does, so a stopped experiment stops being reported.
- The exposure carries a UUIDv7 event ID chosen at that moment and unchanged across every retry, `experiment_key`, `flag_key`, `variation_key`, `unit_type`, `unit_id`, `platform` and `app_version` from the context's attributes (null when absent), `evaluation_reason` (`experiment`, or `override` when a platform override decided it), `override` from the experiment block, `ruleset_version` and `occurred_at`; the SDK's name and version are the envelope's.
- **Automatic exposure.** A flag whose `automatic_exposure` is set records one after every evaluation yielding an experiment block, with no `Exposure` call, for backend code where evaluation is the exposure; `fallback` and `local_override` never do. Repeats per experiment, unit and variation are suppressed by a bounded in-process set for at least the flush interval, and the platform's deduplication stays what analysis relies on.
- `Alias(ctx, fromUnitType, fromUnitID, toUnitType, toUnitID)` records that two unit IDs belong to the same person and changes no context: an experiment crossing registration keeps hashing on the unit type it declared, and the alias is what lets analysis join the two.
- Exposures and aliases share one in-memory queue flushed every 2 s (`WithFlushInterval`) or at 500 events, holding 10 000 and dropping the oldest with `queue_dropped`. A batch answered 429, a 5xx or nothing at all is sent again whole, with the same event IDs, backing off from 1 s to 5 min with full jitter and logging `ingest_failed`; the queue backs off on its own rather than honouring `Retry-After`, so a rate limit naming minutes does not hold exposures back for minutes. A batch still unanswered when the flush interval is up stays queued and its backoff carries over. Events the platform rejected are never sent again. A refused key holds the queue back at a backoff growing to 15 min; any other 4xx drops the batch. `WithExposures(false)` turns both calls off and logs `exposures_disabled` at construction.
- **Evaluation summaries.** Every evaluation increments a counter keyed by flag, variation and evaluation reason, `fallback` and `local_override` included. Every 60 s (`WithSummaryInterval`) the window becomes one evaluation summary with a UUIDv7 event ID, the interval's bounds, the ruleset version held at its end and the counts, gzipped into the events envelope with this SDK's name, version and platform. No unit ID, attribute or flag value is ever in it, so reporting usage never says who your users are. More than 1000 rows split into several events, and nothing evaluated is nothing sent. 429 (honouring `Retry-After`), a 5xx and a network error send the batch again with the same event IDs; a batch still unsent when the interval is up merges into the next window, and a window kept for over an hour is dropped rather than delivered late. A refused key logs `key_revoked` once and holds reporting back at a capped backoff, so a restored key recovers without a redeploy. `WithSummaries(false)` turns reporting off, and offline mode implies it.
- `Flush(ctx)` sends the queue and the current window and returns within its context; `Close(ctx)` flushes within five seconds or its context, then stops.
- An offline client sends nothing: `c.Recorder()` holds `Summary()`, the running counts, `EvaluationSummaries()`, the reports a flush would have sent, and `Exposures()` and `Aliases()`, the events the calls recorded, suppression included.

## Development

The repository root's `go.work` puts this module, the evaluator and the server in one workspace, so the SDK builds against the evaluator in the checkout.

```bash
cd sdks/go && go test -race ./...
```

`conformance_test.go` runs every fixture of `conformance/fixtures`, and the RE2 vectors, through `Detail` on an offline client. `protocol_test.go` runs the ruleset degradation cases and the server-mode scenarios of `conformance/protocol` against a mock server scripted from them, with the fetch loop's waits handed to the test, and fails when a server-mode scenario runs nowhere; `protocol_stream_test.go` runs the change stream scenarios, where two loops wait at once, on real time with every duration divided by one factor. `fetch_test.go` and `stream_test.go` drive the fallback chain and the stream against in-process mock servers, and `sse_internal_test.go` the line parser. `summaries_test.go` drives the evaluation summary and the events client: the envelope, the retry policy with identical event IDs, the window that merges, ages out and splits, and the body that carries no unit ID. `exposures_test.go` drives exposures, automatic exposure, the suppression of repeats, aliases and the bounded queue against the same kind of mock, and `exposures_internal_test.go` the context fingerprint an exposure is looked up by.
