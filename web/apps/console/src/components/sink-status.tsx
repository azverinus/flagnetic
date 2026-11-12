import type { Sink } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";

import { Badge } from "@/components/ui/badge";
import { healthName, healthTone } from "@/lib/sinks";

/** A sink's health, in a word. */
export function SinkHealthBadge({ sink }: { sink: Sink }) {
  const { i18n } = useLingui();
  return <Badge variant={healthTone(sink.health)}>{healthName(i18n, sink.health)}</Badge>;
}

/** Whether retention is about to drop events the sink has not delivered. */
export function SinkAtRisk({ sink }: { sink: Sink }) {
  return sink.at_risk ? (
    <Badge variant="destructive">
      <Trans>At risk</Trans>
    </Badge>
  ) : (
    <span className="text-muted-foreground">
      <Trans>No</Trans>
    </span>
  );
}
