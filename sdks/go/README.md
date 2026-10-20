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

`conformance_test.go` runs every fixture of `conformance/fixtures`, and the RE2 vectors, through `Detail` on an offline client.
