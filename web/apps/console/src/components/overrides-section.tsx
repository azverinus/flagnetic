import {
  deleteOverride,
  type Flag,
  type FlagConfiguration,
  type Override,
  putOverride,
  type UnitType,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { XIcon } from "lucide-react";
import { type FormEvent, useId, useMemo, useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { FormRefusal } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { Table } from "@/components/ui/table";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import { ApiError, unwrap } from "@/lib/errors";
import {
  configurationViolationMessage,
  overrideIssues,
  withConfiguration,
} from "@/lib/flag-detail";
import { flagQuery, flagsQueryKey } from "@/lib/queries";

export type OverridesSectionProps = {
  project: string;
  flag: Flag;
  configuration: FlagConfiguration;
  unitTypes: readonly UnitType[];
  /** A member's name by identifier, for who last wrote each override. */
  nameOf: (member: string) => string;
  /** Why overrides cannot be written now, such as the flag being archived; none when they can. */
  frozen?: string;
};

/**
 * The units pinned to a variation in one environment, checked before any
 * rule. Each one is written at once, on its own, rather than with the
 * rest of the configuration, so pinning a tester's account is one click
 * whatever else is being edited.
 */
export function OverridesSection({
  project,
  flag,
  configuration,
  unitTypes,
  nameOf,
  frozen,
}: OverridesSectionProps) {
  const { t } = useLingui();
  const heading = useId();
  const queryClient = useQueryClient();
  const can = useCan("flag_configuration.update");
  const reason = useMissingPermissionReason("flag_configuration.update");

  const answered = (next: FlagConfiguration) => {
    queryClient.setQueryData(flagQuery(project, flag.key).queryKey, (current: Flag | undefined) =>
      current ? withConfiguration(current, next) : current,
    );
    void queryClient.invalidateQueries({ queryKey: [...flagsQueryKey(project), "list"] });
  };

  const remove = useMutation({
    mutationFn: (override: Override) =>
      unwrap(
        deleteOverride({
          path: {
            project,
            flag: flag.key,
            environment: configuration.environment,
            unit_type: override.unit_type,
            unit_id: override.unit_id,
          },
        }),
      ),
    meta: { toast: false },
    onSuccess: (next, override) => {
      answered(next);
      const unit = `${override.unit_type} ${override.unit_id}`;
      toast.success(t`The override of ${unit} is removed.`);
    },
    onError: (error) => {
      // Someone removed it first: the list is read again to show so.
      if (error instanceof ApiError && error.code === "not_found") {
        void queryClient.invalidateQueries({ queryKey: flagQuery(project, flag.key).queryKey });
      }
    },
  });

  const columns = useMemo<ColumnDef<Override, unknown>[]>(
    () => [
      { id: "unit_type", header: t`Unit type`, accessorKey: "unit_type" },
      {
        id: "unit_id",
        header: t`Unit ID`,
        cell: ({ row }) => <span className="font-mono">{row.original.unit_id}</span>,
      },
      {
        id: "variation",
        header: t`Variation`,
        cell: ({ row }) => <span className="font-mono">{row.original.variation}</span>,
      },
      {
        id: "note",
        header: t`Note`,
        cell: ({ row }) => row.original.note ?? <span className="text-muted-foreground">—</span>,
      },
      {
        id: "added_by",
        header: t`Added by`,
        cell: ({ row }) =>
          row.original.added_by ? (
            nameOf(row.original.added_by)
          ) : (
            <span className="text-muted-foreground">
              <Trans>A project token</Trans>
            </span>
          ),
      },
      {
        id: "actions",
        header: () => <span className="sr-only">{t`Actions`}</span>,
        cell: ({ row }) => {
          const unit = `${row.original.unit_type} ${row.original.unit_id}`;
          return frozen ? null : (
            <Can permission="flag_configuration.update" whenMissing="hide">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={t`Remove the override of ${unit}`}
                disabled={remove.isPending}
                onClick={() => remove.mutate(row.original)}
              >
                <XIcon aria-hidden />
              </Button>
            </Can>
          );
        },
      },
    ],
    [t, nameOf, remove, frozen],
  );

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id={heading} className="text-base font-semibold">
          <Trans>Overrides</Trans>
        </h2>
        <p className="text-sm text-muted-foreground">
          <Trans>
            A variation pinned for one unit, checked before any rule. An override is saved as soon
            as it is added or removed.
          </Trans>
        </p>
      </div>
      <Table
        columns={columns}
        data={configuration.overrides}
        caption={t`Overrides in this environment`}
        hideCaption
        getRowId={(row) => JSON.stringify([row.unit_type, row.unit_id])}
        empty={t`No overrides in this environment.`}
      />
      <FormRefusal
        refusal={
          remove.error instanceof ApiError && remove.error.code === "not_found"
            ? undefined
            : remove.error
        }
      />
      {frozen ? null : can ? (
        <AddOverride
          project={project}
          flag={flag}
          configuration={configuration}
          unitTypes={unitTypes}
          onAnswered={answered}
        />
      ) : (
        <p className="text-sm text-muted-foreground">{reason}</p>
      )}
    </section>
  );
}

function AddOverride({
  project,
  flag,
  configuration,
  unitTypes,
  onAnswered,
}: Pick<OverridesSectionProps, "project" | "flag" | "configuration" | "unitTypes"> & {
  onAnswered: (next: FlagConfiguration) => void;
}) {
  const { i18n, t } = useLingui();
  // Until the member chooses, the first unit type and variation, as they
  // are once read: the unit types may arrive after the form is shown.
  const [chosenType, setUnitType] = useState("");
  const unitType = chosenType || (unitTypes[0]?.key ?? "");
  const [unitId, setUnitId] = useState("");
  const [chosenVariation, setVariation] = useState("");
  const variation = chosenVariation || (flag.variations[0]?.key ?? "");
  const [note, setNote] = useState("");
  const [checked, setChecked] = useState(false);
  const issues = overrideIssues(i18n, unitId, note);

  const save = useMutation({
    mutationFn: () =>
      unwrap(
        putOverride({
          path: {
            project,
            flag: flag.key,
            environment: configuration.environment,
            unit_type: unitType,
            unit_id: unitId,
          },
          body: { variation, note: note.trim() === "" ? null : note.trim() },
        }),
      ),
    meta: { toast: false },
    onSuccess: (next) => {
      onAnswered(next);
      const unit = `${unitType} ${unitId}`;
      toast.success(t`The override of ${unit} is saved.`);
      setUnitId("");
      setNote("");
      setChecked(false);
    },
  });
  const refusal = save.error;
  const at = (pointer: string) => {
    if (!(refusal instanceof ApiError)) return undefined;
    const violation = refusal.violations.find((candidate) => candidate.pointer === pointer);
    return violation && configurationViolationMessage(i18n, violation);
  };
  const shownAtFields =
    refusal instanceof ApiError &&
    refusal.violations.some((violation) => ["/variation", "/note"].includes(violation.pointer));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (issues.unitId || issues.note) return;
    save.mutate();
  };

  return (
    <form
      noValidate
      aria-label={t`Add an override`}
      onSubmit={submit}
      className="flex flex-col gap-3 rounded-md border p-4"
    >
      <div className="grid gap-3 sm:grid-cols-[10rem_1fr_10rem]">
        <Field label={<Trans>Unit type</Trans>}>
          <Select value={unitType} onValueChange={setUnitType}>
            {unitTypes.map((type) => (
              <SelectItem key={type.key} value={type.key}>
                {type.key}
              </SelectItem>
            ))}
          </Select>
        </Field>
        <Field label={<Trans>Unit ID</Trans>} error={checked ? issues.unitId : undefined}>
          <Input
            autoComplete="off"
            className="font-mono"
            value={unitId}
            onChange={(event) => setUnitId(event.target.value)}
          />
        </Field>
        <Field label={<Trans>Variation</Trans>} error={at("/variation")}>
          <Select value={variation} onValueChange={setVariation}>
            {flag.variations.map((option) => (
              <SelectItem key={option.key} value={option.key}>
                {option.key}
              </SelectItem>
            ))}
          </Select>
        </Field>
      </div>
      <Field
        label={<Trans>Note (optional)</Trans>}
        hint={
          <Trans>Why the unit is pinned, such as the test it is for. Never sent to SDKs.</Trans>
        }
        error={(checked ? issues.note : undefined) ?? at("/note")}
      >
        <Input autoComplete="off" value={note} onChange={(event) => setNote(event.target.value)} />
      </Field>
      <FormRefusal refusal={shownAtFields ? undefined : refusal} />
      <div className="flex justify-end">
        <Button type="submit" disabled={save.isPending}>
          <Trans>Add override</Trans>
        </Button>
      </div>
    </form>
  );
}
