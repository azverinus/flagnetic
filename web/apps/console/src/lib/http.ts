import { client } from "@flagnetic/api";

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
    return response;
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
    fetch: managementFetch({ onUnauthenticated }),
  });
}
