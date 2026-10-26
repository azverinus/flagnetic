# Flagnetic SDK for JavaScript

`flagnetic` is the JavaScript and TypeScript SDK of Flagnetic. One npm package with subpath exports, ESM and CommonJS with types, no runtime dependency:

| Import | For |
|---|---|
| `flagnetic/server` | Node 22 and later, with a server key: downloads the ruleset, follows changes through the stream and evaluates flags locally, exactly as the server does |
| `flagnetic/client` | The browser, with a client key: remote evaluation, never the evaluator |
| `flagnetic/react` | React 18 and later bindings over the browser client |
| `flagnetic/openfeature` | OpenFeature providers for the server and the web SDK |

`react`, `@openfeature/server-sdk` and `@openfeature/web-sdk` are optional peer dependencies of the entry points that use them; every other entry point loads without them.

```bash
npm install flagnetic
```

Supported: Node 22 and later (the releases Node still supports; a release reaching its end of life is dropped in a minor version), browsers that run ES2022, React 18 and later, `@openfeature/server-sdk` 1.23 and later, `@openfeature/web-sdk` 1.10 and later. The package speaks the SDK API `/sdk/v1` and works with every Flagnetic server from 0.1.0 on ([compatibility](../../docs/sdks/compatibility.md)); [docs/sdks/javascript.md](../../docs/sdks/javascript.md) is how to choose and deploy each entry point.

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

## Browser client

```ts
import { anonymousId, createClient } from "flagnetic/client";

const flagnetic = createClient("fn_cli_…", "https://flags.example.com", {
  context: { units: { anonymous: anonymousId() }, attributes: { country: "DE" } },
});

// Values serve at once: the bootstrap document, else the last good values
// this browser kept for the key and the context's units, else your defaults.
await flagnetic.ready(2000); // "fresh", "last_good" or "built_in"; never rejects

if (flagnetic.getBool("new_checkout", false)) {
  // ...
}

// After sign-in: resolves once the new context's values arrived, while the
// previous values keep serving, so there is no flash of defaults.
await flagnetic.setContext({ units: { anonymous: anonymousId(), user: "u-1001" } });
```

`createClient` refuses a server key (`fn_srv_…`), which must never ship in a bundle. The client asks the server to evaluate for its context and never contains the evaluator, so `flagnetic/client` stays within a gzip size budget checked in CI. It names itself to the server through the context's `sdk`, `sdk_version` and `platform` attributes, since a browser cannot set a `User-Agent`.

The last good values are kept in `localStorage` under the SDK key and the context's units (in memory where `localStorage` cannot be reached, or with `store: false`), so the next page load serves them before any request and two keys in one browser never see each other's values. A `bootstrap` option takes the document `values(context)` of `flagnetic/server` rendered into the page, served as `fresh`; an `offline` option takes a values document and makes no request at all, with `offline.set`, `offline.reload` and `offline.recorded()` for tests.

The client refetches when the tab becomes visible or the network comes back, at most once every 10 seconds, and polls every 300 seconds while the tab is visible. With `stream: true` it also holds the change stream, read over `fetch` so that it can send its key and `Last-Event-ID` in headers and reconnect by itself; it is off by default, since every open tab holds one connection.

`anonymousId(unitType = "anonymous")` gives this browser one ID per unit type: a random UUIDv7 minted on the first call and kept in `localStorage` for every later visit of the origin. It lives outside the key's storage, so a rotated or replaced client key keeps every anonymous visitor in the experiment arm their ID buckets into; where `localStorage` cannot be reached, the ID holds for the page. Pass the unit type your project declared for anonymous traffic, and keep the ID in the context's units after sign-in.

Exposures, aliases and evaluation summaries are queued in `localStorage` under the key and sent every 5 seconds while the tab is visible; `flush()` and `close()` send them within 5 seconds. Tabs of one origin with the same key share the queue, so what a closed tab left is sent by the next batch of any of them. When the tab is hidden or the page is closed, the client hands the queue to `navigator.sendBeacon`, which the browser delivers after the page is gone: the client key travels as the `sdk_key` query parameter and the envelope as `text/plain`, so no CORS preflight is needed, and what does not fit the browser's 64 KiB beacon quota stays queued for the next visit. Options: `context`, `bootstrap`, `offline`, `store`, `stream`, `pollInterval`, `requestTimeout`, `flushInterval`, `sendExposures`, `sendSummaries`, `allowOverrides`, `logger`, `platform` and `appVersion`. Getters, `getDetail`, `stage`, `status()`, `onChange`, `onStage`, `overrides`, `exposure(key)` and `alias` work as in server mode, without the context argument.

### Propagation

A change made in Flagnetic reaches a browser client at the latest after the 300-second poll while the tab is visible, at once when the tab becomes visible again or the network returns, and within seconds with the stream on.

## React

```tsx
import { FlagneticProvider, useFlag, useFlagDetail, useStage, useFlagnetic } from "flagnetic/react";

function App({ bootstrap }: { bootstrap?: object }) {
  return (
    <FlagneticProvider sdkKey="fn_cli_…" baseUrl="https://flags.example.com"
                       options={{ context: { units: { user: "u-1001" } } }}
                       bootstrap={bootstrap}>
      <Checkout />
    </FlagneticProvider>
  );
}

function Checkout() {
  const enabled = useFlag("new_checkout", false);  // re-renders when this flag's value changes
  const detail = useFlagDetail("banner_text", "Welcome"); // value, reason, stage, experiment
  const stage = useStage();                         // "built_in", "last_good" or "fresh"
  const client = useFlagnetic();                    // setContext, exposure, alias; undefined until mounted
  // ...
}
```

The provider takes a client the application built with `createClient`, which it never closes, or `sdkKey`, `baseUrl`, `options` and `bootstrap` to build and own one. Those four are read once, when the provider mounts, so an object literal in JSX does not rebuild the client; give the provider a `key` to start over. The hooks subscribe through `useSyncExternalStore`: a change re-renders the components that read the changed flag and nothing else, and a hook outside a provider throws.

To render on the server, pass the document `values(context)` of `flagnetic/server` produced as `bootstrap`, and write the same document into the page for the browser. The hooks serve it while the page renders on the server and while it hydrates, so the first render in the browser matches the HTML; the provider builds its client after hydration, from the same document, and the first remote evaluation happens then. Nothing is fetched while a page renders on the server. A `bootstrap` of `undefined`, what `values()` gives before the server mode has a ruleset, renders built-in defaults at stage `built_in` on both sides.

## OpenFeature

`flagnetic/openfeature` has a provider for each OpenFeature SDK: `FlagneticServerProvider` for `@openfeature/server-sdk` 1.23 and later in Node, `FlagneticWebProvider` for `@openfeature/web-sdk` 1.10 and later in the browser. Install the SDK of your side; the entry point loads neither at run time.

```ts
import { OpenFeature } from "@openfeature/server-sdk";
import { FlagneticServerProvider } from "flagnetic/openfeature";

await OpenFeature.setProviderAndWait(new FlagneticServerProvider(process.env.FLAGNETIC_SDK_KEY!, "https://flags.example.com"));
const client = OpenFeature.getClient();

const context = { targetingKey: "u-1001", country: "DE", units: { anonymous: "d-8f1c" } };
const enabled = await client.getBooleanValue("new_checkout", false, context);

client.track("exposure", context, { flag: "pricing" }); // an experiment's exposure is explicit
client.track("alias", context, { fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
```

```ts
import { OpenFeature } from "@openfeature/web-sdk";
import { FlagneticWebProvider } from "flagnetic/openfeature";

await OpenFeature.setProviderAndWait(new FlagneticWebProvider("fn_cli_…", "https://flags.example.com"), { targetingKey: "u-1001" });
const enabled = OpenFeature.getClient().getBooleanValue("new_checkout", false);
await OpenFeature.setContext({ targetingKey: "u-2002" }); // evaluated for the new context
```

Each provider builds and owns a client of its mode, `provider.client`, and takes that mode's options plus two of its own: `unitType`, the unit type the targeting key is a unit ID of (`user` by default), and `readyTimeout`, how long `initialize` waits for the first ruleset or values (10 seconds on the server, 5 in the browser). `initialize` never fails: at stage `built_in` every getter serves your default. The context attribute `units` adds unit IDs by unit type; every other attribute is an attribute, a `Date` sent as an RFC 3339 string. The web provider evaluates OpenFeature's context, so it takes no `context` option.

Reasons map as `reasonOf` does, the same in every Flagnetic provider: `rule`, `prerequisite_failed`, `override` and `targeting_match` are `TARGETING_MATCH`, `rollout` and `experiment` `SPLIT`, `fallthrough` `DEFAULT`, `off` `DISABLED`, `local_override` `STATIC`, and a reason a newer server adds `UNKNOWN`. A built-in default served instead is `ERROR` with `FLAG_NOT_FOUND`, `TYPE_MISMATCH` or `GENERAL`, the Flagnetic error code as message. The flag metadata carries the Flagnetic reason under `REASON_METADATA_KEY` (`flagnetic_reason`), with `stage`, `ruleset_version`, `variation`, `overridden` and an experiment's `experiment_key`, `experiment_variation`, `experiment_unit_type`, `experiment_unit_id`, `experiment_inclusion_bucket`, `experiment_variation_bucket` and `experiment_override`.

`track("exposure")` reports the exposure of the flag named by `flag`, for the context (the current one in the browser), as `exposure` does; `track("alias")` records an alias. Any other event name, or one missing what it needs, does nothing.

Events: `PROVIDER_CONFIGURATION_CHANGED` with the keys of the flags that may have changed; `PROVIDER_STALE` when the client moves to `last_good`; `PROVIDER_READY` when it is fresh again; `PROVIDER_ERROR` when the SDK key is revoked, kept until the key is accepted again. OpenFeature reports `READY` once `initialize` or a context change resolves, whatever was reached; right after it the provider sends `ERROR` or `STALE` when that is where the client stands.

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
pnpm test:browsers   # the browser client cross-origin and the React bindings in Chromium, Firefox and WebKit (FLAGNETIC_BROWSERS=webkit for one)
pnpm test:p99        # 1,000 flags evaluate under 1 ms at p99
pnpm build && pnpm check:package && pnpm check:size
pnpm test:e2e        # the smokes in Node and Chromium against two real replicas (below)
```

The smokes need the end-to-end harness of [`e2e/`](../../e2e/README.md) running: start `flagnetic-e2e up` with `FLAGNETIC_E2E_SEED` naming a file, then run `pnpm test:e2e` with the same `FLAGNETIC_E2E_SEED` and, unless `flagnetic-e2e` is on the `PATH`, `FLAGNETIC_E2E_CMD` naming it. They end by revoking the seed's keys, so bring the harness up again before running them a second time.

The Unicode class tables of the regular expression engine are generated from Go's `regexp/syntax`; after a Go upgrade run `pnpm gen:unicode`, and CI fails when they are stale.
