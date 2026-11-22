---
title: "Go SDK"
description: "The Go modules flagnetic, flagnetictest and the OpenFeature provider: a quickstart, what 1.0 promises, and how soon a change reaches a service."
---

# Go SDK

Three modules, each released on its own tag:

| Module | Directory | What it is | Needs |
|---|---|---|---|
| `github.com/azverinus/flagnetic/sdks/go` | [`sdks/go`](../../sdks/go/README.md) | The SDK: server mode and offline mode, with `flagnetictest` for unit tests | Go 1.26 or newer; the evaluator module and the standard library, nothing else |
| `github.com/azverinus/flagnetic/sdks/go-openfeature` | [`sdks/go-openfeature`](../../sdks/go-openfeature/README.md) | The [OpenFeature](https://openfeature.dev) provider over the SDK | the SDK and `github.com/open-feature/go-sdk` |
| `github.com/azverinus/flagnetic/eval` | [`eval`](../../eval/README.md) | The evaluator the SDK and the server share | the standard library |

The SDK runs in server mode only: it takes a server key (`fn_srv_…`), downloads the environment's whole [ruleset](ruleset.md) and evaluates every flag inside the process, with the true evaluation reason. A client key is refused at construction.

## Quickstart

Install the module:

```bash
go get github.com/azverinus/flagnetic/sdks/go
```

Create a server key for the environment in the console (see [SDK keys](keys.md)) and keep it in your service's secrets, never in a browser or an app. Connect with the key and the base URL of your Flagnetic instance, and wait for the first ruleset for as long as your start-up can afford; `Ready` never fails, it returns the stage the client reached when the first fetch ended or the context expired:

```go
c, err := flagnetic.New(os.Getenv("FLAGNETIC_SDK_KEY"), "https://flags.example.com")
if err != nil {
	return err // a client SDK key: this SDK needs a server key
}
defer c.Close(context.Background())

readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
defer cancel()
stage := c.Ready(readyCtx) // fresh, last_good or built_in
```

Network trouble is never a construction error. A client that has no ruleset yet is at stage `built_in` and serves the default you pass at each call, so a service starts and answers even while the platform is unreachable; `WithStore(path)` keeps the last ruleset on disk for a restart during such an outage.

Describe who or what the evaluation is for with a context: the unit IDs by unit type, which experiments and rollouts hash on, and the attributes your rules read:

```go
fc := flagnetic.Context{
	Units:      map[string]string{"user": "u-1001"},
	Attributes: map[string]any{"country": "DE", "plan": "pro"},
}
```

Evaluate a flag with the getter of its type: `Bool`, `String`, `Number`, `Int` or `JSON`. Every getter takes the `context.Context` of the request first, then the context above, the flag key and the built-in default. Getters never panic and never return an error; a flag that cannot be served gives the default:

```go
if c.Bool(ctx, fc, "new_checkout", false) {
	fmt.Println("serving the new checkout")
}
```

`Detail` also says why the value was served: the evaluation reason, the variation, the rule, the ruleset version and the stage of the fallback chain.

```go
d := c.Detail(ctx, fc, "checkout_copy", "Buy now")
fmt.Println(d.Value, d.Reason, d.Variation)
```

Evaluating is not an exposure. Call `Exposure` where the served variation actually reaches the unit, so that being assigned is never mistaken for having seen it; it records an exposure only when the flag was served by an experiment:

```go
label := c.String(ctx, fc, "checkout_copy", "Buy now")
// ... render the button with label, then record that the unit saw it:
c.Exposure(ctx, fc, "checkout_copy")
```

When an anonymous visitor signs up, link their two IDs so that analysis can join what each did; an alias changes no context:

```go
c.Alias(ctx, "anonymous", "d-8f1c", "user", "u-1001")
```

On shutdown, `Close` sends the exposures, aliases and evaluation counts still queued, within five seconds or the context's deadline, and stops the background loops. Getters keep serving the held ruleset after it:

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := c.Close(shutdownCtx); err != nil {
	log.Printf("flagnetic: events not delivered: %v", err)
}
```

Every snippet above is compiled by the package's tests, and the evaluating ones are run against a ruleset. The [package's README](../../sdks/go/README.md) covers every option, the log events, local overrides, offline mode and the test helpers; the [OpenFeature provider](../../sdks/go-openfeature/README.md) has its own README.

## What 1.0 promises

From 1.0.0 the public API of the three modules changes only additively until 2.0, and the README of each module names what is public. Anything not named there is internal and may change in any release.

## Propagation

| The client | A change reaches it within |
|---|---|
| holds the [ruleset stream](stream.md) (the default) | 2 seconds at the 99th percentile |
| runs with `WithStream(false)`, or behind a proxy that blocks `text/event-stream` | 30 seconds, its poll interval without the stream |

While the stream is connected the client also polls every 5 minutes (`WithPollInterval`).

## Deprecations

When the platform answers an operation with a `Deprecation` header (see [Compatibility and support](../compatibility.md#deprecation-and-removal)), the client logs `deprecation_received` once per operation (`ruleset`, `stream` or `events`) at warning level, with the `Sunset` when there is one, and the call goes on as before. Alert on that name to learn of a deprecation before a removal.

## Compatibility

`flagnetic` for Go reads ruleset `schema_version` 1, speaks `/sdk/v1` and works with every Flagnetic server from 0.1.0 on; see the [compatibility table](compatibility.md). The [behaviour matrix](behaviour-matrix.md) shows which protocol scenario the Go SDK passes.
