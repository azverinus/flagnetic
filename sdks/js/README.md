# Flagnetic SDK for JavaScript

`flagnetic` is the JavaScript and TypeScript SDK of Flagnetic. One npm package with subpath exports, ESM and CommonJS with types, no runtime dependency:

| Import | For |
|---|---|
| `flagnetic/server` | Node 22 and later: the evaluator, which decodes a ruleset document and evaluates flags locally, exactly as the server does |
| `flagnetic/client` | The browser, with a client key: remote evaluation, never the evaluator |
| `flagnetic/react` | React 18 and later bindings over the browser client |
| `flagnetic/openfeature` | OpenFeature providers for the server and the web SDK |

`react`, `@openfeature/server-sdk` and `@openfeature/web-sdk` are optional peer dependencies of the entry points that use them; every other entry point loads without them.

## Evaluator

```ts
import { decodeRuleset } from "flagnetic/server";

const ruleset = decodeRuleset(document); // a ruleset document, as JSON text, bytes or a parsed value
const result = ruleset.evaluate("new_checkout", { units: { user: "u-1001" }, attributes: { country: "DE" } });
```

The regular expressions of `matches` conditions run on a linear-time RE2 engine of the package, so a pattern means what it means on the server and a user's input cannot stall the process. A ruleset from a server newer than the package may hold an operator, condition kind or serve kind the evaluator does not know; `ruleset.unsupportedRules()` reports them by flag, and evaluating such a flag answers `error` with `inconsistent_ruleset`.

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
