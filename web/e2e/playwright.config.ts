import { defineConfig, devices } from "@playwright/test";

import { readState } from "./support/instance";

/**
 * The suite runs against the real binary with the console embedded, on a
 * real PostgreSQL. Nothing is mocked: the generated client's types are the
 * only contract. One worker, because the specs share one instance and one
 * clock.
 */
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env["CI"],
  retries: process.env["CI"] ? 1 : 0,
  reporter: process.env["CI"] ? [["github"], ["html", { open: "never" }]] : [["list"]],
  globalSetup: "./global-setup.ts",
  globalTeardown: "./global-teardown.ts",
  timeout: 30_000,
  expect: { timeout: 5_000 },
  use: {
    // globalSetup wrote where the instance it started is listening.
    baseURL: process.env["FLAGNETIC_E2E_BASE_URL"] ?? safeBaseURL(),
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});

/** The config is read before global setup on the first run, so this may be absent. */
function safeBaseURL(): string {
  try {
    return readState().baseURL;
  } catch {
    return "http://127.0.0.1:8080";
  }
}
