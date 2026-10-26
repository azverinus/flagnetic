import type {
  ChangeRequest,
  FlagConfiguration,
  PatchOperation,
  ProposedConfiguration,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";

import { ConfigurationDiff } from "@/components/structural-diff-view";
import { diffBasis, type PatchChange, patchChanges } from "@/lib/change-requests";
import { diffConfigurations } from "@/lib/structural-diff";

/**
 * What a change request would change. While it waits on the version of
 * the configuration it was based on, that configuration is compared with
 * what it proposes, item by item; otherwise only its patch is left, each
 * operation said by what it touches and what it writes.
 */
export function ChangeRequestDiff({
  changeRequest,
  current,
}: {
  changeRequest: ChangeRequest;
  /** The environment's configuration as it is now, once read. */
  current: FlagConfiguration | undefined;
}) {
  if (
    changeRequest.kind === "flag_configuration" &&
    current !== undefined &&
    diffBasis(changeRequest, current) === "current"
  ) {
    // A configuration's change request proposes a configuration.
    const proposed = changeRequest.proposed as ProposedConfiguration;
    return <ConfigurationDiff changes={diffConfigurations(current, proposed)} />;
  }
  return <PatchDiff diff={changeRequest.diff} />;
}

function Value({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-sm bg-muted px-1 py-0.5 font-mono text-xs break-all">{children}</code>
  );
}

/** A value of a patch as compact JSON, the way the API writes it. */
function json(value: unknown): string {
  return JSON.stringify(value) ?? "null";
}

/** A change request's patch, one sentence per operation. */
export function PatchDiff({ diff }: { diff: readonly PatchOperation[] }) {
  const { t } = useLingui();
  const changes = patchChanges(diff);
  if (changes.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        <Trans>Nothing changes.</Trans>
      </p>
    );
  }
  return (
    <ul
      aria-label={t`Changes to the configuration`}
      className="flex list-disc flex-col gap-1.5 pl-5 text-sm"
    >
      {changes.map((change, index) => (
        <PatchItem key={index} change={change} />
      ))}
    </ul>
  );
}

/** What a patch operation touches, in words. */
function useWhere(change: PatchChange): ReactNode {
  const { t } = useLingui();
  const { target } = change;
  switch (target.kind) {
    case "on":
      return t`Targeting`;
    case "off_variation":
      return t`Off variation`;
    case "fallthrough":
      return t`Fallthrough variation`;
    case "other":
      return <Value>{target.path}</Value>;
  }
  const { position, field } = target;
  const item =
    target.kind === "rule"
      ? t`Rule ${position}`
      : target.kind === "override"
        ? t`Override ${position}`
        : t`Prerequisite ${position}`;
  return field === "" ? (
    item
  ) : (
    <>
      {item}, <Value>{field}</Value>
    </>
  );
}

function PatchItem({ change }: { change: PatchChange }) {
  const where = useWhere(change);
  if (change.target.kind === "on" && typeof change.value === "boolean") {
    return <li>{change.value ? <Trans>Switched on.</Trans> : <Trans>Switched off.</Trans>}</li>;
  }
  const value = <Value>{json(change.value)}</Value>;
  switch (change.op) {
    case "add":
      return (
        <li>
          <Trans>
            Added — {where}: {value}
          </Trans>
        </li>
      );
    case "remove":
      return (
        <li>
          <Trans>Removed — {where}</Trans>
        </li>
      );
    case "replace":
      return (
        <li>
          <Trans>
            Changed — {where}: {value}
          </Trans>
        </li>
      );
  }
}
