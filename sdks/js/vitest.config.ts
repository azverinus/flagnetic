import { fileURLToPath } from "node:url";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";

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
          // A `.dom.test.tsx` needs a document to mount into, so it runs in
          // the browser project alone; the React tests that only render to a
          // string run here too.
          exclude: ["test/**/*.browser.test.ts", "test/**/*.dom.test.tsx", "test/**/*.p99.test.ts"],
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
          exclude: ["test/**/*.node.test.{ts,tsx}", "test/**/*.browser.test.ts", "test/**/*.p99.test.ts"],
          browser: chromium,
        },
      },
    ],
  },
});
