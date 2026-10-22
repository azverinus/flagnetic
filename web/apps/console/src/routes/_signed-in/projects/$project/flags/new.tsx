import { createFlag, type FlagCreate, zFlagCreate, zFlagType, zLifetime } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { PermissionGate } from "@/components/can";
import { TagsInput } from "@/components/tags-input";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { VariationsEditor } from "@/components/variations-editor";
import { ApiError, unwrap } from "@/lib/errors";
import { flagRefusalAt, flagTypeName, flagTypes, flagViolationMessage } from "@/lib/flags";
import { descriptionSchema, nameSchema } from "@/lib/forms";
import { memberName } from "@/lib/members";
import { flagsQueryKey, meQuery, projectMembersQuery } from "@/lib/queries";
import {
  startingVariations,
  type VariationDraft,
  variationInputs,
  variationIssues,
} from "@/lib/variations";

export const Route = createFileRoute("/_signed-in/projects/$project/flags/new")({
  staticData: { crumb: msg`New flag` },
  component: NewFlag,
});

function NewFlag() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  return (
    <PermissionGate permission="flag.edit" title={t`New flag`}>
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <PageHeader
          title={t`New flag`}
          description={
            <Trans>
              The flag is created off in every environment, serving its off variation until it is
              switched on.
            </Trans>
          }
        />
        <CreateFlag key={project} project={project} />
      </div>
    </PermissionGate>
  );
}

/** The owner's value for the member creating the flag, whom the server names when none is sent. */
const me = "me";

/** The fields whose refusals the form shows under them, by their pointers. */
const fieldPointer =
  /^\/(key|type|name|description|owner|tags(\/\d+)?|lifetime|expected_removal_at|variations\/\d+\/(key|value|name|description))$/;

/** The first instant of a day (YYYY-MM-DD) in the member's time zone. */
function startOfDay(day: string): string {
  const [year, month, date] = day.split("-").map(Number) as [number, number, number];
  return new Date(year, month - 1, date).toISOString();
}

function CreateFlag({ project }: { project: string }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { data: caller } = useSuspenseQuery(meQuery);
  const members = useQuery(projectMembersQuery(project));

  const schema = z
    .object({
      key: z
        .string()
        .trim()
        .refine((value) => zFlagCreate.shape.key.safeParse(value).success, {
          message: flagViolationMessage(i18n, { pointer: "/key", code: "invalid_key", detail: "" }),
        }),
      type: zFlagType,
      name: nameSchema(i18n),
      description: descriptionSchema(i18n),
      owner: z.string(),
      tags: z
        .array(z.string())
        .max(50, i18n._(msg`A flag has at most 50 tags.`))
        .refine((tags) => tags.every((tag) => tag.length <= 64), {
          message: i18n._(msg`A tag has at most 64 characters.`),
        }),
      lifetime: zLifetime,
      expected_removal_at: z.string(),
      variations: z.array(z.custom<VariationDraft>()),
    })
    .superRefine((values, context) => {
      for (const issue of variationIssues(i18n, values.type, values.variations)) {
        context.addIssue({
          code: "custom",
          path: ["variations", ...issue.path],
          message: issue.message,
        });
      }
    });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      key: "",
      type: "boolean",
      name: "",
      description: "",
      owner: me,
      tags: [],
      lifetime: "temporary",
      expected_removal_at: "",
      // Kept while the type is boolean, so choosing another type shows
      // two rows to fill in, and switching back and forth loses nothing.
      variations: startingVariations(),
    },
  });

  const create = useMutation({
    mutationFn: (values: Values) => {
      const variations = variationInputs(values.type, values.variations);
      const body: FlagCreate = {
        key: values.key,
        type: values.type,
        name: values.name,
        lifetime: values.lifetime,
        ...(values.description ? { description: values.description } : {}),
        ...(values.owner !== me ? { owner: values.owner } : {}),
        ...(values.tags.length > 0 ? { tags: values.tags } : {}),
        ...(values.lifetime === "temporary" && values.expected_removal_at
          ? { expected_removal_at: startOfDay(values.expected_removal_at) }
          : {}),
        ...(variations ? { variations } : {}),
      };
      return unwrap(createFlag({ path: { project }, body }));
    },
    meta: { toast: false },
    onSuccess: async (flag) => {
      await queryClient.invalidateQueries({ queryKey: flagsQueryKey(project) });
      const key = flag.key;
      toast.success(t`The flag ${key} is created.`);
      await navigate({ to: "/projects/$project/flags", params: { project } });
    },
  });

  const owners = useMemo(() => {
    const others = (members.data ?? [])
      .filter((member) => member.status === "active" && member.member !== caller.member?.id)
      .map((member) => ({ value: member.member, label: memberName(member) }));
    return others.sort((a, b) => a.label.localeCompare(b.label, i18n.locale));
  }, [members.data, caller.member?.id, i18n.locale]);

  const refusal = create.error;
  const taken = refusal instanceof ApiError && refusal.code === "already_exists";
  const at = (pointer: string) => flagRefusalAt(i18n, refusal, pointer);
  const tagsRefusal =
    refusal instanceof ApiError
      ? refusal.violations.find((violation) => violation.pointer.startsWith("/tags"))
      : undefined;
  // A refusal is explained under the fields it names, and above the
  // actions only when no field shows it.
  const shownAtFields =
    taken ||
    (refusal instanceof ApiError &&
      refusal.violations.some((violation) => fieldPointer.test(violation.pointer)));

  const { errors } = form.formState;
  const type = useWatch({ control: form.control, name: "type" });
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
  const myName = caller.member ? memberName(caller.member) : "";

  return (
    <FormPage
      onSubmit={form.handleSubmit((values) => create.mutate(values))}
      onCancel={() => void navigate({ to: "/projects/$project/flags", params: { project } })}
      submitLabel={<Trans>Create flag</Trans>}
      pending={create.isPending}
      refusal={shownAtFields ? undefined : refusal}
    >
      <Field
        label={<Trans>Key</Trans>}
        hint={
          <Trans>
            How code names the flag, such as checkout.newFlow: letters, digits, ., - and _. It never
            changes.
          </Trans>
        }
        error={
          errors.key?.message ??
          (taken ? t`This project already has a flag with that key.` : at("/key"))
        }
      >
        <Input autoComplete="off" autoFocus className="font-mono" {...form.register("key")} />
      </Field>
      <Controller
        control={form.control}
        name="type"
        render={({ field }) => (
          <Field
            label={<Trans>Type</Trans>}
            hint={<Trans>The type of every value the flag serves. It never changes.</Trans>}
            error={errors.type?.message ?? at("/type")}
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
            >
              {flagTypes.map((option) => (
                <SelectItem key={option} value={option}>
                  {flagTypeName(i18n, option)}
                </SelectItem>
              ))}
            </Select>
          </Field>
        )}
      />
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
            error={errors.owner?.message ?? at("/owner")}
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
            >
              <SelectItem value={me}>{t`Me (${myName})`}</SelectItem>
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
            <TagsInput value={field.value} onChange={field.onChange} />
          </Field>
        )}
      />
      <Controller
        control={form.control}
        name="lifetime"
        render={({ field }) => (
          <Field
            label={<Trans>Lifetime</Trans>}
            hint={
              <Trans>
                A temporary flag is meant to be removed once its change is released; a permanent one
                stays.
              </Trans>
            }
            error={at("/lifetime")}
          >
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
        name="variations"
        render={({ field }) => (
          <VariationsEditor
            type={type}
            rows={field.value}
            onChange={field.onChange}
            errors={variationErrors}
            refusal={refusal}
          />
        )}
      />
    </FormPage>
  );
}
