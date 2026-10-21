import {
  type Attribute,
  type AttributeType,
  createAttribute,
  deleteAttribute,
  updateAttribute,
  zAttributeType,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { PencilIcon, PlusIcon, TagsIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import { Can } from "@/components/can";
import { QueryError } from "@/components/query-error";
import { DeleteRowDialog, RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Table } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useCan } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { ApiError, unwrap } from "@/lib/errors";
import { descriptionSchema, keySchema, nameSchema } from "@/lib/forms";
import { useListMutation } from "@/lib/mutations";
import { attributesQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/attributes")({
  staticData: { crumb: msg`Attributes` },
  component: Attributes,
});

const permission: Permission = "declaration.manage";

const typeNames = {
  string: msg({ message: "String", comment: "An attribute type." }),
  number: msg({ message: "Number", comment: "An attribute type." }),
  boolean: msg({ message: "Boolean", comment: "An attribute type." }),
  semver: msg({ message: "Semantic version", comment: "An attribute type." }),
  date: msg({ message: "Date", comment: "An attribute type." }),
  list: msg({ message: "List", comment: "An attribute type." }),
} satisfies Record<AttributeType, MessageDescriptor>;

/** The type's display name, or its name on the wire for one this build does not know. */
function typeName(i18n: I18n, type: string): string {
  const known = zAttributeType.safeParse(type);
  return known.success ? i18n._(typeNames[known.data]) : type;
}

/** What targeting can read about a unit, each with a type fixed once declared. */
function Attributes() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const attributes = useQuery(attributesQuery(project));
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Attribute>();
  const [deleting, setDeleting] = useState<Attribute>();

  const canManage = useCan(permission);

  const columns = useMemo(() => {
    const columns: ColumnDef<Attribute, unknown>[] = [
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.key}</code>,
      },
      {
        id: "name",
        header: t`Name`,
        cell: ({ row }) => (
          <span className="flex flex-wrap items-center gap-2">
            {row.original.name}
            {row.original.standard ? (
              <span className="rounded-sm border px-1.5 py-0.5 text-xs text-muted-foreground">
                <Trans>Standard</Trans>
              </span>
            ) : null}
          </span>
        ),
      },
      { id: "type", header: t`Type`, cell: ({ row }) => typeName(i18n, row.original.type) },
      {
        id: "description",
        header: t`Description`,
        cell: ({ row }) => (
          <span className="text-muted-foreground">{row.original.description}</span>
        ),
      },
    ];
    return withRowActions(columns, canManage, (row) => {
      const { key } = row;
      return (
        <>
          <RowAction label={t`Edit ${key}`} icon={PencilIcon} onClick={() => setEditing(row)} />
          {row.standard ? null : (
            <RowAction
              label={t`Delete ${key}`}
              icon={Trash2Icon}
              onClick={() => setDeleting(row)}
            />
          )}
        </>
      );
    });
  }, [canManage, i18n, t]);
  const newAttribute = (
    <Can permission={permission} whenMissing="disable">
      <Button onClick={() => setCreating(true)}>
        <PlusIcon aria-hidden />
        <Trans>New attribute</Trans>
      </Button>
    </Can>
  );

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Attributes`}
        description={
          <Trans>
            What targeting reads about a unit. The standard attributes are in every project; they
            can be renamed but not deleted. A type never changes once declared.
          </Trans>
        }
        actions={newAttribute}
      />
      {attributes.isError ? (
        <QueryError error={attributes.error} onRetry={() => void attributes.refetch()} />
      ) : attributes.data?.length === 0 ? (
        <EmptyState
          icon={<TagsIcon />}
          title={<Trans>This project has no attribute yet.</Trans>}
          action={newAttribute}
        />
      ) : (
        <Table
          caption={t`Attributes`}
          hideCaption
          columns={columns}
          data={attributes.data ?? []}
          loading={attributes.isPending}
          getRowId={(attribute) => attribute.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New attribute`}>
        <AttributeForm project={project} onDone={() => setCreating(false)} />
      </Dialog>
      <RowDialog
        row={editing}
        onClose={() => setEditing(undefined)}
        title={({ key }) => t`Edit ${key}`}
      >
        {(attribute) => (
          <AttributeForm
            project={project}
            attribute={attribute}
            onDone={() => setEditing(undefined)}
          />
        )}
      </RowDialog>
      <DeleteRowDialog
        rowKey={deleting?.key}
        onDone={() => setDeleting(undefined)}
        title={(key) => t`Delete ${key}`}
        confirmLabel={<Trans>Delete attribute</Trans>}
        remove={(key) => unwrap(deleteAttribute({ path: { project, attribute: key } }))}
        list={attributesQuery(project).queryKey}
      >
        {(key) => (
          <p>
            <Trans>
              The attribute {key} is no longer declared, and its key can be declared again.
            </Trans>
          </p>
        )}
      </DeleteRowDialog>
    </div>
  );
}

/** Declares an attribute, or edits one when given it; its key and type stay as declared. */
function AttributeForm({
  project,
  attribute,
  onDone,
}: {
  project: string;
  attribute?: Attribute;
  onDone: () => void;
}) {
  const { i18n, t } = useLingui();
  const editing = attribute !== undefined;
  const schema = z.object({
    // A disabled field submits no value: what is fixed is shown, not sent.
    key: editing ? z.string().optional() : keySchema(i18n),
    type: editing
      ? z.string().optional()
      : z.string().refine((value) => zAttributeType.safeParse(value).success, t`Choose a type.`),
    name: nameSchema(i18n),
    description: descriptionSchema(i18n),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      key: attribute?.key ?? "",
      type: attribute?.type ?? "",
      name: attribute?.name ?? "",
      description: attribute?.description ?? "",
    },
  });
  const save = useListMutation(
    ({ key, type, name, description }: Values) =>
      unwrap(
        attribute
          ? updateAttribute({
              path: { project, attribute: attribute.key },
              body: { name, description },
            })
          : createAttribute({
              path: { project },
              // The schema has refused any other value already.
              body: { key: key ?? "", type: zAttributeType.parse(type), name, description },
            }),
      ),
    attributesQuery(project).queryKey,
    onDone,
  );
  const taken = save.error instanceof ApiError && save.error.code === "already_exists";
  const { errors } = form.formState;
  const fixed = <Trans>Fixed once declared.</Trans>;

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => save.mutate(values))}
      onCancel={onDone}
      submitLabel={editing ? <Trans>Save</Trans> : <Trans>Create attribute</Trans>}
      pending={save.isPending}
      refusal={taken ? undefined : save.error}
    >
      <Field
        label={<Trans>Key</Trans>}
        hint={
          editing ? fixed : <Trans>Lowercase letters, digits, - and _. It never changes.</Trans>
        }
        error={
          errors.key?.message ??
          (taken
            ? t`This project already has a unit type or an attribute with that key.`
            : undefined)
        }
        refusal={save.error}
        pointer="/key"
      >
        <Input
          autoComplete="off"
          autoFocus={!editing}
          disabled={editing}
          {...form.register("key")}
        />
      </Field>
      <Controller
        control={form.control}
        name="type"
        render={({ field }) => (
          <Field
            label={<Trans>Type</Trans>}
            hint={editing ? fixed : undefined}
            error={errors.type?.message}
            refusal={save.error}
            pointer="/type"
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value || undefined}
              onValueChange={field.onChange}
              disabled={editing}
              placeholder={t`Choose a type`}
            >
              {zAttributeType.options.map((type) => (
                <SelectItem key={type} value={type}>
                  {typeName(i18n, type)}
                </SelectItem>
              ))}
            </Select>
          </Field>
        )}
      />
      <Field
        label={<Trans>Name</Trans>}
        error={errors.name?.message}
        refusal={save.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus={editing} {...form.register("name")} />
      </Field>
      <Field
        label={<Trans>Description</Trans>}
        error={errors.description?.message}
        refusal={save.error}
        pointer="/description"
      >
        <Textarea rows={3} {...form.register("description")} />
      </Field>
    </DialogForm>
  );
}
