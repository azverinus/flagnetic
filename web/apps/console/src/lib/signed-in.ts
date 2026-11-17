import type { Me } from "@flagnetic/api";
import type { QueryClient } from "@tanstack/react-query";
import { redirect } from "@tanstack/react-router";

import { ApiError } from "./errors";
import { meQuery } from "./queries";
import { locationOf, safeReturnTo } from "./return-to";

/** The search parameters of the sign-in page. */
export type SignInSearch = {
  /** Where to go once signed in: a path on this origin. */
  returnTo?: string;
  /** Set when the member was sent here because their session ended. */
  ended?: boolean;
  /** The code a failed single sign-on sent the member back with. */
  error?: string;
  /** The slug of the provider the error names, as `sso_required` does. */
  provider?: string;
};

/** What a code or slug in the address may look like; anything else is dropped. */
const errorCodePattern = /^[a-z][a-z0-9_]{0,63}$/;
const providerSlugPattern = /^[a-z][a-z0-9_-]{0,63}$/;

/** Reads the sign-in page's search parameters, dropping anything unsafe. */
export function validateSignInSearch(search: Record<string, unknown>): SignInSearch {
  const out: SignInSearch = {};
  if (typeof search.returnTo === "string") {
    const target = safeReturnTo(search.returnTo);
    if (target !== "/") out.returnTo = target;
  }
  if (search.ended === true || search.ended === "true") out.ended = true;
  if (typeof search.error === "string" && errorCodePattern.test(search.error)) {
    out.error = search.error;
    if (typeof search.provider === "string" && providerSlugPattern.test(search.provider)) {
      out.provider = search.provider;
    }
  }
  return out;
}

/** The search parameters of the sign-in methods, and of the settings a failed linking returns to. */
export type LinkErrorSearch = {
  /** The code a failed linking sent the member back with. */
  error?: string;
};

/** Reads the code a failed linking returned the member with, dropping anything unsafe. */
export function validateLinkErrorSearch(search: Record<string, unknown>): LinkErrorSearch {
  return typeof search.error === "string" && errorCodePattern.test(search.error)
    ? { error: search.error }
    : {};
}

/** The sign-in page's path. */
const signInPath = "/sign-in";

type Where = { pathname: string; searchStr?: string; hash?: string };

/**
 * The search of the sign-in page for a member who was at where: they come
 * back to it, and are told their session ended when it did.
 */
export function signInSearch(where: Where, ended: boolean): SignInSearch {
  const search: SignInSearch = {};
  const target = safeReturnTo(locationOf(where));
  if (target !== "/" && where.pathname !== signInPath) search.returnTo = target;
  if (ended) search.ended = true;
  return search;
}

/**
 * Guards a signed-in route: answers the caller, or sends a visitor to
 * sign-in keeping where they were.
 */
export async function requireSignedIn(queryClient: QueryClient, where: Where): Promise<Me> {
  try {
    return await queryClient.ensureQueryData(meQuery);
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      throw redirect({
        to: "/sign-in",
        search: signInSearch(where, error.code === "session_expired"),
      });
    }
    throw error;
  }
}

/**
 * What the console does when any request is refused as not signed in. A
 * member who was signed in goes back to sign-in, told their session ended
 * and keeping where they were; a visitor who never signed in is left
 * alone, because the guard already sends them there. Once the console is
 * on its way to sign-in, whatever took it there already carries where the
 * member was, so this navigates no second time.
 */
export function onSessionEnded(
  queryClient: QueryClient,
  where: () => Where,
  navigate: (search: SignInSearch) => void,
): () => void {
  return () => {
    if (queryClient.getQueryData(meQuery.queryKey) === undefined) return;
    const here = where();
    queryClient.clear();
    if (here.pathname === signInPath) return;
    navigate(signInSearch(here, true));
  };
}

/** What one tab tells the others about the session they share. */
export type SessionChange = "signed-in" | "signed-out";

const sessionChannelName = "flagnetic-session";

/**
 * Tells the other tabs of this browser that a member signed in or out here.
 * The session is one cookie shared by every tab, so a tab that does not
 * hear of it keeps showing the member's data after they signed out, and
 * keeps the first member's identity while the cookie is the second's.
 * Nothing is sent where the browser has no broadcast channel; the other
 * tabs then learn of it from their next refused request, as before.
 */
export function announceSessionChange(change: SessionChange): void {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel(sessionChannelName);
  channel.postMessage(change);
  channel.close();
}

/** Calls `onChange` when another tab announces a session change; returns how to stop. */
export function listenForSessionChange(onChange: (change: SessionChange) => void): () => void {
  if (typeof BroadcastChannel === "undefined") return () => undefined;
  const channel = new BroadcastChannel(sessionChannelName);
  channel.onmessage = (event: MessageEvent<unknown>) => {
    if (event.data === "signed-in" || event.data === "signed-out") onChange(event.data);
  };
  return () => channel.close();
}

/**
 * What this tab does when another tab changed the session. After a sign-out
 * it goes to sign-in as it does when its own session ends, and shows nothing
 * of the member's data. After a sign-in, which may be another member's, what
 * it holds of the first member is dropped and read again under the cookie
 * there now is, by `reload`. A tab that holds nobody has nothing to drop.
 */
export function onSessionChangedElsewhere(
  queryClient: QueryClient,
  where: () => Where,
  navigate: (search: SignInSearch) => void,
  reload: () => void,
): (change: SessionChange) => void {
  const ended = onSessionEnded(queryClient, where, navigate);
  return (change) => {
    if (change === "signed-out") {
      ended();
      return;
    }
    if (queryClient.getQueryData(meQuery.queryKey) === undefined) return;
    queryClient.clear();
    reload();
  };
}
