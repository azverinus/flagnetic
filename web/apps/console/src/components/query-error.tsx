import { Trans, useLingui } from "@lingui/react/macro";
import { CircleAlertIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";

/**
 * A read that failed, shown where its content would be, with a way to try
 * again and the request ID to quote.
 */
export function QueryError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { i18n } = useLingui();
  const refusal = error instanceof ApiError ? error : new ApiError(0, {});
  const requestId = refusal.requestId;
  return (
    <div
      role="alert"
      className="flex flex-col gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm"
    >
      <p className="flex items-start gap-2">
        <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        <span>{problemMessage(i18n, refusal)}</span>
      </p>
      {requestId ? (
        <p className="text-muted-foreground">
          <Trans>Request ID: {requestId}</Trans>
        </p>
      ) : null}
      {onRetry ? (
        <div>
          <Button variant="outline" size="sm" onClick={onRetry}>
            <Trans>Try again</Trans>
          </Button>
        </div>
      ) : null}
    </div>
  );
}
