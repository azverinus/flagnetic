// The provider and the hooks in a real browser (Chromium, Firefox and
// WebKit), over the mock of the server's SDK API: what re-renders when a flag changes, what the stage
// hook reports, that nothing updates state after the tree is gone, and that
// a page rendered on a server hydrates into the values it was rendered from.
import { act, type ReactElement } from "react";
import { createRoot, hydrateRoot, type Root } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { createClient } from "../src/client.js";
import { FlagneticProvider, useFlag, useFlagDetail, useFlagnetic, useStage, type FlagneticClient } from "../src/react.js";
import { createServer } from "../src/server.js";
import { MockNetwork } from "./support/mock-network.js";
import { ruleset, valuesFor } from "./support/flags-fixture.js";
import { VirtualClock } from "./support/virtual-clock.js";

const key = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const baseUrl = "http://platform.test";
const user = { units: { user: "u-1" }, attributes: { country: "DE" } };
const json = { "Content-Type": "application/json" };

let network: MockNetwork;
let container: HTMLDivElement;
let root: Root | undefined;
let errors: unknown[][];

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  network = new MockNetwork(new VirtualClock());
  vi.stubGlobal("fetch", network.fetch);
  container = document.createElement("div");
  document.body.append(container);
  errors = [];
  vi.spyOn(console, "error").mockImplementation((...args: unknown[]) => {
    errors.push(args);
  });
});

afterEach(async () => {
  if (root) await step(() => { root?.unmount(); });
  root = undefined;
  container.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/** Lets the client's promises settle inside React's act scope. */
async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

/** Applies a change inside React's act scope, letting its effects flush. */
async function step(fn: () => void): Promise<void> {
  await act(async () => {
    fn();
    await Promise.resolve();
  });
}

/** Answers the remote evaluation the client is waiting on with a values document. */
async function serve(values: Record<string, unknown>): Promise<void> {
  const pending = network.waiting("/sdk/v1/evaluate")[0];
  if (!pending) throw new Error("no remote evaluation is waiting");
  pending.answer({ status: 200, headers: json, body: JSON.stringify(values) });
  await settle();
}

const renders = { checkout: 0, copy: 0, outside: 0 };

function Checkout(): ReactElement {
  renders.checkout++;
  return <p id="checkout">{String(useFlag("checkout", false))}</p>;
}

function Copy(): ReactElement {
  renders.copy++;
  return <p id="copy">{useFlag("paywall_copy", "Buy")}</p>;
}

// Reads no flag: it re-renders only if the provider itself does.
function Outside(): ReactElement {
  renders.outside++;
  return <p id="outside">outside</p>;
}

function text(id: string): string {
  return container.querySelector(`#${id}`)?.textContent ?? "";
}

describe("a flag change over the mock", () => {
  test("re-renders the subscribers of the changed key and nobody else", async () => {
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}
          bootstrap={valuesFor({ checkout: false, paywall_copy: "Buy" })}>
          <Checkout />
          <Copy />
          <Outside />
        </FlagneticProvider>,
      );
    });
    await settle();
    expect(text("checkout")).toBe("false");
    const before = { ...renders };

    await serve(valuesFor({ checkout: true, paywall_copy: "Buy" }, 8));

    expect(text("checkout")).toBe("true");
    expect(renders.checkout - before.checkout).toBe(1);
    expect(renders.copy - before.copy).toBe(0);
    expect(renders.outside - before.outside).toBe(0);
  });

  test("re-renders nobody when the values come back unchanged", async () => {
    const values = valuesFor({ checkout: true, paywall_copy: "Buy" });
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }} bootstrap={values}>
          <Checkout />
          <Copy />
        </FlagneticProvider>,
      );
    });
    await settle();
    const before = { ...renders };

    await serve(values);

    expect(text("checkout")).toBe("true");
    expect(renders.checkout - before.checkout).toBe(0);
    expect(renders.copy - before.copy).toBe(0);
  });

  test("a JSON flag keeps its snapshot, so an unchanged value never loops", async () => {
    let seen = 0;
    let client: FlagneticClient | undefined;
    function Theme(): ReactElement {
      seen++;
      client = useFlagnetic();
      const theme = useFlag("theme", { mode: "system" }) as { mode: string };
      return <p id="theme">{theme.mode}</p>;
    }
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}
          bootstrap={valuesFor({ theme: { mode: "dark" } })}>
          <Theme />
        </FlagneticProvider>,
      );
    });
    await settle();
    expect(text("theme")).toBe("dark");
    const before = seen;

    // The getter copies a JSON value on every read, so only a real change
    // may reach the component.
    await serve(valuesFor({ theme: { mode: "dark" } }, 8));
    expect(seen - before).toBe(0);

    // Setting the context again is a fresh remote evaluation.
    await step(() => { void client?.setContext(user); });
    await serve(valuesFor({ theme: { mode: "light" } }, 9));
    expect(text("theme")).toBe("light");
    expect(seen - before).toBe(1);
  });
});

describe("a detail's stage", () => {
  test("follows the fallback chain: a 503 leaves the component at last_good", async () => {
    let seen = 0;
    function CheckoutDetail(): ReactElement {
      seen++;
      const detail = useFlagDetail("checkout", false);
      return <p id="detail">{`${String(detail.value)}:${detail.stage}`}</p>;
    }
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}
          bootstrap={valuesFor({ checkout: true, paywall_copy: "Buy" })}>
          <CheckoutDetail />
          <Copy />
          <Outside />
        </FlagneticProvider>,
      );
    });
    await settle();
    expect(text("detail")).toBe("true:fresh");
    const before = { seen, ...renders };

    // The server is out. The values keep serving, and the component says
    // so: the detail reports where the fallback chain stands.
    const pending = network.waiting("/sdk/v1/evaluate")[0];
    if (!pending) throw new Error("no remote evaluation is waiting");
    pending.answer({ status: 503 });
    await settle();

    expect(text("detail")).toBe("true:last_good");
    expect(seen - before.seen).toBe(1);
    // A component reading a flag through the value hook renders the same
    // value at either stage, so the transition costs it no re-render.
    expect(renders.copy - before.copy).toBe(0);
    expect(renders.outside - before.outside).toBe(0);
  });
});

describe("the stage hook", () => {
  test("follows built_in to fresh", async () => {
    const stages: string[] = [];
    function Stage(): ReactElement {
      const stage = useStage();
      stages.push(stage);
      return <p id="stage">{stage}</p>;
    }
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}>
          <Stage />
        </FlagneticProvider>,
      );
    });
    await settle();
    expect(text("stage")).toBe("built_in");

    await serve(valuesFor({ checkout: true }));

    expect(text("stage")).toBe("fresh");
    expect(stages.at(0)).toBe("built_in");
    expect(stages.at(-1)).toBe("fresh");
  });

  test("a bootstrap document is fresh from the first render", async () => {
    function Stage(): ReactElement {
      return <p id="stage">{useStage()}</p>;
    }
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}
          bootstrap={valuesFor({ checkout: true })}>
          <Stage />
        </FlagneticProvider>,
      );
    });
    expect(text("stage")).toBe("fresh");
  });
});

describe("the client hook", () => {
  test("hands over the client the provider owns, once it has one", async () => {
    let client: FlagneticClient | undefined;
    function Held(): ReactElement {
      client = useFlagnetic();
      return <p id="client">{client ? "attached" : "none"}</p>;
    }
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}>
          <Held />
        </FlagneticProvider>,
      );
    });
    await settle();
    expect(text("client")).toBe("attached");

    // It is the client the hooks read: its values are what they serve.
    await serve(valuesFor({ checkout: true }));
    expect(client?.stage).toBe("fresh");
    expect(client?.getBool("checkout", false)).toBe(true);
  });
});

describe("a client the application owns", () => {
  test("is read from the first render on and outlives the tree", async () => {
    const client = createClient(key, baseUrl, {
      context: user,
      store: false,
      logger: () => undefined,
      bootstrap: valuesFor({ checkout: true, paywall_copy: "Buy" }),
    });
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider client={client}>
          <Checkout />
        </FlagneticProvider>,
      );
    });
    expect(text("checkout")).toBe("true");

    await step(() => { root?.unmount(); });
    root = undefined;

    // The provider never closes a client it did not build.
    expect(client.getBool("checkout", false)).toBe(true);
    await client.close();
  });

  test("is followed when it changes, and let go when it is taken away", async () => {
    const options = { context: user, store: false as const, logger: () => undefined };
    const first = createClient(key, baseUrl, { ...options, bootstrap: valuesFor({ checkout: true }) });
    const second = createClient(key, baseUrl, { ...options, bootstrap: valuesFor({ checkout: false }) });
    let held: FlagneticClient | undefined;
    function Held(): ReactElement {
      held = useFlagnetic();
      return <></>;
    }
    const tree = (client: FlagneticClient | undefined): ReactElement => (
      <FlagneticProvider {...(client && { client })} bootstrap={valuesFor({ checkout: true })}>
        <Checkout />
        <Held />
      </FlagneticProvider>
    );
    root = createRoot(container);
    await step(() => { root?.render(tree(first)); });
    expect(text("checkout")).toBe("true");
    expect(held).toBe(first);

    await step(() => { root?.render(tree(second)); });
    expect(text("checkout")).toBe("false");
    expect(held).toBe(second);

    // Without a client the hooks fall back to the bootstrap document, never
    // to a client the provider no longer holds.
    await step(() => { root?.render(tree(undefined)); });
    expect(text("checkout")).toBe("true");
    expect(held).toBeUndefined();

    // Neither client was closed by the provider.
    expect(first.getBool("checkout", false)).toBe(true);
    expect(second.getBool("checkout", false)).toBe(false);
    await first.close();
    await second.close();
  });
});

describe("unmounting", () => {
  test("sets no state after the tree is gone, and closes the client it owns", async () => {
    root = createRoot(container);
    await step(() => {
      root?.render(
        <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }}
          bootstrap={valuesFor({ checkout: false })}>
          <Checkout />
        </FlagneticProvider>,
      );
    });
    await settle();
    const before = renders.checkout;
    const waiting = network.waiting("/sdk/v1/evaluate")[0];
    expect(waiting).toBeDefined();

    await step(() => { root?.unmount(); });
    root = undefined;

    // The answer the client was waiting on lands after the tree is gone.
    waiting?.answer({ status: 200, headers: json, body: JSON.stringify(valuesFor({ checkout: true }, 8)) });
    await settle();

    expect(renders.checkout).toBe(before);
    expect(errors).toEqual([]);
    // A closed client makes no further request.
    const after = network.seen.length;
    await new Promise((r) => setTimeout(r, 20));
    expect(network.seen.length).toBe(after);
  });
});

describe("hydration", () => {
  test("the first render in the browser is the one the server sent, and the first refetch follows it", async () => {
    const flags = createServer("", "", { offline: ruleset(), logger: () => undefined });
    const bootstrap = flags.values(user);

    function Page(): ReactElement {
      const checkout = useFlag("checkout", false);
      const copy = useFlag("paywall_copy", "Buy");
      const detail = useFlagDetail("checkout", false);
      return (
        <main>
          <p id="checkout">{String(checkout)}</p>
          <p id="copy">{copy}</p>
          <p id="reason">{detail.reason}</p>
          <p id="stage">{useStage()}</p>
        </main>
      );
    }

    const tree = (
      <FlagneticProvider sdkKey={key} baseUrl={baseUrl} options={{ context: user, store: false, logger: () => undefined }} bootstrap={bootstrap}>
        <Page />
      </FlagneticProvider>
    );

    container.innerHTML = renderToString(tree);
    const served = container.innerHTML;
    expect(text("checkout")).toBe("true");
    expect(text("copy")).toBe("Try free");
    expect(text("reason")).toBe("targeting_match");
    // Nothing was fetched to render the page.
    expect(network.seen).toHaveLength(0);

    await step(() => {
      root = hydrateRoot(container, tree);
    });

    // React rewrites the markup it cannot hydrate: this it could.
    expect(container.innerHTML).toBe(served);
    expect(errors).toEqual([]);
    expect(text("checkout")).toBe("true");
    expect(text("copy")).toBe("Try free");
    expect(text("stage")).toBe("fresh");

    // The client the provider owns is built after hydration, and that is
    // when the first remote evaluation happens.
    await settle();
    expect(network.seen.filter((r) => r.path === "/sdk/v1/evaluate")).toHaveLength(1);

    await serve(valuesFor({ checkout: false, paywall_copy: "Try free" }, 8));
    expect(text("checkout")).toBe("false");
  });
});
