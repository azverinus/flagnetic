import { Trans } from "@lingui/react/macro";
import { LockIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";

/** Beside an environment's name, when its changes need a change request. */
export function ProtectedMark() {
  return (
    <Badge variant="muted" className="inline-flex items-center gap-1">
      <LockIcon aria-hidden className="size-3" />
      <Trans>Protected</Trans>
    </Badge>
  );
}

/** An environment's name, with the mark when it is protected. */
export function EnvironmentName({
  environment,
}: {
  environment: { name: string; protected: boolean };
}) {
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      {environment.name}
      {environment.protected ? <ProtectedMark /> : null}
    </span>
  );
}
