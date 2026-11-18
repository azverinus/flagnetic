import { type ReactNode, useId } from "react";

import { SearchField } from "@/components/search-field";
import { Select, SelectItem } from "@/components/ui/select";
import { cn } from "@/lib/cn";

/** A choice of a facet: the value the address holds and the words for it. */
export type FacetOption = { value: string; label: string };

/** Up to this many options a facet is a radio list; beyond it, a select. */
export const facetRadioLimit = 5;

/** The select's value for a facet left unset: Radix keeps the empty string for itself. */
const unset = "\u0000unset";

function FacetFrame({
  label,
  hint,
  className,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  className?: string;
  children: (labelId: string) => ReactNode;
}) {
  const labelId = useId();
  return (
    <div className={cn("flex flex-col gap-1.5 px-2 pt-3 pb-1", className)}>
      <div id={labelId} className="text-xs/4 font-medium text-muted-foreground">
        {label}
      </div>
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      {children(labelId)}
    </div>
  );
}

export type PanelFacetProps = {
  label: ReactNode;
  hint?: ReactNode;
  /** The option the address holds, or undefined while the facet is not set. */
  value: string | undefined;
  /** Applies the choice at once; undefined clears the facet. */
  onChange: (value: string | undefined) => void;
  options: readonly FacetOption[];
  /** The words for the facet left unset, such as "Any type"; absent where a choice is always made. */
  anyLabel?: string;
  disabled?: boolean;
};

/**
 * One facet of a list in the context panel: a single choice among the
 * options, a short radio list up to five of them and a select beyond, with
 * the facet left unset as its first choice. Either applies at once and
 * writes whatever the screen writes for the choice, so a facet is the same
 * search parameter as the control it replaces.
 */
export function PanelFacet({
  label,
  hint,
  value,
  onChange,
  options,
  anyLabel,
  disabled,
}: PanelFacetProps) {
  const name = useId();
  return (
    <FacetFrame label={label} hint={hint}>
      {(labelId) =>
        options.length <= facetRadioLimit ? (
          <div role="radiogroup" aria-labelledby={labelId} className="flex flex-col gap-0.5">
            {[
              ...(anyLabel === undefined ? [] : [{ value: undefined, label: anyLabel }]),
              ...options,
            ].map((option) => (
              <label
                key={option.value ?? unset}
                className="flex h-8 items-center gap-2 rounded-md px-2 text-sm hover:bg-foreground/[0.06] has-disabled:opacity-50"
              >
                <input
                  type="radio"
                  name={name}
                  checked={value === option.value}
                  disabled={disabled}
                  onChange={() => onChange(option.value)}
                  className="size-4 shrink-0 accent-primary"
                />
                <span className="truncate">{option.label}</span>
              </label>
            ))}
          </div>
        ) : (
          <Select
            aria-labelledby={labelId}
            value={value ?? unset}
            disabled={disabled}
            onValueChange={(next) => onChange(next === unset ? undefined : next)}
          >
            {anyLabel === undefined ? null : <SelectItem value={unset}>{anyLabel}</SelectItem>}
            {options.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </Select>
        )
      }
    </FacetFrame>
  );
}

export type PanelChecklistProps = {
  label: ReactNode;
  hint?: ReactNode;
  /** The options the address holds. */
  values: readonly string[];
  /** Applies the options now held, at once. */
  onChange: (values: string[]) => void;
  options: readonly (FacetOption & { description?: string })[];
};

/** A facet that takes several options at once, each a checkbox; none checked is the facet unset. */
export function PanelChecklist({ label, hint, values, onChange, options }: PanelChecklistProps) {
  return (
    <FacetFrame label={label} hint={hint}>
      {(labelId) => (
        <div role="group" aria-labelledby={labelId} className="flex flex-col gap-0.5">
          {options.map((option) => (
            <label
              key={option.value}
              title={option.description}
              className="flex min-h-8 items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-foreground/[0.06]"
            >
              <input
                type="checkbox"
                checked={values.includes(option.value)}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? [...values, option.value]
                      : values.filter((value) => value !== option.value),
                  )
                }
                className="size-4 shrink-0 accent-primary"
              />
              <span>{option.label}</span>
            </label>
          ))}
        </div>
      )}
    </FacetFrame>
  );
}

export type PanelTextFacetProps = {
  label: ReactNode;
  placeholder?: string;
  value: string;
  onChange: (value: string) => void;
};

/** A facet typed in, applied once typing pauses. */
export function PanelTextFacet({ label, placeholder, value, onChange }: PanelTextFacetProps) {
  return (
    <div className="px-2 pt-3 pb-1">
      <SearchField
        label={<span className="text-xs/4 font-medium text-muted-foreground">{label}</span>}
        placeholder={placeholder}
        className="w-full gap-1.5"
        value={value}
        onChange={onChange}
      />
    </div>
  );
}
