import { client } from "@flagnetic/api";

import { hideLossyNumbers, revealLossless, stringifyLossless } from "./lossless-json";

/** Signing in is the one place a 401 means "wrong password", not "signed out". */
const signInOperationPath = "/api/v1/auth/sign-in";

export type ManagementFetchOptions = {
  /** The fetch the requests go through; the global one by default. */
  fetch?: typeof globalThis.fetch;
  /**
   * Called when the server says the caller is not signed in, whether the
   * session ended or the request carried none: what the console does next
   * depends on whether a member was signed in, not on which it was.
   */
  onUnauthenticated: () => void;
};

/** The ruleset is saved as the server sent it, so its text is never rewritten. */
const rulesetPath = /\/environments\/[^/]+\/ruleset$/;

/**
 * The answer with the numbers a double would change hidden in its text,
 * for the generated client's own `JSON.parse`; the transformer set by
 * {@link configureClient} gives them back as numbers that keep their text.
 */
async function keepingNumbers(response: Response, url: string): Promise<Response> {
  const type = response.headers.get("content-type") ?? "";
  if (
    !/[/+]json\b/.test(type) ||
    rulesetPath.test(new URL(url, "http://origin.invalid").pathname)
  ) {
    return response;
  }
  const text = await response.text();
  const headers = new Headers(response.headers);
  headers.delete("content-length");
  return new Response(
    response.status === 204 || response.status === 304 ? null : hideLossyNumbers(text),
    {
      status: response.status,
      statusText: response.statusText,
      headers,
    },
  );
}

/**
 * The fetch the generated client runs on. The session cookie travels on
 * its own, same-origin; the server refuses a request from another origin
 * outright, so there is no token to send and nothing to retry. What this
 * adds is the report of an ended session, so the console can send the
 * member to sign-in keeping where they were.
 */
export function managementFetch(options: ManagementFetchOptions): typeof globalThis.fetch {
  const send = options.fetch ?? globalThis.fetch.bind(globalThis);
  return async (input, init) => {
    const request = new Request(input as RequestInfo, init);
    const response = await send(request);
    if (
      response.status === 401 &&
      new URL(request.url, "http://origin.invalid").pathname !== signInOperationPath
    ) {
      options.onUnauthenticated();
    }
    return keepingNumbers(response, request.url);
  };
}

/**
 * Points the generated client at the management API on this origin, with
 * the session cookie and the report of an ended session. Called once,
 * before the first render.
 */
export function configureClient(onUnauthenticated: () => void): void {
  client.setConfig({
    ...client.getConfig(),
    credentials: "same-origin",
    // A value the server keeps byte for byte must go back as it came, not
    // as the nearest double (see lossless-json).
    bodySerializer: (body: unknown) => stringifyLossless(body),
    responseTransformer: async (data: unknown) => revealLossless(data),
    fetch: managementFetch({ onUnauthenticated }),
  });
}

/**
 * The header naming the version of an entity a write is based on, as the
 * server answered it. The server refuses a write of a versioned entity
 * without it, and refuses one based on a version that moved on since, so
 * two people editing the same entity never overwrite each other unseen.
 */
export function ifMatch(version: number): { "If-Match": string } {
  return { "If-Match": `"${version}"` };
}
