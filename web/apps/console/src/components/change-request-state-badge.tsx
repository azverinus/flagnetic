import type { ChangeRequestState } from "@flagnetic/api";
import { useLingui } from "@lingui/react/macro";

import { Badge } from "@/components/ui/badge";
import { changeRequestStateName } from "@/lib/change-requests";

/**
 * A change request's state as a short label: those waiting on someone,
 * open or approved, stand out; drafts and final states are plain.
 */
export function ChangeRequestStateBadge({ state }: { state: ChangeRequestState }) {
  const { i18n } = useLingui();
  return (
    <Badge
      variant={state === "open" || state === "approved" ? "default" : "muted"}
      className="whitespace-nowrap"
    >
      {changeRequestStateName(i18n, state)}
    </Badge>
  );
}
