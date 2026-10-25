import { createSdkKey } from "@flagnetic/api";

import { managementClient, must } from "./console";
import { readState } from "./instance";

/** A key of an environment, created through the management API, with the value it is shown once. */
export async function seedSdkKey(
  project: string,
  environment: string,
  body: { kind: "server" | "client"; name: string },
): Promise<string> {
  const created = must(
    `seeding the key ${body.name}`,
    await createSdkKey({ client: managementClient(), path: { project, environment }, body }),
  );
  return created.value;
}

/** A request to the SDK API as an SDK would make it, failing unless the server accepts it. */
export async function sdkRequest(
  key: string,
  path: string,
  { userAgent, body }: { userAgent?: string; body?: unknown } = {},
): Promise<Response> {
  const response = await fetch(`${readState().baseURL}/sdk/v1${path}`, {
    method: body === undefined ? "GET" : "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      ...(userAgent ? { "User-Agent": userAgent } : {}),
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`${path} answered ${response.status}: ${await response.text()}`);
  }
  return response;
}

/** A fresh event ID as an SDK makes one: a lowercase UUIDv7 carrying the time `at`. */
export function eventId(at: Date = new Date()): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const ms = BigInt(at.getTime());
  for (let i = 0; i < 6; i++) bytes[i] = Number((ms >> BigInt(8 * (5 - i))) & 0xffn);
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/** One exposure as an SDK reports it, of a unit to a variation of an experiment at a time. */
export type Exposure = {
  experiment: string;
  flag: string;
  variation: string;
  unitType: string;
  unitId: string;
  at: Date;
};

/** Posts exposures in one batch as an SDK would, answering the server's result. */
export async function postExposures(
  key: string,
  exposures: readonly Exposure[],
): Promise<{ accepted: number; rejected: { code: string }[] }> {
  const response = await sdkRequest(key, "/events", {
    body: {
      schema_version: 1,
      sdk: { name: "flagnetic-js", version: "1.0.0" },
      sent_at: new Date().toISOString(),
      events: exposures.map((exposure) => ({
        type: "exposure",
        event_id: eventId(exposure.at),
        occurred_at: exposure.at.toISOString(),
        experiment_key: exposure.experiment,
        flag_key: exposure.flag,
        variation_key: exposure.variation,
        unit_type: exposure.unitType,
        unit_id: exposure.unitId,
        evaluation_reason: "experiment",
        override: false,
        ruleset_version: 1,
      })),
    },
  });
  return (await response.json()) as { accepted: number; rejected: { code: string }[] };
}
