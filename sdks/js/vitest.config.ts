import { fileURLToPath } from "node:url";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";
import type { BrowserCommand } from "vitest/node";
import { CrossOriginServer } from "./test/support/cross-origin-server.js";

// The repository root: the conformance suite lives in conformance/ beside the SDKs.
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));

// FLAGNETIC_BROWSER_CHANNEL=chrome runs the browser project on an installed
// Chrome instead of Playwright's own Chromium.
const channel = process.env.FLAGNETIC_BROWSER_CHANNEL;

const chromium = {
  enabled: true,
  provider: playwright(channel ? { launchOptions: { channel } } : {}),
  headless: true,
  screenshotFailures: false,
  instances: [{ browser: "chromium" as const }],
};

// The browser client's own tests run in Chromium, Firefox and WebKit against
// a server on another origin, and the React bindings mount and hydrate in each. FLAGNETIC_BROWSERS narrows the engines, as CI
// does to run one per job.
const engines = (process.env.FLAGNETIC_BROWSERS ?? "chromium,firefox,webkit").split(",") as ("chromium" | "firefox" | "webkit")[];

// The cross-origin server lives in Node; the page drives it through these
// commands, the only way a test in the browser reaches back into Node.
const crossOrigin = new CrossOriginServer();
const crossOriginStart: BrowserCommand = () => crossOrigin.start();
const crossOriginReset: BrowserCommand = () => {
  crossOrigin.reset();
};
const crossOriginServe: BrowserCommand<[sdkKey: string, doc: unknown]> = (_ctx, sdkKey, doc) => {
  crossOrigin.serve(sdkKey, doc);
};
const crossOriginRequests: BrowserCommand = () => crossOrigin.requests();
const crossOriginSend: BrowserCommand<[text: string]> = (_ctx, text) => crossOrigin.send(text);
const commands = { crossOriginStart, crossOriginReset, crossOriginServe, crossOriginRequests, crossOriginSend };

export default defineConfig({
  server: { fs: { allow: [repoRoot] } },
  test: {
    projects: [
      {
        extends: true,
        test: {
          name: "node",
          environment: "node",
          include: ["test/**/*.test.{ts,tsx}"],
          // A `.browser.test` needs a document, so it runs in the engines'
          // project alone; the React tests that only render to a string run
          // here too.
          exclude: ["test/**/*.browser.test.{ts,tsx}", "test/**/*.p99.test.ts"],
          benchmark: { include: ["bench/**/*.bench.ts"] },
        },
      },
      {
        // The evaluation budget is a project of its own, run alone in CI, so
        // other tests in worker threads beside it cannot distort the timing.
        extends: true,
        test: {
          name: "p99",
          environment: "node",
          include: ["test/**/*.p99.test.ts"],
        },
      },
      {
        extends: true,
        test: {
          name: "browser",
          include: ["test/**/*.test.{ts,tsx}"],
          exclude: ["test/**/*.node.test.{ts,tsx}", "test/**/*.browser.test.{ts,tsx}", "test/**/*.p99.test.ts"],
          browser: chromium,
        },
      },
      {
        extends: true,
        test: {
          name: "browsers",
          include: ["test/**/*.browser.test.{ts,tsx}"],
          browser: {
            ...chromium,
            provider: playwright(),
            instances: engines.map((browser) => ({ browser })),
            commands,
          },
        },
      },
    ],
  },
});
