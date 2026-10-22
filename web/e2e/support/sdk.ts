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
