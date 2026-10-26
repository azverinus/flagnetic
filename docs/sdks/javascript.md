# JavaScript SDK

`flagnetic` on npm is the JavaScript and TypeScript SDK: one package with no runtime dependency, ESM and CommonJS builds with type declarations, and an entry point per place it runs. The [package README](../../sdks/js/README.md) is the API reference; this page is how to choose and deploy it.

```bash
npm install flagnetic
```

| Entry point | Runs in | Key | Evaluates |
|---|---|---|---|
| `flagnetic/server` | Node 22 and later | server key (`fn_srv_…`) | locally, from the ruleset, with the true reason |
| `flagnetic/client` | browsers that run ES2022 | client key (`fn_cli_…`) | on the server, for the context it posts |
| `flagnetic/react` | React 18 and later, over the browser client | client key | as the browser client |
| `flagnetic/openfeature` | `@openfeature/server-sdk` 1.23 and later, `@openfeature/web-sdk` 1.10 and later | either, by provider | as the client it builds |

`react`, `@openfeature/server-sdk` and `@openfeature/web-sdk` are optional peer dependencies: install the ones of the entry points you import. Each entry point refuses the other kind of key at construction, so a server key never ends up in a bundle and a client key never in a backend (see [SDK keys](keys.md)). The package speaks `/sdk/v1` and reads `schema_version` 1 (see [Versioning and compatibility](compatibility.md)).

## Server mode

`createServer(serverKey, baseUrl)` downloads the environment's [ruleset](ruleset.md), follows the [stream](stream.md) and evaluates every flag in the process, so a getter costs no request and stays under a millisecond at p99. `ready(timeout)` waits for the first ruleset and tells the stage reached (`fresh`, `last_good` from the `store` file, or `built_in`); it never rejects, and a getter serves your built-in default whenever it cannot evaluate. Exposures, aliases and evaluation summaries are queued and posted to [events](events.md); `close()` sends what is queued within 5 seconds, so call it when the process shuts down.

## Browser client

`createClient(clientKey, baseUrl, { context })` posts its context to [remote evaluation](evaluate.md) and serves the values it is answered, never a rule: where targeting decided a value the reason is `targeting_match`. The last good values are kept in `localStorage` per key and context, so a returning visitor is served them before any request. `anonymousId()` gives the browser one ID per unit type, kept across visits and key rotations, for anonymous traffic.

The page and the Flagnetic server are usually on different origins. The SDK API answers CORS for client keys, so nothing needs configuring on the page's side; restrict a client key to your origins with its [allowed origins](keys.md#allowed-origins). When the tab is hidden or the page closes, queued events go out with `navigator.sendBeacon`, the key in the query and the body as `text/plain`, so no preflight is needed and a short visit's exposures are not lost.

## React and server-side rendering

`FlagneticProvider` builds a browser client after hydration, or follows one you pass, and `useFlag`, `useFlagDetail`, `useStage` and `useFlagnetic` read it through `useSyncExternalStore`. To render on the server, evaluate with the server mode's `values(context)`, pass that document to the provider as `bootstrap` and write it into the page: the HTML and the first render in the browser then agree, and nothing is fetched while the page renders on the server. `values()` answers what a client key would be answered, `targeting_match` and no rule identifier, since the document is readable by anyone who opens the page.

## OpenFeature

`FlagneticServerProvider` (Node) and `FlagneticWebProvider` (browser) each build and own a client of their mode. The targeting key is the unit ID of the provider's `unitType` (`user` by default), the `units` attribute adds unit IDs by unit type, and every other attribute is an attribute. Reasons map the same way in every Flagnetic provider; the Flagnetic reason itself is in the flag metadata under `flagnetic_reason`. `track("exposure", context, { flag })` and `track("alias", …)` are the exposure and the alias.

## Propagation

How soon a change made in Flagnetic is served:

| Where | Within |
|---|---|
| Server mode, stream up | 2 seconds at p99 |
| Server mode, stream down | the poll interval, 30 seconds by default |
| Browser client, tab visible | at most 300 seconds, the poll interval |
| Browser client, tab made visible again or the network back | at once (refetches at most once every 10 seconds) |
| Browser client with `stream: true` | seconds; every open tab holds one connection, so it is off by default |

## Tests and offline use

`testServer(ruleset)` and `testServerFromValues(values)` in `flagnetic/server`, and the browser client's `offline` option, make no request at all: set a value, reload, and read the exposures and aliases that would have been sent. Local overrides need `allowOverrides: true` and are refused in a protected environment.

## Releases

The package is released from `sdks/js` by its own release pull request, tagged `sdks/js/vX.Y.Z`, and published to npm with a provenance attestation that ties each version to the workflow run that built it. Every pull request that touches the SDK runs its conformance suite and protocol scenarios on Node 22 and 24, the browser client in Chromium, Firefox and WebKit, the package and size checks, and the smokes in Node and Chromium against two real replicas.
