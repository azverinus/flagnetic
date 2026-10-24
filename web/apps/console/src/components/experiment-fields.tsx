import type { Experiment, FlagListItem, UnitTypeListItem } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { LockIcon } from "lucide-react";

import { TagsInput } from "@/components/tags-input";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { WeightsInput } from "@/components/weights-input";
import {
  evenWeights,
  type ExperimentField,
  type ExperimentIssues,
  type ExperimentValues,
  lockedFields,
  lockReason,
  maxVariations,
  percentText,
} from "@/lib/experiments";

export type ExperimentFieldsProps = {
  values: ExperimentValues;
  onChange: (values: ExperimentValues) => void;
  /** What the form found wrong, once it checked the values. */
  issues: ExperimentIssues | undefined;
  /** The refusal of the last submission; each violation is shown under its field. */
  refusal: unknown;
  /** The experiment as saved, when the form edits one; its status locks fields. */
  base?: Experiment;
  /** The flags an experiment can compare: those of the project not archived. */
  flags: readonly FlagListItem[];
  unitTypes: readonly UnitTypeListItem[];
  /** The members who may own the experiment. */
  owners: readonly { value: string; label: string }[];
  /** Whether the caller may not edit experiments at all. */
  disabled?: boolean;
};

/**
 * Every field of an experiment, for creating one and editing one. Fields
 * the experiment's status no longer lets change are shown, disabled, with
 * the reason said once above them. Choosing another flag fills the weights
 * with its variations split evenly, since the old flag's variations mean
 * nothing for it.
 */
export function ExperimentFields({
  values,
  onChange,
  issues,
  refusal,
  base,
  flags,
  unitTypes,
  owners,
  disabled = false,
}: ExperimentFieldsProps) {
  const { i18n, t } = useLingui();
  const locked = lockedFields(base?.status ?? "draft");
  const reason = base ? lockReason(i18n, base.status) : undefined;
  const off = (field: ExperimentField) => disabled || locked.has(field);
  const set = (patch: Partial<ExperimentValues>) => onChange({ ...values, ...patch });
  const issue = (field: ExperimentField | "key") => issues?.fields.get(field);

  const chosen = flags.find((flag) => flag.key === values.flag);
  // The experiment's own flag stays offered when it is archived, so the
  // form shows what the experiment compares; the issue under it says why
  // it must change.
  const flagOptions =
    values.flag !== "" && !chosen ? [{ key: values.flag, name: values.flag }, ...flags] : flags;
  const variations = chosen
    ? chosen.variations.map((variation) => ({
        key: variation.key,
        name: variation.name ?? variation.key,
      }))
    : (base?.variations ?? []).map(({ variation }) => ({ key: variation, name: variation }));
  const unitTypeOptions =
    values.unit_type !== "" && !unitTypes.some((unitType) => unitType.key === values.unit_type)
      ? [{ key: values.unit_type, name: values.unit_type }, ...unitTypes]
      : unitTypes;

  const chooseFlag = (key: string) => {
    const flag = flags.find((candidate) => candidate.key === key);
    const keys = (flag?.variations ?? []).slice(0, maxVariations).map((variation) => variation.key);
    const weights = evenWeights(keys.length);
    set({
      flag: key,
      variations: keys.map((variation, index) => ({
        variation,
        weight: percentText(i18n, weights[index] ?? 0),
      })),
    });
  };

  return (
    <div className="flex w-full max-w-3xl flex-col gap-5">
      {reason ? (
        <p
          role="status"
          className="flex items-start gap-2 rounded-md border bg-muted/40 p-3 text-sm"
        >
          <LockIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          {reason}
        </p>
      ) : null}
      <Field
        label={<Trans>Key</Trans>}
        hint={
          <Trans>
            Names the experiment in rules and in the exposures SDKs report: letters, digits, ., -
            and _. It never changes.
          </Trans>
        }
        error={issue("key")}
        refusal={refusal}
        pointer="/key"
      >
        <Input
          autoComplete="off"
          className="font-mono"
          value={values.key}
          disabled={disabled || base !== undefined}
          onChange={(event) => set({ key: event.target.value })}
        />
      </Field>
      <Field label={<Trans>Name</Trans>} error={issue("name")} refusal={refusal} pointer="/name">
        <Input
          autoComplete="off"
          value={values.name}
          disabled={off("name")}
          onChange={(event) => set({ name: event.target.value })}
        />
      </Field>
      <Field
        label={<Trans>Hypothesis</Trans>}
        hint={<Trans>What the experiment expects to find, written before it runs.</Trans>}
        error={issue("hypothesis")}
        refusal={refusal}
        pointer="/hypothesis"
      >
        <Textarea
          rows={3}
          value={values.hypothesis}
          disabled={off("hypothesis")}
          onChange={(event) => set({ hypothesis: event.target.value })}
        />
      </Field>
      <Field
        label={<Trans>Flag</Trans>}
        hint={<Trans>The flag whose variations the experiment compares.</Trans>}
        error={issue("flag")}
        refusal={refusal}
        pointer="/flag"
      >
        <Select
          value={values.flag}
          disabled={off("flag")}
          placeholder={t`Choose a flag`}
          onValueChange={chooseFlag}
        >
          {flagOptions.map((flag) => (
            <SelectItem key={flag.key} value={flag.key}>
              {flag.name === flag.key ? flag.key : `${flag.name} (${flag.key})`}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field
        label={<Trans>Unit type</Trans>}
        hint={<Trans>Whose units are split: users, accounts, devices.</Trans>}
        error={issue("unit_type")}
        refusal={refusal}
        pointer="/unit_type"
      >
        <Select
          value={values.unit_type}
          disabled={off("unit_type")}
          placeholder={t`Choose a unit type`}
          onValueChange={(unit_type) => set({ unit_type })}
        >
          {unitTypeOptions.map((unitType) => (
            <SelectItem key={unitType.key} value={unitType.key}>
              {unitType.name === unitType.key ? unitType.key : `${unitType.name} (${unitType.key})`}
            </SelectItem>
          ))}
        </Select>
      </Field>
      {values.flag === "" ? (
        <p className="text-sm text-muted-foreground">
          <Trans>Choose a flag to set the weights of its variations.</Trans>
        </p>
      ) : (
        <WeightsInput
          rows={values.variations}
          onChange={(rows) => set({ variations: rows })}
          variations={variations}
          issues={issues?.weights}
          refusal={refusal}
          disabled={off("variations")}
        />
      )}
      <Field
        label={<Trans>Traffic allocation (%)</Trans>}
        hint={
          <Trans>
            The share of units included in the experiment; the others get what the flag's other
            rules serve.
          </Trans>
        }
        error={issue("traffic_allocation")}
        refusal={refusal}
        pointer="/traffic_allocation"
        className="w-56"
      >
        <Input
          autoComplete="off"
          inputMode="decimal"
          className="font-mono"
          value={values.traffic_allocation}
          disabled={off("traffic_allocation")}
          onChange={(event) => set({ traffic_allocation: event.target.value })}
        />
      </Field>
      <Field
        label={<Trans>Expected end</Trans>}
        hint={<Trans>The day the experiment is expected to have its answer.</Trans>}
        error={issue("expected_end_at")}
        refusal={refusal}
        pointer="/expected_end_at"
        className="w-56"
      >
        <Input
          type="date"
          value={values.expected_end_at}
          disabled={off("expected_end_at")}
          onChange={(event) => set({ expected_end_at: event.target.value })}
        />
      </Field>
      <Field
        label={<Trans>Owner</Trans>}
        hint={<Trans>The member accountable for the experiment.</Trans>}
        error={issue("owner")}
        refusal={refusal}
        pointer="/owner"
      >
        <Select
          value={values.owner}
          disabled={off("owner")}
          onValueChange={(owner) => set({ owner })}
        >
          {owners.map((owner) => (
            <SelectItem key={owner.value} value={owner.value}>
              {owner.label}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field
        label={<Trans>Tags</Trans>}
        hint={<Trans>Press Enter after each tag.</Trans>}
        error={issue("tags")}
        refusal={refusal}
        pointer="/tags"
      >
        <TagsInput value={values.tags} disabled={off("tags")} onChange={(tags) => set({ tags })} />
      </Field>
    </div>
  );
}
