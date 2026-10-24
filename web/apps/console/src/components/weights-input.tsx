import { Trans, useLingui } from "@lingui/react/macro";
import { PlusIcon, Trash2Icon } from "lucide-react";
import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { ApiError } from "@/lib/errors";
import {
  evenWeights,
  maxVariations,
  percentText,
  type WeightDraft,
  type WeightIssue,
  weightTotal,
} from "@/lib/experiments";
import { violationMessage } from "@/lib/problem-messages";

export type WeightsInputProps = {
  rows: readonly WeightDraft[];
  onChange: (rows: WeightDraft[]) => void;
  /** The variations of the experiment's flag, in the flag's order. */
  variations: readonly { key: string; name: string }[];
  /** What the form found wrong, shown once it checked the weights. */
  issues?: { rows: readonly WeightIssue[]; total: string | undefined };
  /** The refusal of the last submission; a violation of a row is shown under it. */
  refusal?: unknown;
  /** Whether the experiment's status no longer lets the weights change. */
  disabled?: boolean;
};

/**
 * The variations an experiment compares, each with its share of the
 * allocated traffic as a percentage, in the order that fixes their bucket
 * ranges. The total is shown as the member types, since the weights must
 * sum to exactly 100.00 % before the experiment can start.
 */
export function WeightsInput({
  rows,
  onChange,
  variations,
  issues,
  refusal,
  disabled = false,
}: WeightsInputProps) {
  const { i18n, t } = useLingui();
  const heading = useId();
  const totalId = useId();
  const total = weightTotal(rows);
  const totalText = total === undefined ? undefined : percentText(i18n, total);
  const unused = variations.filter(
    (variation) => !rows.some((row) => row.variation === variation.key),
  );
  const refused = (index: number, field: "variation" | "weight") => {
    if (!(refusal instanceof ApiError)) return undefined;
    const violation = refusal.violations.find(
      (candidate) => candidate.pointer === `/variations/${index}/${field}`,
    );
    return violation && violationMessage(i18n, violation);
  };
  const issueAt = (index: number, field: "variation" | "weight") =>
    issues?.rows.find((issue) => issue.index === index && issue.field === field)?.message ??
    refused(index, field);
  const wholeRefusal =
    refusal instanceof ApiError
      ? refusal.violations.find((violation) => violation.pointer === "/variations")
      : undefined;
  const totalIssue =
    issues?.total ?? (wholeRefusal ? violationMessage(i18n, wholeRefusal) : undefined);

  const update = (index: number, patch: Partial<WeightDraft>) =>
    onChange(rows.map((row, at) => (at === index ? { ...row, ...patch } : row)));

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-3">
      <h2 id={heading} className="text-sm font-medium">
        <Trans>Variations and weights</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Each variation's share of the units the traffic allocation includes; the shares sum to
          100.00 %.
        </Trans>
      </p>
      <ol className="flex flex-col gap-3">
        {rows.map((row, index) => {
          const n = index + 1;
          // A variation the flag no longer has stays named, so the row
          // shows what it holds and the issue under it makes sense.
          const known = variations.some((variation) => variation.key === row.variation);
          return (
            <li
              key={index}
              className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)_auto] items-start gap-3"
            >
              <Field label={<Trans>Variation {n}</Trans>} error={issueAt(index, "variation")}>
                <Select
                  value={row.variation}
                  disabled={disabled}
                  placeholder={t`Choose a variation`}
                  onValueChange={(variation) => update(index, { variation })}
                >
                  {!known && row.variation !== "" ? (
                    <SelectItem value={row.variation}>{row.variation}</SelectItem>
                  ) : null}
                  {variations.map((variation) => (
                    <SelectItem key={variation.key} value={variation.key}>
                      {variation.name === variation.key
                        ? variation.key
                        : `${variation.name} (${variation.key})`}
                    </SelectItem>
                  ))}
                </Select>
              </Field>
              <Field
                label={<Trans>Weight of variation {n} (%)</Trans>}
                error={issueAt(index, "weight")}
              >
                <Input
                  autoComplete="off"
                  inputMode="decimal"
                  className="font-mono"
                  disabled={disabled}
                  value={row.weight}
                  onChange={(event) => update(index, { weight: event.target.value })}
                />
              </Field>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="mt-7"
                aria-label={t`Remove variation ${n}`}
                disabled={disabled}
                onClick={() => onChange(rows.filter((_, at) => at !== index))}
              >
                <Trash2Icon aria-hidden />
              </Button>
            </li>
          );
        })}
      </ol>
      <p id={totalId} aria-live="polite" className="text-sm font-medium">
        {totalText === undefined ? <Trans>Total: —</Trans> : <Trans>Total: {totalText} %</Trans>}
      </p>
      {totalIssue ? (
        <p role="alert" className="text-sm text-destructive">
          {totalIssue}
        </p>
      ) : null}
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          disabled={disabled || unused.length === 0 || rows.length >= maxVariations}
          onClick={() =>
            onChange([...rows, { variation: unused[0]!.key, weight: percentText(i18n, 0) }])
          }
        >
          <PlusIcon aria-hidden />
          <Trans>Add variation</Trans>
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={disabled || rows.length === 0}
          onClick={() => {
            const weights = evenWeights(rows.length);
            onChange(
              rows.map((row, index) => ({ ...row, weight: percentText(i18n, weights[index]!) })),
            );
          }}
        >
          <Trans>Split evenly</Trans>
        </Button>
      </div>
    </section>
  );
}
