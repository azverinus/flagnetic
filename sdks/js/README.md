# Flagnetic SDK for JavaScript

`flagnetic` is the JavaScript and TypeScript SDK of Flagnetic. One npm package with subpath exports, ESM and CommonJS with types, no runtime dependency:

| Import | For |
|---|---|
| `flagnetic/server` | Node 22 and later, with a server key: downloads the ruleset, follows changes through the stream and evaluates flags locally, exactly as the server does |
| `flagnetic/client` | The browser, with a client key: remote evaluation, never the evaluator |
| `flagnetic/react` | React 18 and later bindings over the browser client |
| `flagnetic/openfeature` | OpenFeature providers for the server and the web SDK |

`react`, `@openfeature/server-sdk` and `@openfeature/web-sdk` are optional peer dependencies of the entry points that use them; every other entry point loads without them.

## Server mode

```ts
import { createServer } from "flagnetic/server";

const flagnetic = createServer(process.env.FLAGNETIC_SDK_KEY!, "https://flags.example.com");

// Wait at most two seconds for the first ruleset; it never rejects and
// tells the stage reached: "fresh", "last_good" (from the store) or "built_in".
await flagnetic.ready(2000);

const context = { units: { user: "u-1001" }, attributes: { country: "DE" } };
if (flagnetic.getBool(context, "new_checkout", false)) {
  // ...
}

// Why a value was served.
const detail = flagnetic.getDetail(context, "banner_text", "Welcome");
// detail.value, .variation, .reason, .errorCode, .rulesetVersion, .stage, .experiment

flagnetic.exposure(context, "banner_text"); // an experiment's exposure is explicit
flagnetic.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });

await flagnetic.close(); // flushes what is queued, within 5 seconds
```

`createServer` refuses a client key (`fn_cli_…`), which belongs to `flagnetic/client`; network trouble never makes it throw. A getter returns your built-in default instead of throwing whenever the flag is unknown, has another type or cannot be evaluated. The ruleset is downloaded with `If-None-Match`, the stream is held by default and a change reaches the client within 2 seconds at p99; with the stream down the client polls every 30 seconds, and with it up every 300. A `store` option names a file that keeps the last good ruleset across restarts.

Options: `pollInterval`, `pollIntervalWithoutStream`, `stream`, `requestTimeout`, `store`, `logger`, `allowOverrides`, `sendSummaries`, `sendExposures`, `flushInterval`, `summaryInterval`, `platform`, `appVersion` and `offline`. Requests carry `User-Agent: flagnetic-js/<version> (<platform>)`.

`onChange(listener)` and `onStage(listener)` return a function that removes the listener. `stage` and `status()` report where the fallback chain stands.

### Local overrides

```ts
const flagnetic = createServer(key, url, { allowOverrides: true });
flagnetic.overrides.set("new_checkout", true); // reads as reason "local_override"
```

`overrides.set` throws `LocalOverridesNotAllowed` without `allowOverrides` and `LocalOverrideRefused` while the ruleset belongs to a protected environment. Both log `override_refused`.

### Rendering on the server

`flagnetic.values(context, { flags? })` evaluates the flags for a context into the document a browser client takes as its `bootstrap`. It is masked as a client key's answer is: where targeting decided a value the reason is `targeting_match` and no rule is named, because the document is written into a page anyone can read.

### Tests

```ts
import { testServer, testServerFromValues } from "flagnetic/server";

const flagnetic = testServerFromValues({ new_checkout: true, limit: 10 });
flagnetic.set("new_checkout", false); // rewrite a value; change listeners fire
flagnetic.reload();                   // back to what the client was built from
flagnetic.recorder.exposures();       // exposures and aliases that would have been sent
```

`testServer(document)` builds the same offline client from a ruleset document. Neither makes a network request.

### Propagation

A change made in Flagnetic reaches a server client within 2 seconds at p99 while the stream is up, and within the poll interval otherwise.

## Evaluator

```ts
import { decodeRuleset } from "flagnetic/server";

const ruleset = decodeRuleset(document); // a ruleset document, as JSON text, bytes or a parsed value
const result = ruleset.evaluate("new_checkout", { units: { user: "u-1001" }, attributes: { country: "DE" } });
```

The regular expressions of `matches` conditions run on a linear-time RE2 engine of the package, so a pattern means what it means on the server and an attribute value cannot stall the process. A ruleset from a server newer than the package may hold an operator, condition kind or serve kind the evaluator does not know; `ruleset.unsupportedRules()` reports them by flag, and evaluating such a flag answers `error` with `inconsistent_ruleset`.

## Development

```bash
pnpm install
pnpm lint && pnpm typecheck
pnpm test            # the shared conformance suite and unit tests, in Node
pnpm test:browser    # the same suite in Chromium (pnpm exec playwright install chromium once)
pnpm test:p99        # 1,000 flags evaluate under 1 ms at p99
pnpm build && pnpm check:package
```

The Unicode class tables of the regular expression engine are generated from Go's `regexp/syntax`; after a Go upgrade run `pnpm gen:unicode`, and CI fails when they are stale.
