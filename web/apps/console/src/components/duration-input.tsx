import { plural } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import type { Ref } from "react";

import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { type Duration, type DurationUnit, durationUnits } from "@/lib/auth-settings";

export type DurationInputProps = {
  value: Duration;
  onChange: (value: Duration) => void;
  onBlur?: () => void;
  ref?: Ref<HTMLInputElement>;
  /** What the unit's choice is called, naming the duration, such as "Idle timeout unit". */
  unitLabel: string;
  /** Wired by `Field` to the number, which its label names. */
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

/**
 * The count the unit's name agrees with while no whole number is typed:
 * one that every language writes in its plural for many, as English
 * writes "5 days" and Russian "5 дней".
 */
const countOfMany = 5;

/**
 * A duration as a whole number and its unit, so a day is typed as 1 day
 * rather than as 86,400 seconds. The field's label names the number; the
 * unit's choice is named on its own.
 */
export function DurationInput({
  value,
  onChange,
  onBlur,
  ref,
  unitLabel,
  id,
  "aria-describedby": describedBy,
  "aria-invalid": invalid,
}: DurationInputProps) {
  const { t } = useLingui();
  // The unit agrees with the number typed beside it, as a language
  // writes "1 day" and "2 days"; anything but a whole number reads as many.
  const count = /^\d+$/.test(value.amount.trim()) ? Number(value.amount.trim()) : countOfMany;
  const unitName: Record<DurationUnit, string> = {
    seconds: t`${plural(count, { one: "second", other: "seconds" })}`,
    minutes: t`${plural(count, { one: "minute", other: "minutes" })}`,
    hours: t`${plural(count, { one: "hour", other: "hours" })}`,
    days: t`${plural(count, { one: "day", other: "days" })}`,
  };
  return (
    <div className="flex gap-2">
      <Input
        ref={ref}
        id={id}
        inputMode="numeric"
        autoComplete="off"
        className="w-32"
        aria-describedby={describedBy}
        aria-invalid={invalid}
        value={value.amount}
        onBlur={onBlur}
        onChange={(event) => onChange({ ...value, amount: event.target.value })}
      />
      <Select
        aria-label={unitLabel}
        aria-describedby={describedBy}
        aria-invalid={invalid}
        className="w-36"
        value={value.unit}
        onValueChange={(unit) => onChange({ ...value, unit: unit as DurationUnit })}
      >
        {durationUnits.map((unit) => (
          <SelectItem key={unit} value={unit}>
            {unitName[unit]}
          </SelectItem>
        ))}
      </Select>
    </div>
  );
}
