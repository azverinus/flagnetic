// Server rendering: the server mode produces a values document for a
// context, the provider serves it, and the page comes out with the
// platform's values in it. No client is built and no request is made while a
// page is rendered, which is what lets the same tree run on a server at all.
//
// The other half, hydrating that HTML in a real browser and finding the same
// values, is `react.browser.test.tsx`.
import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test, vi } from "vitest";
import { FlagneticProvider, useFlag, useFlagDetail, useFlagnetic, useStage } from "../src/react.js";
import { createServer } from "../src/server.js";
import { ruleset } from "./support/flags-fixture.js";

const user = { units: { user: "u-1" }, attributes: { country: "DE" } };

function Page(): ReactElement {
  const checkout = useFlag("checkout", false);
  const copy = useFlag("paywall_copy", "Buy");
  const theme = useFlag("theme", { mode: "system" }) as { mode: string };
  const detail = useFlagDetail("checkout", false);
  const stage = useStage();
  const client = useFlagnetic();
  return (
    <main>
      <p id="checkout">{String(checkout)}</p>
      <p id="copy">{copy}</p>
      <p id="theme">{theme.mode}</p>
      <p id="reason">{detail.reason}</p>
      <p id="version">{detail.rulesetVersion ?? "none"}</p>
      <p id="stage">{stage}</p>
      <p id="client">{client ? "attached" : "none"}</p>
    </main>
  );
}

describe("server rendering", () => {
  test("renders the values the server mode computed for the context", () => {
    const flags = createServer("", "", { offline: ruleset(), logger: () => undefined });
    const bootstrap = flags.values(user);

    const html = renderToStaticMarkup(
      <FlagneticProvider bootstrap={bootstrap}>
        <Page />
      </FlagneticProvider>,
    );

    expect(html).toContain('<p id="checkout">true</p>');
    expect(html).toContain('<p id="copy">Try free</p>');
    expect(html).toContain('<p id="theme">dark</p>');
    // The page is public: it learns that targeting decided the value, never
    // which rule did.
    expect(html).toContain('<p id="reason">targeting_match</p>');
    expect(html).toContain('<p id="version">7</p>');
    // A bootstrap document is `fresh`, and no client exists yet.
    expect(html).toContain('<p id="stage">fresh</p>');
    expect(html).toContain('<p id="client">none</p>');
  });

  test("renders another context's values: the document is per context", () => {
    const flags = createServer("", "", { offline: ruleset(), logger: () => undefined });
    const html = renderToStaticMarkup(
      <FlagneticProvider bootstrap={flags.values({ units: { user: "u-2" }, attributes: { country: "FR" } })}>
        <Page />
      </FlagneticProvider>,
    );
    expect(html).toContain('<p id="checkout">false</p>');
    expect(html).toContain('<p id="reason">targeting_match</p>');
  });

  test("builds no client and makes no request while a page is rendered", () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    try {
      const flags = createServer("", "", { offline: ruleset(), logger: () => undefined });
      const html = renderToStaticMarkup(
        <FlagneticProvider sdkKey="fn_cli_k" baseUrl="http://platform.test" bootstrap={flags.values(user)}>
          <Page />
        </FlagneticProvider>,
      );
      expect(html).toContain('<p id="client">none</p>');
      expect(html).toContain('<p id="checkout">true</p>');
      expect(fetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  test("with nothing to render from the page renders built-in defaults at stage built_in", () => {
    // What `values()` gives while the server mode is still at `built_in`:
    // the page says so instead of claiming fresh values.
    const html = renderToStaticMarkup(
      <FlagneticProvider sdkKey="fn_cli_k" baseUrl="http://platform.test" bootstrap={undefined}>
        <Page />
      </FlagneticProvider>,
    );
    expect(html).toContain('<p id="checkout">false</p>');
    expect(html).toContain('<p id="copy">Buy</p>');
    expect(html).toContain('<p id="reason">fallback</p>');
    expect(html).toContain('<p id="stage">built_in</p>');
  });

  test("a bootstrap document the page could not produce is refused, not thrown", () => {
    const logger = vi.fn();
    const html = renderToStaticMarkup(
      <FlagneticProvider bootstrap="{" options={{ logger }}>
        <Page />
      </FlagneticProvider>,
    );
    expect(html).toContain('<p id="stage">built_in</p>');
    expect(html).toContain('<p id="checkout">false</p>');
    expect(logger).toHaveBeenCalledWith("warn", "fetch_failed", expect.objectContaining({ source: "bootstrap", cause: "parse" }));
  });

  test("a bootstrap document of another schema is refused and says so", () => {
    const logger = vi.fn();
    const html = renderToStaticMarkup(
      <FlagneticProvider bootstrap={{ schema_version: 99, ruleset_version: 1, flags: {} }} options={{ logger }}>
        <Page />
      </FlagneticProvider>,
    );
    expect(html).toContain('<p id="stage">built_in</p>');
    expect(logger).toHaveBeenCalledWith("error", "schema_unsupported", expect.objectContaining({ schema_version: 99 }));
    expect(logger).toHaveBeenCalledWith("warn", "fetch_failed", expect.objectContaining({ source: "bootstrap", cause: "schema" }));
  });

  test("a hook without a provider above it says so", () => {
    for (const Hook of [
      () => useFlag("checkout", false),
      () => useFlagDetail("checkout", false),
      () => useStage(),
      () => useFlagnetic(),
    ]) {
      function Bare(): ReactElement {
        Hook();
        return <p />;
      }
      expect(() => renderToStaticMarkup(<Bare />)).toThrow(/needs a <FlagneticProvider>/);
    }
  });
});
