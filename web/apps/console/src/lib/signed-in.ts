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
};

/** Reads the sign-in page's search parameters, dropping anything unsafe. */
export function validateSignInSearch(search: Record<string, unknown>): SignInSearch {
  const out: SignInSearch = {};
  if (typeof search.returnTo === "string") {
    const target = safeReturnTo(search.returnTo);
    if (target !== "/") out.returnTo = target;
  }
  if (search.ended === true || search.ended === "true") out.ended = true;
  return out;
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
