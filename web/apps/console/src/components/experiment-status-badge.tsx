import type { ExperimentStatus } from "@flagnetic/api";
import { useLingui } from "@lingui/react/macro";

import { Badge } from "@/components/ui/badge";
import { experimentStatusName } from "@/lib/experiments";

/**
 * An experiment's status as a short label: a running experiment stands
 * out, since it is the one changing what users get; the others are plain.
 */
export function ExperimentStatusBadge({ status }: { status: ExperimentStatus }) {
  const { i18n } = useLingui();
  return (
    <Badge variant={status === "running" ? "default" : "muted"} className="whitespace-nowrap">
      {experimentStatusName(i18n, status)}
    </Badge>
  );
}
