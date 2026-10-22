import { Trans, useLingui } from "@lingui/react/macro";
import { XIcon } from "lucide-react";
import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Select, SelectItem } from "@/components/ui/select";
import { newPrerequisite, type PrerequisiteDraft } from "@/lib/configuration-draft";

export type PrerequisitesSectionProps = {
  prerequisites: PrerequisiteDraft[];
  onChange: (prerequisites: PrerequisiteDraft[]) => void;
  /** The project's other flags that are not archived, with their variations' keys. */
  flags: ReadonlyMap<string, readonly string[]>;
  /** What is wrong with each prerequisite, by its key. */
  errors: ReadonlyMap<string, string>;
  disabled?: boolean;
};

/**
 * The flags this flag requires in the environment: unless each serves
 * the variation named, the flag serves its off variation, whatever its
 * rules say.
 */
export function PrerequisitesSection({
  prerequisites,
  onChange,
  flags,
  errors,
  disabled = false,
}: PrerequisitesSectionProps) {
  const { t } = useLingui();
  const heading = useId();
  const update = (key: string, change: Partial<PrerequisiteDraft>) =>
    onChange(prerequisites.map((row) => (row.key === key ? { ...row, ...change } : row)));

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id={heading} className="text-base font-semibold">
          <Trans>Prerequisites</Trans>
        </h2>
        <p className="text-sm text-muted-foreground">
          <Trans>
            Flags that must serve a variation for this flag to be evaluated here; otherwise it
            serves its off variation.
          </Trans>
        </p>
      </div>
      {prerequisites.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>No prerequisites.</Trans>
        </p>
      ) : (
        <ol className="flex flex-col gap-2">
          {prerequisites.map((row, index) => {
            const position = index + 1;
            const error = errors.get(row.key);
            const errorId = `${heading}-${row.key}-error`;
            const variations = flags.get(row.flag) ?? [];
            return (
              <li key={row.key}>
                <div
                  role="group"
                  aria-label={t`Prerequisite ${position}`}
                  className="flex flex-col gap-2 rounded-md border p-3"
                >
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                    <Select
                      aria-label={t`Flag (prerequisite ${position})`}
                      value={row.flag}
                      placeholder={t`Choose a flag`}
                      disabled={disabled}
                      aria-invalid={error ? true : undefined}
                      aria-describedby={error ? errorId : undefined}
                      onValueChange={(flag) => update(row.key, { flag, variation: "" })}
                      className="sm:w-56"
                    >
                      {[...flags.keys()].map((key) => (
                        <SelectItem key={key} value={key}>
                          {key}
                        </SelectItem>
                      ))}
                      {row.flag && !flags.has(row.flag) ? (
                        <SelectItem value={row.flag}>{row.flag}</SelectItem>
                      ) : null}
                    </Select>
                    <Select
                      aria-label={t`Required variation (prerequisite ${position})`}
                      value={row.variation}
                      placeholder={t`Choose a variation`}
                      disabled={disabled || !row.flag}
                      onValueChange={(variation) => update(row.key, { variation })}
                      className="sm:w-48"
                    >
                      {variations.map((key) => (
                        <SelectItem key={key} value={key}>
                          {key}
                        </SelectItem>
                      ))}
                      {row.variation && !variations.includes(row.variation) ? (
                        <SelectItem value={row.variation}>{row.variation}</SelectItem>
                      ) : null}
                    </Select>
                    {disabled ? null : (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={t`Remove prerequisite ${position}`}
                        onClick={() =>
                          onChange(prerequisites.filter((other) => other.key !== row.key))
                        }
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
          })}
        </ol>
      )}
      {disabled ? null : (
        <div>
          <Button
            type="button"
            variant="outline"
            onClick={() => onChange([...prerequisites, newPrerequisite()])}
          >
            <Trans>Add prerequisite</Trans>
          </Button>
        </div>
      )}
    </section>
  );
}
