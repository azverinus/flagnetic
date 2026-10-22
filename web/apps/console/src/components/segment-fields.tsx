import { Trans, useLingui } from "@lingui/react/macro";
import { PlusIcon } from "lucide-react";
import { useId } from "react";

import { ConditionRow } from "@/components/condition-row";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { type AttributeOption, newAttributeCondition } from "@/lib/conditions";
import { formatNumber } from "@/i18n/format";
import { maxConditions } from "@/lib/rule-editor";
import type { SegmentDraft } from "@/lib/segments";

export type SegmentFieldsProps = {
  draft: SegmentDraft;
  onChange: (next: SegmentDraft) => void;
  /** The attributes and unit types a condition can name. */
  attributes: readonly AttributeOption[];
  /** What is wrong, by a field's name or a condition's key. */
  errors: ReadonlyMap<string, string>;
  disabled?: boolean;
};

/** No segment and no flag: a segment's conditions name attributes only. */
const noSegments: never[] = [];
const noFlags = new Map<string, readonly string[]>();

/**
 * What a segment holds, its key aside: a name, a description, the private
 * switch, and the attribute conditions that must all hold, each edited in
 * the same row as a rule's conditions.
 */
export function SegmentFields({
  draft,
  onChange,
  attributes,
  errors,
  disabled = false,
}: SegmentFieldsProps) {
  const { i18n } = useLingui();
  const privateId = useId();
  const privateHint = useId();
  const conditionsHeading = useId();
  const full = draft.conditions.length >= maxConditions;
  const maxConditionsText = formatNumber(i18n, maxConditions);

  return (
    <>
      <Field label={<Trans>Name</Trans>} error={errors.get("name")}>
        <Input
          autoComplete="off"
          disabled={disabled}
          value={draft.name}
          onChange={(event) => onChange({ ...draft, name: event.target.value })}
        />
      </Field>
      <Field label={<Trans>Description</Trans>} error={errors.get("description")}>
        <Textarea
          rows={3}
          disabled={disabled}
          value={draft.description}
          onChange={(event) => onChange({ ...draft, description: event.target.value })}
        />
      </Field>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <Switch
            id={privateId}
            aria-describedby={privateHint}
            checked={draft.private}
            disabled={disabled}
            onCheckedChange={(checked) => onChange({ ...draft, private: checked })}
          />
          <label htmlFor={privateId} className="text-sm font-medium">
            <Trans>Private</Trans>
          </label>
        </div>
        <p id={privateHint} className="text-sm text-muted-foreground">
          <Trans>
            Its conditions never leave the server: only server-side evaluation uses it, and a client
            SDK key never receives it.
          </Trans>
        </p>
        {errors.get("private") ? (
          <p role="alert" className="text-sm text-destructive">
            {errors.get("private")}
          </p>
        ) : null}
      </div>
      <section aria-labelledby={conditionsHeading} className="flex flex-col gap-2">
        <h2 id={conditionsHeading} className="text-sm font-medium">
          <Trans>Conditions (all must hold)</Trans>
        </h2>
        <p className="text-sm text-muted-foreground">
          <Trans>
            A segment compares declared attributes only; one without conditions holds every context.
          </Trans>
        </p>
        {draft.conditions.length > 0 ? (
          <ol className="flex flex-col gap-2">
            {draft.conditions.map((condition, index) => (
              <ConditionRow
                key={condition.key}
                position={index + 1}
                condition={condition}
                onChange={(next) =>
                  onChange({
                    ...draft,
                    conditions: draft.conditions.map((other) =>
                      other.key === condition.key ? next : other,
                    ),
                  })
                }
                onRemove={() =>
                  onChange({
                    ...draft,
                    conditions: draft.conditions.filter((other) => other.key !== condition.key),
                  })
                }
                attributes={attributes}
                segments={noSegments}
                flags={noFlags}
                error={errors.get(condition.key)}
                disabled={disabled}
              />
            ))}
          </ol>
        ) : null}
        {disabled ? null : (
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={full || attributes.length === 0}
              onClick={() => {
                const first = attributes[0]!;
                onChange({
                  ...draft,
                  conditions: [...draft.conditions, newAttributeCondition(first.key, first.type)],
                });
              }}
            >
              <PlusIcon aria-hidden />
              <Trans>Add condition</Trans>
            </Button>
            {full ? (
              <p className="text-sm text-muted-foreground">
                <Trans>A segment has at most {maxConditionsText} conditions.</Trans>
              </p>
            ) : null}
          </div>
        )}
      </section>
    </>
  );
}
