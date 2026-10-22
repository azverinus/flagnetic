import type { AttributeType, Segment } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { XIcon } from "lucide-react";
import { type KeyboardEvent, useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { cn } from "@/lib/cn";
import {
  type AttributeOption,
  type ConditionDraft,
  entryType,
  isFullSemver,
  localDateTime,
  operatorLabel,
  operatorsByType,
  rfc3339,
  valueShape,
  withAttribute,
  withOperator,
} from "@/lib/conditions";

export type ConditionRowProps = {
  /** The condition's place in its rule, from 1. */
  position: number;
  condition: ConditionDraft;
  onChange: (next: ConditionDraft) => void;
  onRemove: () => void;
  attributes: readonly AttributeOption[];
  segments: readonly Segment[];
  /** The project's other flags that are not archived, with their variations' keys. */
  flags: ReadonlyMap<string, readonly string[]>;
  /** What is wrong with the condition, the editor's own finding or the server's. */
  error?: string;
  disabled?: boolean;
};

/**
 * One condition of a rule: an attribute compared by an operator fitting
 * its type with a value typed for it, membership of a segment, or another
 * flag's variation. Every control is named by the condition's place, so
 * a screen reader tells the rows of a rule apart.
 */
export function ConditionRow({
  position,
  condition,
  onChange,
  onRemove,
  attributes,
  segments,
  flags,
  error,
  disabled = false,
}: ConditionRowProps) {
  const { i18n, t } = useLingui();
  const errorId = useId();
  const described = error ? errorId : undefined;
  const invalid = error ? true : undefined;
  const attribute = attributes.find((option) => option.key === condition.attribute);
  const type: AttributeType = attribute?.type ?? "string";

  let fields;
  if (condition.kind === "attribute") {
    const shape = valueShape(type, condition.op);
    const operators = attribute ? operatorsByType[type] : [];
    fields = (
      <>
        <Select
          aria-label={t`Attribute (condition ${position})`}
          value={condition.attribute}
          placeholder={t`Choose an attribute`}
          disabled={disabled}
          aria-invalid={invalid}
          aria-describedby={described}
          onValueChange={(key) => {
            const next = attributes.find((option) => option.key === key);
            if (next) onChange(withAttribute(condition, key, next.type));
          }}
          className="sm:w-48"
        >
          {attributes.map(({ key, name, unitType }) => (
            <SelectItem key={`${unitType}-${key}`} value={key}>
              {unitType ? t`${name} (unit ID)` : name}
            </SelectItem>
          ))}
          {condition.attribute && !attribute ? (
            <SelectItem value={condition.attribute}>{condition.attribute}</SelectItem>
          ) : null}
        </Select>
        <Select
          aria-label={t`Operator (condition ${position})`}
          value={condition.op}
          disabled={disabled || !attribute}
          onValueChange={(op) => onChange(withOperator(condition, op, type))}
          className="sm:w-48"
        >
          {operators.map((op) => (
            <SelectItem key={op} value={op}>
              {operatorLabel(i18n, op)}
            </SelectItem>
          ))}
          {condition.op && !operators.includes(condition.op as never) ? (
            <SelectItem value={condition.op}>{operatorLabel(i18n, condition.op)}</SelectItem>
          ) : null}
        </Select>
        {shape === "none" ? (
          <p className="self-center text-sm text-muted-foreground">
            <Trans>No value needed.</Trans>
          </p>
        ) : shape === "list" ? (
          <ListValueInput
            label={t`Value (condition ${position})`}
            type={type}
            value={condition.list}
            onChange={(list) => onChange({ ...condition, list })}
            disabled={disabled}
            invalid={invalid}
            describedBy={described}
          />
        ) : (
          <ScalarValueInput
            label={t`Value (condition ${position})`}
            type={type}
            value={condition.text}
            onChange={(text) => onChange({ ...condition, text })}
            disabled={disabled}
            invalid={invalid}
            describedBy={described}
          />
        )}
      </>
    );
  } else if (condition.kind === "prerequisite") {
    const variations = flags.get(condition.flag) ?? [];
    fields = (
      <>
        <Select
          aria-label={t`Flag (condition ${position})`}
          value={condition.flag}
          placeholder={t`Choose a flag`}
          disabled={disabled}
          aria-invalid={invalid}
          aria-describedby={described}
          onValueChange={(flag) => onChange({ ...condition, flag, variation: "" })}
          className="sm:w-56"
        >
          {[...flags.keys()].map((key) => (
            <SelectItem key={key} value={key}>
              {key}
            </SelectItem>
          ))}
          {condition.flag && !flags.has(condition.flag) ? (
            <SelectItem value={condition.flag}>{condition.flag}</SelectItem>
          ) : null}
        </Select>
        <Select
          aria-label={t`Required variation (condition ${position})`}
          value={condition.variation}
          placeholder={t`Choose a variation`}
          disabled={disabled || !condition.flag}
          onValueChange={(variation) => onChange({ ...condition, variation })}
          className="sm:w-48"
        >
          {variations.map((key) => (
            <SelectItem key={key} value={key}>
              {key}
            </SelectItem>
          ))}
          {condition.variation && !variations.includes(condition.variation) ? (
            <SelectItem value={condition.variation}>{condition.variation}</SelectItem>
          ) : null}
        </Select>
      </>
    );
  } else {
    const chosen = segments.find((segment) => segment.key === condition.segment);
    fields = (
      <>
        <Select
          aria-label={t`Membership (condition ${position})`}
          value={condition.kind}
          disabled={disabled}
          onValueChange={(kind) =>
            onChange({ ...condition, kind: kind as "in_segment" | "not_in_segment" })
          }
          className="sm:w-48"
        >
          <SelectItem value="in_segment">{t`is in the segment`}</SelectItem>
          <SelectItem value="not_in_segment">{t`is not in the segment`}</SelectItem>
        </Select>
        <Select
          aria-label={t`Segment (condition ${position})`}
          value={condition.segment}
          placeholder={t`Choose a segment`}
          disabled={disabled}
          aria-invalid={invalid}
          aria-describedby={described}
          onValueChange={(segment) => onChange({ ...condition, segment })}
          className="sm:w-56"
        >
          {segments.map((segment) => (
            <SelectItem key={segment.key} value={segment.key}>
              {segment.name}
            </SelectItem>
          ))}
          {condition.segment && !chosen ? (
            <SelectItem value={condition.segment}>{condition.segment}</SelectItem>
          ) : null}
        </Select>
        {chosen?.private ? (
          <p className="self-center text-sm text-muted-foreground">
            <Trans>Private: evaluated on the server only.</Trans>
          </p>
        ) : null}
      </>
    );
  }

  return (
    <li>
      <div
        role="group"
        aria-label={t`Condition ${position}`}
        className="flex flex-col gap-2 rounded-md border bg-muted/20 p-3"
      >
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-start">
          {fields}
          {disabled ? null : (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t`Remove condition ${position}`}
              onClick={onRemove}
              className="sm:ml-auto"
            >
              <XIcon aria-hidden />
            </Button>
          )}
        </div>
        {error ? (
          <p id={errorId} role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </div>
    </li>
  );
}

export type ValueInputProps<T> = {
  label: string;
  type: AttributeType;
  value: T;
  onChange: (value: T) => void;
  disabled: boolean;
  invalid?: boolean;
  describedBy?: string;
};

/** One value typed for the attribute: a choice for a boolean, a date and time for a date. */
export function ScalarValueInput({
  label,
  type,
  value,
  onChange,
  disabled,
  invalid,
  describedBy,
}: ValueInputProps<string>) {
  const { t } = useLingui();
  const common = {
    "aria-label": label,
    "aria-invalid": invalid,
    "aria-describedby": describedBy,
    disabled,
  };
  switch (entryType(type)) {
    case "boolean":
      return (
        <Select {...common} value={value} onValueChange={onChange} className="sm:w-32">
          <SelectItem value="true">true</SelectItem>
          <SelectItem value="false">false</SelectItem>
        </Select>
      );
    case "date":
      // The field shows the member's own time; the condition keeps the instant.
      return (
        <Input
          {...common}
          type="datetime-local"
          value={value ? localDateTime(value) : ""}
          onChange={(event) => onChange(rfc3339(event.target.value) ?? "")}
          className="sm:w-56"
        />
      );
    case "number":
      return (
        <Input
          {...common}
          type="number"
          step="any"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="sm:w-40"
        />
      );
    case "semver":
      return (
        <Input
          {...common}
          autoComplete="off"
          placeholder={t`1.41.0`}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="font-mono sm:w-40"
        />
      );
    default:
      return (
        <Input
          {...common}
          autoComplete="off"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="sm:min-w-48 sm:flex-1"
        />
      );
  }
}

/**
 * A list of values typed one at a time, each added with Enter or a comma
 * as a chip with a button removing it; a value already in the list, or
 * one that is not of the attribute's type, is not added.
 */
export function ListValueInput({
  label,
  type,
  value,
  onChange,
  disabled,
  invalid,
  describedBy,
}: ValueInputProps<string[]>) {
  const { t } = useLingui();
  const hintId = useId();
  const [typed, setTyped] = useState("");
  const [refused, setRefused] = useState<string>();
  const kind = entryType(type);

  const add = () => {
    const entry = typed.trim();
    if (entry === "") return;
    if (kind === "number" && !Number.isFinite(Number(entry))) {
      setRefused(t`${entry} is not a number.`);
      return;
    }
    if (kind === "semver" && !isFullSemver(entry)) {
      setRefused(t`${entry} is not a full semantic version, such as 1.41.0.`);
      return;
    }
    setRefused(undefined);
    setTyped("");
    if (!value.includes(entry)) onChange([...value, entry]);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    // Enter never submits the enclosing form, even with nothing typed:
    // leaving the field adds what was typed and empties it, so an Enter
    // pressed after focus came back (a closing menu returns focus to its
    // trigger, a person clicks back in) would otherwise submit the form
    // instead of finishing the value.
    if (event.key === "Enter" || (event.key === "," && typed.trim() !== "")) {
      event.preventDefault();
      add();
    }
  };

  return (
    <div className="flex min-w-0 flex-col gap-1 sm:flex-1">
      <div
        className={cn(
          "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-md border border-input bg-background px-2 py-1 shadow-xs",
          (invalid || refused) && "border-destructive",
        )}
      >
        {value.length > 0 ? (
          <ul aria-label={t`Values of ${label}`} className="flex flex-wrap gap-1.5">
            {value.map((entry) => (
              <li
                key={entry}
                className="flex items-center gap-1 rounded-sm bg-muted py-0.5 pr-0.5 pl-2 font-mono text-xs"
              >
                {entry}
                {disabled ? null : (
                  <button
                    type="button"
                    aria-label={t`Remove ${entry}`}
                    className="rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                    onClick={() => onChange(value.filter((other) => other !== entry))}
                  >
                    <XIcon aria-hidden className="size-3" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        ) : null}
        <input
          aria-label={label}
          aria-invalid={invalid || refused ? true : undefined}
          aria-describedby={[hintId, describedBy].filter(Boolean).join(" ")}
          autoComplete="off"
          disabled={disabled}
          className="h-7 min-w-24 flex-1 rounded-sm bg-transparent px-1 text-sm"
          placeholder={kind === "semver" ? t`1.41.0, then Enter` : t`A value, then Enter`}
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
            setRefused(undefined);
          }}
          onKeyDown={onKeyDown}
          onBlur={add}
        />
      </div>
      <p
        id={hintId}
        className={cn("text-xs", refused ? "text-destructive" : "text-muted-foreground")}
      >
        {refused ?? <Trans>Press Enter after each value.</Trans>}
      </p>
    </div>
  );
}
