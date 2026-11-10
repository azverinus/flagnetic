import { execFile } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { promisify } from "node:util";

import type { Page } from "@playwright/test";

import { freePort, workDir } from "./instance";

const run = promisify(execFile);

/** The Dex release the OIDC specs sign in through. */
const image = "dexidp/dex:v2.45.1";

const dexDir = resolve(workDir, "dex");
const statePath = resolve(workDir, "dex.json");

/**
 * A person Dex knows. Dex marks the email of every static password
 * verified. The hashes are bcrypt of the passwords, as Dex asks for; they
 * are test credentials that exist only in this harness.
 */
export type DexUser = { email: string; name: string; password: string; hash: string };

/**
 * Vera is the member the harness seeds as a viewer: signing in as her
 * through a provider that links by email lands in an account that exists.
 * The stranger has an address in the same domain and no member, so a
 * provider that does not create members refuses her.
 */
export const dexUsers = {
  vera: {
    email: "vera@flagnetic.test",
    name: "Vera Viewer",
    password: "dex-cobalt-lantern-2031",
    hash: "$2a$10$HMXzCOvuyYjaz0Eyvkzhjuzg.vWI.8LDXKafAECdoD9POI4SPiP0e",
  },
  stranger: {
    email: "sam.stranger@flagnetic.test",
    name: "Sam Stranger",
    password: "dex-saffron-harbor-1874",
    hash: "$2a$10$iDQrCCaWFiC/6yEoiE1L6uDx5bsjl/DUj4cDOijOWU5pTDYcvM0vG",
  },
  // Ines has an address in `enforcedDomain`, which only the enforcement spec
  // makes one the instance vouches for through Dex.
  ines: {
    email: "ines@corp.flagnetic.test",
    name: "Ines Enforced",
    password: "dex-juniper-forge-4417",
    hash: "$2a$10$Mht6HVIoR4Jvx2IhQvL5Wug2GuQSLOwwNUrX0gWP.jkwT4w9ZS9pu",
  },
} as const satisfies Record<string, DexUser>;

/** The email domain of Ines, whom the enforcement spec sends to Dex. */
export const enforcedDomain = "corp.flagnetic.test";

/** The slug the specs give the provider they create; Dex lists its redirect URIs exactly. */
export const dexSlug = "dex";
/** The client Dex is configured with. */
const clientId = "flagnetic";
const clientSecret = "dex-client-secret";

export type DexState = {
  /** The issuer: plain http on a loopback address, which the instance accepts. */
  issuer: string;
  clientId: string;
  clientSecret: string;
  container: string;
};

export function readDex(): DexState {
  return JSON.parse(readFileSync(statePath, "utf8")) as DexState;
}

/**
 * The configuration of a Dex that keeps everything in memory, asks for a
 * password only (no connector to choose, no approval screen), and has one
 * client whose redirect URI is the instance's callback for `dexSlug`.
 */
function configuration(issuer: string, baseURL: string): string {
  const users = Object.values(dexUsers)
    .map(
      (user, index) =>
        `  - email: "${user.email}"\n    hash: "${user.hash}"\n    username: "${user.name}"\n    userID: "user-${index + 1}"\n`,
    )
    .join("");
  return [
    `issuer: ${issuer}`,
    "storage:",
    "  type: memory",
    "web:",
    "  http: 0.0.0.0:5556",
    "logger:",
    "  level: error",
    "enablePasswordDB: true",
    "oauth2:",
    "  skipApprovalScreen: true",
    "staticClients:",
    `  - id: ${clientId}`,
    `    secret: ${clientSecret}`,
    "    name: Flagnetic",
    "    redirectURIs:",
    `      - ${baseURL}/sso/${dexSlug}/callback`,
    "staticPasswords:",
    users,
  ].join("\n");
}

/**
 * Starts Dex in Docker, bound to loopback so the instance and the browser
 * reach the same address (an http issuer is accepted only there), and waits
 * until it serves its discovery document. One container serves the whole
 * run; `stopDex` removes it. `baseURL` is the address of the instance the
 * run starts, which Dex sends the browser back to.
 */
export async function startDex(baseURL: string): Promise<DexState> {
  const port = await freePort();
  const issuer = `http://127.0.0.1:${port}/dex`;
  mkdirSync(dexDir, { recursive: true });
  const config = resolve(dexDir, "config.yaml");
  writeFileSync(config, configuration(issuer, baseURL));

  const container = `flagnetic-e2e-dex-${process.pid}`;
  try {
    await run("docker", [
      "run",
      "--detach",
      "--rm",
      "--name",
      container,
      "--publish",
      `127.0.0.1:${port}:5556`,
      "--volume",
      `${config}:/etc/dex/config.yaml:ro`,
      image,
      "dex",
      "serve",
      "/etc/dex/config.yaml",
    ]);
  } catch (error) {
    throw new Error(
      `starting Dex needs Docker: ${error instanceof Error ? error.message : String(error)}`,
      { cause: error },
    );
  }
  const state: DexState = { issuer, clientId, clientSecret, container };
  writeFileSync(statePath, JSON.stringify(state, null, 2));

  const deadline = Date.now() + 60_000;
  for (;;) {
    try {
      const response = await fetch(`${issuer}/.well-known/openid-configuration`);
      if (response.ok) return state;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) {
      await stopDex();
      throw new Error(`Dex was not ready at ${issuer}`);
    }
    await new Promise((done) => setTimeout(done, 200));
  }
}

/** Removes the container the run started, if it started one. */
export async function stopDex(): Promise<void> {
  let state: DexState;
  try {
    state = readDex();
  } catch {
    return;
  }
  rmSync(statePath, { force: true });
  try {
    await run("docker", ["rm", "--force", state.container]);
  } catch {
    // Already gone.
  }
}

/**
 * Fills in Dex's login form, which the browser reaches by following a
 * provider's sign-in address, and sends it. Dex then sends the browser back
 * to the instance.
 */
export async function signInAtDex(page: Page, user: DexUser): Promise<void> {
  await page.locator("#login").fill(user.email);
  await page.locator("#password").fill(user.password);
  await page.locator("#submit-login").click();
}
