import type { Condition, Serve, Variation } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";

import { formatDateTime } from "@/i18n/format";
import { describeCondition, describeServe } from "@/lib/conditions";
import type {
  ConfigurationChange,
  DefinitionChange,
  OverrideValue,
  RuleDetail,
  RuleLike,
  SegmentChange,
} from "@/lib/structural-diff";

/** A value as the change names it, set apart from the sentence around it. */
function Value({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-sm bg-muted px-1 py-0.5 font-mono text-xs break-all">{children}</code>
  );
}

function Nothing() {
  return (
    <p className="text-sm text-muted-foreground">
      <Trans>Nothing that a member edits changed.</Trans>
    </p>
  );
}

function ChangeList({ label, children }: { label: string; children: ReactNode }) {
  return (
    <ul aria-label={label} className="flex list-disc flex-col gap-1.5 pl-5 text-sm">
      {children}
    </ul>
  );
}

function ConditionText({ condition }: { condition: Condition }) {
  const { i18n } = useLingui();
  return <Value>{describeCondition(i18n, condition)}</Value>;
}

function ServeText({ serve }: { serve: Serve }) {
  const { i18n } = useLingui();
  return <Value>{describeServe(i18n, serve)}</Value>;
}

function OverrideText({ value }: { value: OverrideValue }) {
  const { variation, note } = value;
  return note ? (
    <Trans>
      <Value>{variation}</Value> (note: {note})
    </Trans>
  ) : (
    <Value>{variation}</Value>
  );
}

/** A rule by its description, or by what it serves when it has none. */
function RuleName({ rule }: { rule: RuleLike }) {
  return rule.description ? <>“{rule.description}”</> : <ServeText serve={rule.serve} />;
}

function RuleDetailItem({ detail }: { detail: RuleDetail }) {
  if (detail.kind === "description") {
    const { before, after } = detail;
    return (
      <li>
        <Trans>
          Description changed from “{before}” to “{after}”.
        </Trans>
      </li>
    );
  }
  if (detail.kind === "serve") {
    return (
      <li>
        <Trans>
          Serves <ServeText serve={detail.after} /> instead of <ServeText serve={detail.before} />.
        </Trans>
      </li>
    );
  }
  const { position } = detail;
  switch (detail.change) {
    case "added":
      return (
        <li>
          <Trans>
            Condition {position} added: <ConditionText condition={detail.after!} />.
          </Trans>
        </li>
      );
    case "removed":
      return (
        <li>
          <Trans>
            Condition {position} removed: <ConditionText condition={detail.before!} />.
          </Trans>
        </li>
      );
    case "changed":
      return (
        <li>
          <Trans>
            Condition {position} changed from <ConditionText condition={detail.before!} /> to{" "}
            <ConditionText condition={detail.after!} />.
          </Trans>
        </li>
      );
  }
}

function ConfigurationChangeItem({ change }: { change: ConfigurationChange }) {
  const { t } = useLingui();
  switch (change.kind) {
    case "on":
      return <li>{change.after ? t`Switched on.` : t`Switched off.`}</li>;
    case "off_variation": {
      const { before, after } = change;
      return (
        <li>
          <Trans>
            Off variation changed from <Value>{before}</Value> to <Value>{after}</Value>.
          </Trans>
        </li>
      );
    }
    case "fallthrough": {
      const { before, after } = change;
      return (
        <li>
          <Trans>
            Fallthrough variation changed from <Value>{before}</Value> to <Value>{after}</Value>.
          </Trans>
        </li>
      );
    }
    case "prerequisite": {
      const { flag, before, after } = change;
      if (change.change === "added") {
        return (
          <li>
            <Trans>
              Prerequisite added: <Value>{flag}</Value> serves <Value>{after}</Value>.
            </Trans>
          </li>
        );
      }
      if (change.change === "removed") {
        return (
          <li>
            <Trans>
              Prerequisite removed: <Value>{flag}</Value> serves <Value>{before}</Value>.
            </Trans>
          </li>
        );
      }
      return (
        <li>
          <Trans>
            Prerequisite <Value>{flag}</Value> requires <Value>{after}</Value> instead of{" "}
            <Value>{before}</Value>.
          </Trans>
        </li>
      );
    }
    case "override": {
      const { unitType, unitId } = change;
      const unit = (
        <Value>
          {unitType} {unitId}
        </Value>
      );
      if (change.change === "added") {
        return (
          <li>
            <Trans>
              Override added: {unit} receives <OverrideText value={change.after!} />.
            </Trans>
          </li>
        );
      }
      if (change.change === "removed") {
        return (
          <li>
            <Trans>
              Override removed: {unit} received <OverrideText value={change.before!} />.
            </Trans>
          </li>
        );
      }
      return (
        <li>
          <Trans>
            Override of {unit} changed from <OverrideText value={change.before!} /> to{" "}
            <OverrideText value={change.after!} />.
          </Trans>
        </li>
      );
    }
    case "rule": {
      const { position } = change;
      if (change.change !== "changed") {
        return change.change === "added" ? (
          <li>
            <Trans>
              Rule {position} added: <RuleName rule={change.rule} />.
            </Trans>
          </li>
        ) : (
          <li>
            <Trans>
              Rule {position} removed: <RuleName rule={change.rule} />.
            </Trans>
          </li>
        );
      }
      const { movedFrom } = change;
      return (
        <li>
          {movedFrom === undefined ? (
            <Trans>Rule {position} changed.</Trans>
          ) : change.details.length === 0 ? (
            <Trans>
              Rule {position} moved here from position {movedFrom}.
            </Trans>
          ) : (
            <Trans>
              Rule {position} moved here from position {movedFrom} and changed.
            </Trans>
          )}
          {change.details.length > 0 ? (
            <ul className="mt-1 flex list-[circle] flex-col gap-1 pl-5">
              {change.details.map((detail, index) => (
                <RuleDetailItem key={index} detail={detail} />
              ))}
            </ul>
          ) : null}
        </li>
      );
    }
  }
}

/** What changed in a flag's configuration in one environment, item by item. */
export function ConfigurationDiff({ changes }: { changes: readonly ConfigurationChange[] }) {
  const { t } = useLingui();
  if (changes.length === 0) return <Nothing />;
  return (
    <ChangeList label={t`Changes to the configuration`}>
      {changes.map((change, index) => (
        <ConfigurationChangeItem key={index} change={change} />
      ))}
    </ChangeList>
  );
}

function VariationText({ variation }: { variation: Variation }) {
  return <Value>{JSON.stringify(variation.value)}</Value>;
}

function DefinitionChangeItem({
  change,
  nameOf,
}: {
  change: DefinitionChange;
  nameOf: (member: string) => string;
}) {
  const { i18n, t } = useLingui();
  switch (change.kind) {
    case "field": {
      const text = (value: unknown): string => {
        if (change.field === "owner") return nameOf(String(value));
        if (change.field === "lifetime") return value === "permanent" ? t`permanent` : t`temporary`;
        if (change.field === "expected_removal_at") {
          return typeof value === "string" ? formatDateTime(i18n, value) : t`none`;
        }
        return String(value);
      };
      const [before, after] = [text(change.before), text(change.after)];
      switch (change.field) {
        case "name":
          return (
            <li>
              <Trans>
                Name changed from “{before}” to “{after}”.
              </Trans>
            </li>
          );
        case "description":
          return (
            <li>
              <Trans>Description changed.</Trans>
            </li>
          );
        case "owner":
          return (
            <li>
              <Trans>
                Owner changed from {before} to {after}.
              </Trans>
            </li>
          );
        case "lifetime":
          return (
            <li>
              <Trans>
                Lifetime changed from {before} to {after}.
              </Trans>
            </li>
          );
        case "expected_removal_at":
          return (
            <li>
              <Trans>
                Expected removal changed from {before} to {after}.
              </Trans>
            </li>
          );
        case "automatic_exposure":
          return (
            <li>
              {change.after
                ? t`Automatic exposure switched on.`
                : t`Automatic exposure switched off.`}
            </li>
          );
        case "archived":
          return <li>{change.after ? t`Archived.` : t`Restored.`}</li>;
      }
      return null;
    }
    case "tag": {
      const { tag } = change;
      return (
        <li>
          {change.change === "added" ? (
            <Trans>
              Tag added: <Value>{tag}</Value>.
            </Trans>
          ) : (
            <Trans>
              Tag removed: <Value>{tag}</Value>.
            </Trans>
          )}
        </li>
      );
    }
    case "variation": {
      const { key } = change;
      if (change.change === "added") {
        return (
          <li>
            <Trans>
              Variation <Value>{key}</Value> added, serving{" "}
              <VariationText variation={change.after!} />.
            </Trans>
          </li>
        );
      }
      if (change.change === "removed") {
        return (
          <li>
            <Trans>
              Variation <Value>{key}</Value> removed.
            </Trans>
          </li>
        );
      }
      const valueChanged =
        JSON.stringify(change.before!.value) !== JSON.stringify(change.after!.value);
      return (
        <li>
          {valueChanged ? (
            <Trans>
              Variation <Value>{key}</Value> serves <VariationText variation={change.after!} />{" "}
              instead of <VariationText variation={change.before!} />.
            </Trans>
          ) : (
            <Trans>
              Variation <Value>{key}</Value> has a new name or description.
            </Trans>
          )}
        </li>
      );
    }
    case "variation_order": {
      const order = change.after.join(", ");
      return (
        <li>
          <Trans>
            Variations reordered: <Value>{order}</Value>.
          </Trans>
        </li>
      );
    }
  }
}

function SegmentChangeItem({ change }: { change: SegmentChange }) {
  if (change.kind === "condition") return <RuleDetailItem detail={change} />;
  switch (change.field) {
    case "name": {
      const [before, after] = [String(change.before), String(change.after)];
      return (
        <li>
          <Trans>
            Name changed from “{before}” to “{after}”.
          </Trans>
        </li>
      );
    }
    case "description":
      return (
        <li>
          <Trans>Description changed.</Trans>
        </li>
      );
    case "private":
      return (
        <li>{change.after ? <Trans>Made private.</Trans> : <Trans>No longer private.</Trans>}</li>
      );
  }
}

/** What changed in a segment, item by item. */
export function SegmentDiff({ changes }: { changes: readonly SegmentChange[] }) {
  const { t } = useLingui();
  if (changes.length === 0) return <Nothing />;
  return (
    <ChangeList label={t`Changes to the segment`}>
      {changes.map((change, index) => (
        <SegmentChangeItem key={index} change={change} />
      ))}
    </ChangeList>
  );
}

/** What changed in a flag's definition, item by item. */
export function DefinitionDiff({
  changes,
  nameOf,
}: {
  changes: readonly DefinitionChange[];
  /** A member's name by identifier, for a change of owner. */
  nameOf: (member: string) => string;
}) {
  const { t } = useLingui();
  if (changes.length === 0) return <Nothing />;
  return (
    <ChangeList label={t`Changes to the definition`}>
      {changes.map((change, index) => (
        <DefinitionChangeItem key={index} change={change} nameOf={nameOf} />
      ))}
    </ChangeList>
  );
}
