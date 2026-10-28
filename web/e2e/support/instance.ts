import { spawn, type ChildProcess } from "node:child_process";
import { execFile } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);

const here = dirname(fileURLToPath(import.meta.url));
/** The repository root: the suite builds the very binary a release ships. */
export const repoRoot = resolve(here, "../../..");
export const workDir = resolve(here, "..", ".playwright");
const statePath = resolve(workDir, "instance.json");
const pidPath = resolve(workDir, "server.pid");
/** The test clock's offset file: what `advanceClock` writes. */
const clockPath = resolve(workDir, "clock-offset");

/** The instance admin the harness bootstraps, and what the suite signs in as. */
export const admin = {
  email: "ada@flagnetic.test",
  name: "Ada Lovelace",
  password: "harness-tangerine-violin-1987",
};

/**
 * A member who holds viewer in the first seeded project and no role in
 * the second, invited and accepted through the management API.
 */
export const viewer = {
  email: "vera@flagnetic.test",
  name: "Vera Viewer",
  password: "harness-cobalt-lantern-2031",
};

/** The projects the harness seeds through the management API. */
export const seededProject = { key: "checkout", name: "Checkout" };
export const secondProject = { key: "billing", name: "Billing" };

export type InstanceState = {
  baseURL: string;
  token: string;
};

export function readState(): InstanceState {
  return JSON.parse(readFileSync(statePath, "utf8")) as InstanceState;
}

/** Records where the instance this run started is listening. */
export function remember(state: InstanceState): void {
  mkdirSync(workDir, { recursive: true });
  writeFileSync(statePath, JSON.stringify(state, null, 2));
}

/** A port nothing else holds, so several runs can share a machine. */
export async function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const probe = createServer();
    probe.once("error", fail);
    probe.listen(0, "127.0.0.1", () => {
      const address = probe.address();
      if (typeof address === "object" && address) {
        probe.close(() => done(address.port));
        return;
      }
      probe.close(() => fail(new Error("no port")));
    });
  });
}

function databaseURL(): string {
  const url = process.env["DATABASE_URL"];
  if (!url) {
    throw new Error(
      "DATABASE_URL is not set. Run the suite through `task web:e2e`, which starts a throwaway PostgreSQL, or point it at an empty database.",
    );
  }
  return url;
}

/** The environment every `flagnetic` command in the harness runs with. */
function environment(extra: Record<string, string> = {}): NodeJS.ProcessEnv {
  return {
    ...process.env,
    DATABASE_URL: databaseURL(),
    FLAGNETIC_SECRET_KEY: "playwright-secret-key-0123456789abcdef",
    FLAGNETIC_LOG_FORMAT: "text",
    FLAGNETIC_LOG_LEVEL: "warn",
    ...extra,
  };
}

/** Builds the console and then the binary that embeds it. */
export async function build(): Promise<string> {
  mkdirSync(workDir, { recursive: true });
  if (process.env["FLAGNETIC_E2E_SKIP_BUILD"] !== "1") {
    await run("pnpm", ["--filter", "@flagnetic/console", "build"], {
      cwd: resolve(repoRoot, "web"),
      maxBuffer: 32 * 1024 * 1024,
    });
  }
  const binary = resolve(workDir, "flagnetic");
  await run("go", ["build", "-o", binary, "./cmd/flagnetic"], {
    cwd: repoRoot,
    maxBuffer: 32 * 1024 * 1024,
  });
  return binary;
}

/**
 * Applies the migrations and bootstraps the first instance admin with a
 * local credential, returning the API token the seeding uses. The database
 * must be empty: bootstrap refuses a second instance admin, which keeps a
 * run from inheriting another run's data.
 */
export async function bootstrap(binary: string): Promise<string> {
  await run(binary, ["migrate"], { env: environment(), cwd: repoRoot });
  const { stdout } = await run(
    binary,
    ["admin", "bootstrap", "--email", admin.email, "--name", admin.name],
    { env: environment({ FLAGNETIC_ADMIN_PASSWORD: admin.password }), cwd: repoRoot },
  );
  const token = stdout.trim();
  if (!token) {
    throw new Error("admin bootstrap printed no API token; is the database empty?");
  }
  return token;
}

/**
 * The binary's clock runs ahead of the wall clock by whatever duration the
 * offset file holds, read on every call. `serve` accepts the file only
 * under the harness's marker and on loopback.
 */
const clockSettings = {
  FLAGNETIC_TEST_HARNESS: "playwright",
  FLAGNETIC_TEST_CLOCK_FILE: clockPath,
};

/**
 * Moves the running binary's clock to `offset` ahead of the wall clock, a
 * Go duration such as "25h"; "0s" puts it back. A spec that moves it puts
 * it back when it ends, since every later spec shares the instance.
 */
export function advanceClock(offset: string): void {
  writeFileSync(clockPath, offset);
}

/** Starts the binary and waits until it reports itself ready. */
export async function serve(binary: string): Promise<{ baseURL: string; child: ChildProcess }> {
  const port = await freePort();
  const baseURL = `http://127.0.0.1:${port}`;
  rmSync(clockPath, { force: true });
  const child = spawn(binary, ["serve"], {
    env: environment({
      FLAGNETIC_LISTEN_ADDR: `127.0.0.1:${port}`,
      // Plain http on loopback, where the session cookie is allowed without
      // Secure, so the harness needs no certificate.
      FLAGNETIC_BASE_URL: baseURL,
      // No metrics listener: its fixed port would clash with another run.
      FLAGNETIC_METRICS_LISTEN_ADDR: "",
      // The webhook specs post to a receiver on loopback over plain http,
      // and want a delivery within a moment of its event.
      FLAGNETIC_WEBHOOK_ALLOW_HTTP: "true",
      FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS: "true",
      FLAGNETIC_WEBHOOK_TICK: "1s",
      ...clockSettings,
    }),
    cwd: repoRoot,
    stdio: ["ignore", "inherit", "inherit"],
  });
  child.unref();
  writeFileSync(pidPath, String(child.pid ?? ""));

  const deadline = Date.now() + 30_000;
  for (;;) {
    if (child.exitCode !== null) {
      throw new Error(`flagnetic serve exited with ${child.exitCode} before becoming ready`);
    }
    try {
      const response = await fetch(`${baseURL}/readyz`);
      if (response.ok) break;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) throw new Error(`flagnetic serve was not ready at ${baseURL}`);
    await new Promise((done) => setTimeout(done, 100));
  }
  return { baseURL, child };
}

/** Stops the server the run started. */
export function stop(): void {
  let pid: number;
  try {
    pid = Number(readFileSync(pidPath, "utf8"));
  } catch {
    return;
  }
  if (!pid) return;
  try {
    process.kill(pid, "SIGTERM");
  } catch {
    // Already gone.
  }
}
