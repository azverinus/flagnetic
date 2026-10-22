import {
  type Attribute,
  type AttributeListItem,
  createAttribute,
  deleteAttribute,
  getAttribute,
  updateAttribute,
  zAttributeType,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { PencilIcon, PlusIcon, TagsIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import { Can } from "@/components/can";
import { NamedConflictBanner } from "@/components/conflict-banner";
import { DiscoveredAttributes } from "@/components/discovered-attributes";
import { QueryError } from "@/components/query-error";
import { DeleteRowDialog, RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Table } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { formatNumber } from "@/i18n/format";
import { useCan } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { typeName } from "@/lib/attribute-types";
import { ApiError, unwrap } from "@/lib/errors";
import { changedFields, descriptionSchema, keySchema, nameSchema } from "@/lib/forms";
import { ifMatch } from "@/lib/http";
import { useListMutation } from "@/lib/mutations";
import { attributesQuery } from "@/lib/queries";
import { useFormConflict } from "@/lib/use-form-conflict";

export const Route = createFileRoute("/_signed-in/projects/$project/attributes")({
  staticData: { crumb: msg`Attributes` },
  component: Attributes,
});

const permission: Permission = "declaration.manage";

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
    const columns: ColumnDef<AttributeListItem, unknown>[] = [
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
              <Badge variant="muted">
                <Trans>Standard</Trans>
              </Badge>
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
      {
        id: "usage",
        header: t`Used by flags and segments`,
        cell: ({ row }) => formatNumber(i18n, row.original.usage_count),
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

  const declared = (
    <>
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
    </>
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
      <Tabs
        label={t`Attribute lists`}
        tabs={[
          {
            value: "declared",
            label: <Trans>Declared</Trans>,
            content: declared,
          },
          {
            value: "discovered",
            label: <Trans>Discovered</Trans>,
            content: <DiscoveredAttributes project={project} />,
          },
        ]}
      />

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
        remove={(key) =>
          unwrap(
            deleteAttribute({
              path: { project, attribute: key },
              headers: deleting && ifMatch(deleting.version),
            }),
          )
        }
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
  const queryClient = useQueryClient();
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
  const conflicts = useFormConflict({
    project,
    kind: "attribute",
    initial: attribute,
    read: () => unwrap(getAttribute({ path: { project, attribute: attribute?.key ?? "" } })),
  });
  const { base, conflict } = conflicts;
  const list = attributesQuery(project).queryKey;
  const save = useListMutation(
    ({ values: { key, type, name, description }, at }: { values: Values; at?: number }) =>
      unwrap(
        base && at !== undefined
          ? updateAttribute({
              path: { project, attribute: base.key },
              body: changedFields(
                { name: base.name, description: base.description },
                { name, description },
              ),
              headers: ifMatch(at),
            })
          : createAttribute({
              path: { project },
              // The schema has refused any other value already.
              body: { key: key ?? "", type: zAttributeType.parse(type), name, description },
            }),
      ),
    list,
    onDone,
  );
  const send = (at: number | undefined) =>
    form.handleSubmit((values) => {
      conflicts.clear();
      save.mutate({ values, at }, { onError: (error) => conflicts.meet(error) });
    });
  const taken = save.error instanceof ApiError && save.error.code === "already_exists";
  const { errors } = form.formState;
  const fixed = <Trans>Fixed once declared.</Trans>;

  return (
    <DialogForm
      onSubmit={send(conflicts.version)}
      onCancel={onDone}
      submitLabel={editing ? <Trans>Save</Trans> : <Trans>Create attribute</Trans>}
      pending={save.isPending}
      refusal={taken ? undefined : conflicts.refusal(save.error)}
    >
      {base && conflict ? (
        <NamedConflictBanner
          base={base}
          conflict={conflict}
          pending={save.isPending}
          onReload={() => {
            const { current } = conflict;
            form.reset({
              key: current.key,
              type: current.type,
              name: current.name,
              description: current.description,
            });
            conflicts.restart(current);
            save.reset();
            void queryClient.invalidateQueries({ queryKey: list });
          }}
          onKeep={() => void send(conflicts.keep(conflict.current))()}
        />
      ) : null}
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
