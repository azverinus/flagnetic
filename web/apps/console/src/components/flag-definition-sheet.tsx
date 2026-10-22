import {
  type AuditEntry,
  type Flag,
  type FlagDefinition,
  type FlagPatch,
  updateFlag,
  zLifetime,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useMemo, useState } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { ConflictBanner } from "@/components/conflict-banner";
import { DefinitionDiff } from "@/components/structural-diff-view";
import { TagsInput } from "@/components/tags-input";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { FormActions, FormRefusal } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectItem } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { VariationsEditor } from "@/components/variations-editor";
import { ApiError, unwrap } from "@/lib/errors";
import { entryOfVersion } from "@/lib/flag-detail";
import {
  dayOf,
  flagRefusalAt,
  flagTypeName,
  flagViolationMessage,
  startOfDay,
  tagsSchema,
} from "@/lib/flags";
import { descriptionSchema, nameSchema } from "@/lib/forms";
import { ifMatch } from "@/lib/http";
import { memberName } from "@/lib/members";
import { flagEntriesQuery, flagQuery, flagsQueryKey, projectMembersQuery } from "@/lib/queries";
import { fingerprint, diffDefinitions } from "@/lib/structural-diff";
import {
  type VariationDraft,
  variationDrafts,
  variationInputs,
  variationIssues,
} from "@/lib/variations";

export type FlagDefinitionSheetProps = {
  project: string;
  flag: Flag;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** A member's name by identifier, for the owner and the conflict's diff. */
  nameOf: (member: string) => string;
};

/**
 * A flag's definition, edited once for every environment: its name,
 * description, owner, tags, lifetime, automatic exposure and variations.
 * Its key and type never change, so they are shown, not edited.
 */
export function FlagDefinitionSheet({
  project,
  flag,
  open,
  onOpenChange,
  nameOf,
}: FlagDefinitionSheetProps) {
  const { t } = useLingui();
  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={t`Edit definition`}
      description={<Trans>Applies to the flag in every environment.</Trans>}
      className="max-w-2xl"
    >
      {/* Mounted while open only, so every opening starts from the flag as it is now. */}
      {open ? (
        <DefinitionForm
          project={project}
          flag={flag}
          onDone={() => onOpenChange(false)}
          nameOf={nameOf}
        />
      ) : null}
    </Sheet>
  );
}

/** The fields whose refusals the form shows under them, by their pointers. */
const fieldPointer =
  /^\/(name|description|owner|tags(\/\d+)?|lifetime|expected_removal_at|variations\/\d+\/(key|value|name|description))$/;

/** A conflict met on saving: the definition as it is now, and the entry that made it so. */
type Conflict = { current: FlagDefinition; entry: AuditEntry | undefined };

function DefinitionForm({
  project,
  flag,
  onDone,
  nameOf,
}: {
  project: string;
  flag: Flag;
  onDone: () => void;
  nameOf: (member: string) => string;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const members = useQuery(projectMembersQuery(project));
  const exposureId = useId();

  const schema = z
    .object({
      name: nameSchema(i18n),
      description: descriptionSchema(i18n),
      owner: z.string(),
      tags: tagsSchema(i18n),
      lifetime: zLifetime,
      expected_removal_at: z.string(),
      automatic_exposure: z.boolean(),
      variations: z.array(z.custom<VariationDraft>()),
    })
    .superRefine((values, context) => {
      for (const issue of variationIssues(i18n, flag.type, values.variations)) {
        context.addIssue({
          code: "custom",
          path: ["variations", ...issue.path],
          message: issue.message,
        });
      }
    });
  type Values = z.infer<typeof schema>;
  const valuesOf = (definition: FlagDefinition): Values => ({
    name: definition.name,
    description: definition.description,
    owner: definition.owner,
    tags: definition.tags,
    lifetime: definition.lifetime,
    expected_removal_at: definition.expected_removal_at
      ? dayOf(definition.expected_removal_at)
      : "",
    automatic_exposure: definition.automatic_exposure,
    variations: variationDrafts(definition.variations),
  });

  // The definition the form's values were filled from, which a save
  // compares them with to send only what the member changed, and the
  // version the save names: apart, since keeping one's edits over a newer
  // version saves onto that version what was changed from the older one.
  const [base, setBase] = useState<FlagDefinition>(flag);
  const [version, setVersion] = useState(flag.version);
  const [conflict, setConflict] = useState<Conflict>();
  const [unread, setUnread] = useState(false);
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: valuesOf(flag) });

  const patchOf = (values: Values): FlagPatch => {
    const patch: FlagPatch = {};
    const start = valuesOf(base);
    if (values.name !== start.name) patch.name = values.name;
    if (values.description !== start.description) patch.description = values.description;
    if (values.owner !== start.owner) patch.owner = values.owner;
    if (fingerprint(values.tags) !== fingerprint(start.tags)) patch.tags = values.tags;
    if (values.lifetime !== start.lifetime) patch.lifetime = values.lifetime;
    if (
      values.lifetime === "temporary" &&
      values.expected_removal_at !== "" &&
      (values.expected_removal_at !== start.expected_removal_at ||
        values.lifetime !== start.lifetime)
    ) {
      patch.expected_removal_at = startOfDay(values.expected_removal_at);
    }
    if (values.automatic_exposure !== start.automatic_exposure) {
      patch.automatic_exposure = values.automatic_exposure;
    }
    // Variations compare by what each one is, a blank name being its key,
    // as the server names it.
    const normal = (rows: { key: string; value: unknown; name?: string; description?: string }[]) =>
      fingerprint(
        rows.map((row) => [row.key, row.value, row.name || row.key, row.description ?? ""]),
      );
    const variations = variationInputs(flag.type, values.variations);
    if (variations && normal(variations) !== normal(base.variations)) patch.variations = variations;
    return patch;
  };

  const save = useMutation({
    mutationFn: async ({ values, at }: { values: Values; at: number }) =>
      unwrap(
        updateFlag({
          path: { project, flag: flag.key },
          body: patchOf(values),
          headers: ifMatch(at),
        }),
      ),
    meta: { toast: false },
    onSuccess: (saved) => {
      queryClient.setQueryData(flagQuery(project, flag.key).queryKey, saved);
      void queryClient.invalidateQueries({ queryKey: [...flagsQueryKey(project), "list"] });
      toast.success(t`The definition is saved.`);
      onDone();
    },
    onError: async (error) => {
      if (!(error instanceof ApiError) || error.code !== "version_mismatch") return;
      let current: Flag;
      try {
        current = await queryClient.fetchQuery({ ...flagQuery(project, flag.key), staleTime: 0 });
      } catch {
        // The newer version could not be read: the refusal says to reload.
        setUnread(true);
        return;
      }
      const entries = await queryClient
        .fetchQuery({ ...flagEntriesQuery(project, flag.id, { limit: 20 }), staleTime: 0 })
        .catch(() => []);
      // The definition's own entries are those addressing no environment.
      const entry = entryOfVersion(
        entries.filter((candidate) => candidate.environment_id === null),
        current.version,
      );
      setConflict({ current, entry });
    },
  });

  const submit = form.handleSubmit((values) => {
    if (Object.keys(patchOf(values)).length === 0) {
      onDone();
      return;
    }
    setConflict(undefined);
    setUnread(false);
    save.mutate({ values, at: version });
  });

  const owners = useMemo(() => {
    const listed = (members.data ?? [])
      .filter((member) => member.status === "active")
      .map((member) => ({ value: member.member, label: memberName(member) }));
    if (!listed.some((member) => member.value === base.owner)) {
      listed.push({ value: base.owner, label: nameOf(base.owner) });
    }
    return listed.sort((a, b) => a.label.localeCompare(b.label, i18n.locale));
  }, [members.data, base.owner, nameOf, i18n.locale]);

  // A conflict is explained by its banner once the newer version is read.
  const refusal =
    save.error instanceof ApiError && save.error.code === "version_mismatch" && !unread
      ? undefined
      : save.error;
  const at = (pointer: string) => flagRefusalAt(i18n, refusal, pointer);
  const tagsRefusal =
    refusal instanceof ApiError
      ? refusal.violations.find((violation) => violation.pointer.startsWith("/tags"))
      : undefined;
  const shownAtFields =
    refusal instanceof ApiError &&
    refusal.violations.some((violation) => fieldPointer.test(violation.pointer));
  const { errors } = form.formState;
  const lifetime = useWatch({ control: form.control, name: "lifetime" });
  const rows = useWatch({ control: form.control, name: "variations" });
  const variationErrors = rows.map((_, index) => {
    const row = errors.variations?.[index];
    return row
      ? {
          key: row.key?.message,
          value: row.value?.message,
          name: row.name?.message,
          description: row.description?.message,
        }
      : undefined;
  });

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-5">
      {conflict ? (
        <ConflictBanner
          subject="form"
          entry={conflict.entry}
          diff={
            <DefinitionDiff changes={diffDefinitions(base, conflict.current)} nameOf={nameOf} />
          }
          actions={
            <>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setBase(conflict.current);
                  setVersion(conflict.current.version);
                  form.reset(valuesOf(conflict.current));
                  setConflict(undefined);
                  save.reset();
                }}
              >
                <Trans>Reload and lose my edits</Trans>
              </Button>
              <Button
                type="button"
                disabled={save.isPending}
                onClick={() => {
                  const at = conflict.current.version;
                  void form.handleSubmit((values) => {
                    setVersion(at);
                    setConflict(undefined);
                    save.mutate({ values, at });
                  })();
                }}
              >
                <Trans>Keep my edits</Trans>
              </Button>
            </>
          }
        />
      ) : null}
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt className="text-muted-foreground">
          <Trans>Key</Trans>
        </dt>
        <dd className="font-mono">{flag.key}</dd>
        <dt className="text-muted-foreground">
          <Trans>Type</Trans>
        </dt>
        <dd>{flagTypeName(i18n, flag.type)}</dd>
      </dl>
      <Field label={<Trans>Name</Trans>} error={errors.name?.message ?? at("/name")}>
        <Input autoComplete="off" {...form.register("name")} />
      </Field>
      <Field
        label={<Trans>Description</Trans>}
        error={errors.description?.message ?? at("/description")}
      >
        <Textarea rows={3} {...form.register("description")} />
      </Field>
      <Controller
        control={form.control}
        name="owner"
        render={({ field }) => (
          <Field
            label={<Trans>Owner</Trans>}
            hint={<Trans>The member accountable for the flag.</Trans>}
            error={at("/owner")}
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
            >
              {owners.map((owner) => (
                <SelectItem key={owner.value} value={owner.value}>
                  {owner.label}
                </SelectItem>
              ))}
            </Select>
          </Field>
        )}
      />
      <Controller
        control={form.control}
        name="tags"
        render={({ field }) => (
          <Field
            label={<Trans>Tags</Trans>}
            hint={<Trans>Press Enter after each tag.</Trans>}
            error={
              errors.tags?.message ??
              (tagsRefusal ? flagViolationMessage(i18n, tagsRefusal) : undefined)
            }
          >
            <TagsInput
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onChange={field.onChange}
            />
          </Field>
        )}
      />
      <Controller
        control={form.control}
        name="lifetime"
        render={({ field }) => (
          <Field label={<Trans>Lifetime</Trans>} error={at("/lifetime")}>
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
            >
              <SelectItem value="temporary">{t`Temporary`}</SelectItem>
              <SelectItem value="permanent">{t`Permanent`}</SelectItem>
            </Select>
          </Field>
        )}
      />
      {lifetime === "temporary" ? (
        <Field
          label={<Trans>Expected removal</Trans>}
          hint={<Trans>Left empty, 90 days from today.</Trans>}
          error={at("/expected_removal_at")}
          className="w-56"
        >
          <Input type="date" {...form.register("expected_removal_at")} />
        </Field>
      ) : null}
      <Controller
        control={form.control}
        name="automatic_exposure"
        render={({ field }) => (
          <div className="flex items-start gap-3">
            <Switch
              id={exposureId}
              ref={field.ref}
              checked={field.value}
              onCheckedChange={field.onChange}
              aria-describedby={`${exposureId}-hint`}
            />
            <div className="flex flex-col gap-1">
              <Label htmlFor={exposureId}>
                <Trans>Automatic exposure</Trans>
              </Label>
              <p id={`${exposureId}-hint`} className="text-sm text-muted-foreground">
                <Trans>
                  SDKs record an exposure after every evaluation whose result carries an experiment.
                </Trans>
              </p>
            </div>
          </div>
        )}
      />
      <Controller
        control={form.control}
        name="variations"
        render={({ field }) => (
          <VariationsEditor
            type={flag.type}
            rows={field.value}
            onChange={(next) => {
              // A refusal points at rows by their places, which adding or
              // removing a row moves; it is dropped rather than shown on a
              // row it was not about.
              if (next.length !== field.value.length) save.reset();
              field.onChange(next);
            }}
            errors={variationErrors}
            refusal={refusal}
          />
        )}
      />
      <FormRefusal refusal={shownAtFields ? undefined : refusal} />
      <FormActions
        submitLabel={<Trans>Save definition</Trans>}
        onCancel={onDone}
        pending={save.isPending}
      />
    </form>
  );
}
