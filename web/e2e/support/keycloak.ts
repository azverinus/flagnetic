import { execFile } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { promisify } from "node:util";

import type { Page } from "@playwright/test";

import { freePort, workDir } from "./instance";

const run = promisify(execFile);

/** The Keycloak release the SAML specs sign in through. */
const image = "quay.io/keycloak/keycloak:26.4";

/** The realm Keycloak imports at its start. */
const realm = "flagnetic";

const keycloakDir = resolve(workDir, "keycloak");
const statePath = resolve(workDir, "keycloak.json");

/** A person Keycloak knows, with a verified email. */
export type KeycloakUser = {
  username: string;
  email: string;
  firstName: string;
  password: string;
};

/**
 * Kai has no member: a provider that creates members on first sign-in
 * makes one. The domain is one no other spec's provider vouches for.
 * The password is a test credential that exists only in this harness.
 */
export const keycloakUsers = {
  kai: {
    username: "kai",
    email: "kai@saml.flagnetic.test",
    firstName: "Kai Keycloak",
    password: "keycloak-amber-quarry-5520",
  },
} as const satisfies Record<string, KeycloakUser>;

/** The email domain of Keycloak's people. */
export const keycloakDomain = "saml.flagnetic.test";

/**
 * The slug the specs give the SAML provider they create: Keycloak knows a
 * client by its entity ID, which names the slug.
 */
export const keycloakSlug = "keycloak";

export type KeycloakState = {
  /** The realm's identity provider metadata: plain http on a loopback address, which the instance accepts. */
  metadataURL: string;
  /** The realm's entity ID, as its metadata names it. */
  entityID: string;
  container: string;
};

export function readKeycloak(): KeycloakState {
  return JSON.parse(readFileSync(statePath, "utf8")) as KeycloakState;
}

/**
 * The realm Keycloak imports: its people, and one SAML client for
 * `keycloakSlug` registered with the entity ID and the ACS URL the instance
 * at baseURL computes. Assertions are signed and unencrypted, name the
 * person by username, and carry the attributes `email` and `name`.
 */
function realmJSON(baseURL: string): string {
  const entity = `${baseURL}/sso/${keycloakSlug}/metadata`;
  const acs = `${baseURL}/sso/${keycloakSlug}/acs`;
  const mapper = (name: string, property: string) => ({
    name,
    protocol: "saml",
    protocolMapper: "saml-user-property-mapper",
    config: {
      "user.attribute": property,
      "attribute.name": name,
      "attribute.nameformat": "Basic",
      "friendly.name": name,
    },
  });
  return JSON.stringify({
    realm,
    enabled: true,
    sslRequired: "none",
    users: Object.values(keycloakUsers).map((user) => ({
      username: user.username,
      email: user.email,
      emailVerified: true,
      enabled: true,
      firstName: user.firstName,
      lastName: "Tester",
      credentials: [{ type: "password", value: user.password, temporary: false }],
    })),
    clients: [
      {
        clientId: entity,
        protocol: "saml",
        enabled: true,
        name: keycloakSlug,
        redirectUris: [acs],
        baseUrl: acs,
        attributes: {
          saml_assertion_consumer_url_post: acs,
          saml_name_id_format: "username",
          "saml.server.signature": "false",
          "saml.assertion.signature": "true",
          "saml.signature.algorithm": "RSA_SHA256",
          "saml.client.signature": "false",
          "saml.force.post.binding": "true",
          "saml.authnstatement": "true",
          saml_force_name_id_format: "false",
        },
        protocolMappers: [mapper("email", "email"), mapper("name", "firstName")],
      },
    ],
  });
}

/**
 * Starts Keycloak in Docker, bound to loopback so the instance and the
 * browser reach the same address (an http metadata URL is accepted only
 * there), and waits until it serves the realm's metadata, which takes a
 * while. One container serves the whole run; `stopKeycloak` removes it.
 * `baseURL` is the address of the instance the run starts, which the
 * realm's client is registered for.
 */
export async function startKeycloak(baseURL: string): Promise<KeycloakState> {
  const port = await freePort();
  mkdirSync(keycloakDir, { recursive: true });
  const realmFile = resolve(keycloakDir, "realm.json");
  writeFileSync(realmFile, realmJSON(baseURL));

  const container = `flagnetic-e2e-keycloak-${process.pid}`;
  try {
    await run("docker", [
      "run",
      "--detach",
      "--rm",
      "--name",
      container,
      "--publish",
      `127.0.0.1:${port}:8080`,
      "--volume",
      `${realmFile}:/opt/keycloak/data/import/realm.json:ro`,
      image,
      "start-dev",
      "--import-realm",
    ]);
  } catch (error) {
    throw new Error(
      `starting Keycloak needs Docker: ${error instanceof Error ? error.message : String(error)}`,
      { cause: error },
    );
  }
  const realmURL = `http://127.0.0.1:${port}/realms/${realm}`;
  const state: KeycloakState = {
    metadataURL: `${realmURL}/protocol/saml/descriptor`,
    entityID: realmURL,
    container,
  };
  writeFileSync(statePath, JSON.stringify(state, null, 2));

  const deadline = Date.now() + 240_000;
  for (;;) {
    try {
      const response = await fetch(state.metadataURL);
      if (response.ok) return state;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) {
      await stopKeycloak();
      throw new Error(`Keycloak was not ready at ${state.metadataURL}`);
    }
    await new Promise((done) => setTimeout(done, 500));
  }
}

/** Removes the container the run started, if it started one. */
export async function stopKeycloak(): Promise<void> {
  let state: KeycloakState;
  try {
    state = readKeycloak();
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
 * Fills in Keycloak's login form, which the browser reaches by following a
 * SAML provider's sign-in address, and sends it. Keycloak then posts the
 * SAML response to the instance from a page of its own.
 */
export async function signInAtKeycloak(page: Page, user: KeycloakUser): Promise<void> {
  await page.locator("#username").fill(user.username);
  await page.locator("#password").fill(user.password);
  await page.locator("#kc-login").click();
}
