import type { AuditEntry } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import { formatDateTime } from "@/i18n/format";

export type ConflictBannerProps = {
  /**
   * What the member was editing: a configuration draft, or a form such as
   * a flag's definition.
   */
  subject: "configuration" | "form";
  /** The audit entry recording the newer version, once read; who saved it and when. */
  entry: AuditEntry | undefined;
  /** What the newer version changed from the one the member started from. */
  diff: ReactNode;
  /** The ways out: reload, or keep the member's edits. */
  actions: ReactNode;
};

/**
 * What a write refused because someone saved first shows, in place of a
 * plain error: who saved and when, what they changed, and the member's
 * choices, so a conflict costs seconds rather than the member's edits.
 */
export function ConflictBanner({ subject, entry, diff, actions }: ConflictBannerProps) {
  const { i18n } = useLingui();
  const who = entry?.actor.name;
  const when = entry ? formatDateTime(i18n, entry.occurred_at) : undefined;
  let sentence: ReactNode;
  if (subject === "configuration") {
    sentence =
      who && when ? (
        <Trans>
          {who} saved this configuration on {when}, after this draft was loaded.
        </Trans>
      ) : (
        <Trans>Someone saved this configuration after this draft was loaded.</Trans>
      );
  } else {
    sentence =
      who && when ? (
        <Trans>
          {who} saved a change on {when}, after you opened this.
        </Trans>
      ) : (
        <Trans>Someone saved a change after you opened this.</Trans>
      );
  }
  return (
    <div
      role="alert"
      className="flex flex-col gap-3 rounded-md border border-destructive/50 bg-destructive/5 p-4 text-sm"
    >
      <p className="flex items-start gap-2 font-medium">
        <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        {sentence}
      </p>
      <div className="flex flex-col gap-1.5">
        <p className="text-muted-foreground">
          <Trans>Their change:</Trans>
        </p>
        {diff}
      </div>
      <div className="flex flex-wrap gap-2">{actions}</div>
    </div>
  );
}
