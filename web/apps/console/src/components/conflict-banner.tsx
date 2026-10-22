import type { AuditEntry } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import { NamedDiff } from "@/components/structural-diff-view";
import { Button } from "@/components/ui/button";
import { formatDateTime } from "@/i18n/format";
import { diffNamed, type NamedLike } from "@/lib/structural-diff";
import type { FormConflict } from "@/lib/use-form-conflict";

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

export type NamedConflictBannerProps = {
  /** What the form was filled from. */
  base: NamedLike;
  /** The newer version the save met, and the entry that made it. */
  conflict: FormConflict<NamedLike>;
  /** Whether a save is under way, which keeping one's edits starts. */
  pending: boolean;
  /** Fills the form from the newer version, dropping the member's edits. */
  onReload: () => void;
  /** Saves the fields the member changed onto the newer version. */
  onKeep: () => void;
};

/**
 * The conflict banner of a form editing a project, an environment, a
 * unit type or an attribute: who saved and when, what they changed, and
 * the same two ways out every form offers.
 */
export function NamedConflictBanner({
  base,
  conflict,
  pending,
  onReload,
  onKeep,
}: NamedConflictBannerProps) {
  return (
    <ConflictBanner
      subject="form"
      entry={conflict.entry}
      diff={<NamedDiff changes={diffNamed(base, conflict.current)} />}
      actions={
        <>
          <Button type="button" variant="outline" onClick={onReload}>
            <Trans>Reload and lose my edits</Trans>
          </Button>
          <Button type="button" disabled={pending} onClick={onKeep}>
            <Trans>Keep my edits</Trans>
          </Button>
        </>
      }
    />
  );
}
