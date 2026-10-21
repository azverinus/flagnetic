import type { I18n } from "@lingui/core";
import { MutationCache, QueryClient } from "@tanstack/react-query";

import { ApiError } from "./errors";
import { auditQueryKey } from "./queries";
import { toastError } from "./toast";

/** A refusal is the server's answer; only a failure to reach it is worth retrying. */
function retry(count: number, error: unknown): boolean {
  return count < 2 && error instanceof ApiError && (error.status === 0 || error.status >= 500);
}

/**
 * The console's query client. A refused change is toasted unless the
 * screen explains it in place, and an ended session goes back to sign-in
 * instead. Every change to a project or the instance that succeeds is
 * recorded in an audit log, so the logs shown, such as an overview's
 * recent activity, are read again; signing in and out opt out.
 */
export function createQueryClient(i18n: I18n): QueryClient {
  const queryClient: QueryClient = new QueryClient({
    defaultOptions: { queries: { retry } },
    mutationCache: new MutationCache({
      onSuccess: (_data, _variables, _context, mutation) => {
        if (mutation.meta?.readsAuditAgain === false) return;
        void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      },
      onError: (error, _variables, _context, mutation) => {
        if (mutation.meta?.toast === false || (error instanceof ApiError && error.status === 401)) {
          return;
        }
        toastError(i18n, error);
      },
    }),
  });
  return queryClient;
}
