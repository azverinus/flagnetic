import { getAuthMethods, getMe, type AuthMethods, type Me } from "@flagnetic/api";
import { queryOptions } from "@tanstack/react-query";

import { unwrap } from "./errors";

/** The caller: who is signed in, and with which credential. */
export const meQuery = queryOptions<Me>({
  queryKey: ["me"],
  queryFn: () => unwrap(getMe()),
  staleTime: 60_000,
  retry: false,
});

/** The ways this instance lets a member sign in, read before anyone is signed in. */
export const authMethodsQuery = queryOptions<AuthMethods>({
  queryKey: ["auth-methods"],
  queryFn: () => unwrap(getAuthMethods()),
  staleTime: 5 * 60_000,
});
