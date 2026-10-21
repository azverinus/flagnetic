import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { toast } from "sonner";

import { ApiError } from "./errors";
import { problemMessage } from "./problem-messages";

/**
 * Tells the member a change was refused, with the request ID to quote to
 * an operator, who finds the request's log line by it.
 */
export function toastError(i18n: I18n, error: unknown): void {
  const refusal = error instanceof ApiError ? error : new ApiError(0, {});
  const requestId = refusal.requestId;
  toast.error(problemMessage(i18n, refusal), {
    description: requestId ? i18n._(msg`Request ID: ${requestId}`) : undefined,
  });
}
