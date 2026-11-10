import { readState } from "./instance";

/** What `/scim/v2` answered: the status, and the resource when it answered one (the `id` is all the specs read). */
export type ScimAnswer = { status: number; body?: { id: string } };

/**
 * One request to `/scim/v2` as a directory's connector sends it: the SCIM
 * token as the bearer, and a JSON body in the media type SCIM names. The
 * generated client covers the management API only, so this is plain HTTP.
 */
export async function scimRequest(
  token: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<ScimAnswer> {
  const response = await fetch(`${readState().baseURL}/scim/v2${path}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/scim+json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  return { status: response.status, body: text === "" ? undefined : JSON.parse(text) };
}
