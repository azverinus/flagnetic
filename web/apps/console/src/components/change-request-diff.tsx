import type {
  ChangeRequest,
  Flag,
  FlagConfiguration,
  PatchOperation,
  ProposedConfiguration,
  ProposedFlagDefinition,
  ProposedSegmentDefinition,
  Segment,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";

import {
  ConfigurationDiff,
  DefinitionDiff,
  SegmentDiff,
} from "@/components/structural-diff-view";
import { diffBasis, type PatchChange, patchChanges } from "@/lib/change-requests";
import { diffConfigurations, diffDefinitions, diffSegments } from "@/lib/structural-diff";

/** What a change request proposes for: a configuration, a flag's definition or a segment's. */
type Subject = "configuration" | "definition" | "segment";

function subjectOf(changeRequest: ChangeRequest): Subject {
  if (changeRequest.kind !== "definition") return "configuration";
  return changeRequest.segment === undefined ? "definition" : "segment";
}

/**
 * What a change request would change. While it waits on the version of
 * the configuration, the flag or the segment it was based on, that is
 * compared with what it proposes, item by item; otherwise only its patch
 * is left, each operation said by what it touches and what it writes. A
 * revert names the version it returns to first.
 */
export function ChangeRequestDiff({
  changeRequest,
  current,
}: {
  changeRequest: ChangeRequest;
  /**
   * What it would change as it is now, once read: the environment's
   * configuration, the flag of a flag's definition, or the segment.
   */
  current: FlagConfiguration | Flag | Segment | undefined;
}) {
  const revertTo = changeRequest.revert_to;
  return (
    <div className="flex flex-col gap-3">
      {revertTo !== null ? (
        <p className="text-sm">
          <Trans>Reverts the configuration to version {revertTo}.</Trans>
        </p>
      ) : null}
      <Changes changeRequest={changeRequest} current={current} />
    </div>
  );
}

/** A member's identifier stands for them where no name is at hand; a proposal never changes the owner. */
const asIs = (member: string) => member;

function Changes({
  changeRequest,
  current,
}: {
  changeRequest: ChangeRequest;
  current: FlagConfiguration | Flag | Segment | undefined;
}) {
  const subject = subjectOf(changeRequest);
  if (current === undefined || diffBasis(changeRequest, current) !== "current") {
    return <PatchDiff diff={changeRequest.diff} subject={subject} />;
  }
  // What a change request proposes follows its kind and the entity it
  // names, and so does what it is compared with.
  switch (subject) {
    case "configuration": {
      const proposed = changeRequest.proposed as ProposedConfiguration;
      return (
        <ConfigurationDiff
          changes={diffConfigurations(current as FlagConfiguration, proposed)}
        />
      );
    }
    case "definition": {
      const flag = current as Flag;
      const proposed = changeRequest.proposed as ProposedFlagDefinition;
      return (
        <DefinitionDiff changes={diffDefinitions(flag, { ...flag, ...proposed })} nameOf={asIs} />
      );
    }
    case "segment": {
      const segment = current as Segment;
      const proposed = changeRequest.proposed as ProposedSegmentDefinition;
      return <SegmentDiff changes={diffSegments(segment, { ...segment, ...proposed })} />;
    }
  }
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
export function PatchDiff({
  diff,
  subject = "configuration",
}: {
  diff: readonly PatchOperation[];
  subject?: Subject;
}) {
  const { t } = useLingui();
  const label = {
    configuration: t`Changes to the configuration`,
    definition: t`Changes to the definition`,
    segment: t`Changes to the segment`,
  }[subject];
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
      aria-label={label}
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
    case "automatic_exposure":
      return t`Automatic exposure`;
    case "private":
      return t`Private`;
    case "other":
      return <Value>{target.path}</Value>;
  }
  const { position, field } = target;
  const item = {
    rule: t`Rule ${position}`,
    override: t`Override ${position}`,
    prerequisite: t`Prerequisite ${position}`,
    variation: t`Variation ${position}`,
    condition: t`Condition ${position}`,
  }[target.kind];
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
  if (typeof change.value === "boolean" && change.op === "replace") {
    switch (change.target.kind) {
      case "on":
        return (
          <li>{change.value ? <Trans>Switched on.</Trans> : <Trans>Switched off.</Trans>}</li>
        );
      case "automatic_exposure":
        return (
          <li>
            {change.value ? (
              <Trans>Automatic exposure switched on.</Trans>
            ) : (
              <Trans>Automatic exposure switched off.</Trans>
            )}
          </li>
        );
      case "private":
        return (
          <li>
            {change.value ? <Trans>Made private.</Trans> : <Trans>No longer private.</Trans>}
          </li>
        );
    }
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
