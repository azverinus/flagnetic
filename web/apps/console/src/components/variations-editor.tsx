import type { FlagType } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { PlusIcon, Trash2Icon } from "lucide-react";
import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { flagRefusalAt } from "@/lib/flags";
import {
  formatJson,
  newVariation,
  type VariationDraft,
  type VariationField,
} from "@/lib/variations";

export type VariationsEditorProps = {
  type: FlagType;
  rows: readonly VariationDraft[];
  onChange: (rows: VariationDraft[]) => void;
  /** The form's own errors, by row and field. */
  errors?: ReadonlyArray<Partial<Record<VariationField, string>> | undefined>;
  /** The refusal of the last submission; a violation of a row is shown under its field. */
  refusal?: unknown;
};

/**
 * A flag's variations, in the order the flag lists them. A boolean flag's
 * are always `true` and `false`, so they are shown and nothing is edited;
 * any other flag has at least one row, each with a key, a value of the
 * flag's type, and an optional name and description.
 */
export function VariationsEditor({ type, rows, onChange, errors, refusal }: VariationsEditorProps) {
  const { t } = useLingui();
  const heading = useId();

  if (type === "boolean") {
    return (
      <section aria-labelledby={heading} className="flex flex-col gap-2">
        <h2 id={heading} className="text-sm font-medium">
          <Trans>Variations</Trans>
        </h2>
        <p className="text-sm text-muted-foreground">
          <Trans>A boolean flag always has exactly the variations true and false.</Trans>
        </p>
        <ul aria-label={t`Variations`} className="flex gap-2">
          {["true", "false"].map((value) => (
            <li key={value} className="rounded-md border bg-muted/40 px-3 py-1.5 font-mono text-sm">
              {value}
            </li>
          ))}
        </ul>
      </section>
    );
  }

  const update = (index: number, field: VariationField, value: string) =>
    onChange(rows.map((row, at) => (at === index ? { ...row, [field]: value } : row)));
  const remove = (index: number) => onChange(rows.filter((_, at) => at !== index));

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-3">
      <h2 id={heading} className="text-sm font-medium">
        <Trans>Variations</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          The values the flag can serve. A key names a variation in rules and results; a name
          defaults to the key.
        </Trans>
      </p>
      <ol className="flex flex-col gap-3">
        {rows.map((row, index) => (
          <li key={row.id}>
            <VariationRow
              row={row}
              index={index}
              type={type}
              errors={errors?.[index]}
              refusal={refusal}
              onEdit={(field, value) => update(index, field, value)}
              onRemove={rows.length > 1 ? () => remove(index) : undefined}
            />
          </li>
        ))}
      </ol>
      <div>
        <Button
          type="button"
          variant="outline"
          onClick={() => onChange([...rows, newVariation(rows)])}
        >
          <PlusIcon aria-hidden />
          <Trans>Add variation</Trans>
        </Button>
      </div>
    </section>
  );
}

function VariationRow({
  row,
  index,
  type,
  errors,
  refusal,
  onEdit,
  onRemove,
}: {
  row: VariationDraft;
  index: number;
  type: FlagType;
  errors: Partial<Record<VariationField, string>> | undefined;
  refusal: unknown;
  onEdit: (field: VariationField, value: string) => void;
  /** Removes the row; absent for the last row left, since a flag keeps one. */
  onRemove: (() => void) | undefined;
}) {
  const { i18n, t } = useLingui();
  const n = index + 1;
  const error = (field: VariationField) =>
    errors?.[field] ?? flagRefusalAt(i18n, refusal, `/variations/${index}/${field}`);

  return (
    <fieldset className="relative flex flex-col gap-4 rounded-md border p-4 pt-3">
      <legend className="float-left h-9 pr-12 text-sm leading-9 font-medium">
        <Trans>Variation {n}</Trans>
      </legend>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="absolute top-3 right-3"
        aria-label={t`Remove variation ${n}`}
        disabled={onRemove === undefined}
        onClick={onRemove}
      >
        <Trash2Icon aria-hidden />
      </Button>
      <div className="clear-left grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
        <Field label={<Trans>Key</Trans>} error={error("key")}>
          <Input
            autoComplete="off"
            className="font-mono"
            value={row.key}
            onChange={(event) => onEdit("key", event.target.value)}
          />
        </Field>
        {type === "json" ? (
          <div className="flex flex-col gap-2">
            <Field label={<Trans>Value</Trans>} error={error("value")}>
              <Textarea
                rows={3}
                spellCheck={false}
                className="font-mono"
                value={row.value}
                onChange={(event) => onEdit("value", event.target.value)}
              />
            </Field>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="self-start"
              aria-label={t`Format variation ${n}`}
              onClick={() => onEdit("value", formatJson(row.value))}
            >
              <Trans>Format</Trans>
            </Button>
          </div>
        ) : (
          <Field label={<Trans>Value</Trans>} error={error("value")}>
            <Input
              autoComplete="off"
              inputMode={type === "number" ? "decimal" : undefined}
              className={type === "number" ? "font-mono" : undefined}
              value={row.value}
              onChange={(event) => onEdit("value", event.target.value)}
            />
          </Field>
        )}
        <Field label={<Trans>Name</Trans>} error={error("name")}>
          <Input
            autoComplete="off"
            value={row.name}
            onChange={(event) => onEdit("name", event.target.value)}
          />
        </Field>
        <Field label={<Trans>Description</Trans>} error={error("description")}>
          <Input
            autoComplete="off"
            value={row.description}
            onChange={(event) => onEdit("description", event.target.value)}
          />
        </Field>
      </div>
    </fieldset>
  );
}
