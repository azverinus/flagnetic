import {
  createUnitType,
  deleteUnitType,
  getUnitType,
  type UnitType,
  type UnitTypeListItem,
  updateUnitType,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { BoxesIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Can } from "@/components/can";
import { NamedConflictBanner } from "@/components/conflict-banner";
import { QueryError } from "@/components/query-error";
import {
  DeleteRowDialog,
  listedIfMatch,
  RowAction,
  RowDialog,
  withRowActions,
} from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { formatNumber } from "@/i18n/format";
import { useCan } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { ApiError, unwrap } from "@/lib/errors";
import { changedFields, descriptionSchema, keySchema, nameSchema } from "@/lib/forms";
import { ifMatch } from "@/lib/http";
import { useListMutation } from "@/lib/mutations";
import { unitTypesQuery } from "@/lib/queries";
import { useFormConflict } from "@/lib/use-form-conflict";

export const Route = createFileRoute("/_signed-in/projects/$project/unit-types")({
  staticData: { crumb: msg`Unit types` },
  component: UnitTypes,
});

const permission: Permission = "declaration.manage";

/** The kinds of unit a flag can be served to, such as a user or a device. */
function UnitTypes() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const unitTypes = useQuery(unitTypesQuery(project));
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<UnitType>();
  const [deleting, setDeleting] = useState<UnitType>();

  const canManage = useCan(permission);

  const columns = useMemo(() => {
    const columns: ColumnDef<UnitTypeListItem, unknown>[] = [
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.key}</code>,
      },
      { id: "name", header: t`Name`, cell: ({ row }) => row.original.name },
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
          <RowAction label={t`Delete ${key}`} icon={Trash2Icon} onClick={() => setDeleting(row)} />
        </>
      );
    });
  }, [canManage, i18n, t]);
  const newUnitType = (
    <Can permission={permission} whenMissing="disable">
      <Button onClick={() => setCreating(true)}>
        <PlusIcon aria-hidden />
        <Trans>New unit type</Trans>
      </Button>
    </Can>
  );

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Unit types`}
        description={
          <Trans>
            The kinds of unit a flag is served to, such as a user or a device. A key is shared with
            the attributes and never changes.
          </Trans>
        }
        actions={newUnitType}
      />
      {unitTypes.isError ? (
        <QueryError error={unitTypes.error} onRetry={() => void unitTypes.refetch()} />
      ) : unitTypes.data?.length === 0 ? (
        <EmptyState
          icon={<BoxesIcon />}
          title={<Trans>This project has no unit type yet.</Trans>}
          description={<Trans>Declare the kinds of unit its flags are served to.</Trans>}
          action={newUnitType}
        />
      ) : (
        <Table
          caption={t`Unit types`}
          hideCaption
          columns={columns}
          data={unitTypes.data ?? []}
          loading={unitTypes.isPending}
          getRowId={(unitType) => unitType.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New unit type`}>
        <UnitTypeForm project={project} onDone={() => setCreating(false)} />
      </Dialog>
      <RowDialog
        row={editing}
        onClose={() => setEditing(undefined)}
        title={({ key }) => t`Edit ${key}`}
      >
        {(unitType) => (
          <UnitTypeForm
            project={project}
            unitType={unitType}
            onDone={() => setEditing(undefined)}
          />
        )}
      </RowDialog>
      <DeleteRowDialog
        rowKey={deleting?.key}
        onDone={() => setDeleting(undefined)}
        title={(key) => t`Delete ${key}`}
        confirmLabel={<Trans>Delete unit type</Trans>}
        remove={(key) =>
          unwrap(
            deleteUnitType({
              path: { project, unit_type: key },
              headers: listedIfMatch(unitTypes.data, key, deleting),
            }),
          )
        }
        list={unitTypesQuery(project).queryKey}
      >
        {(key) => (
          <p>
            <Trans>
              The unit type {key} is no longer declared, and its key can be declared again.
            </Trans>
          </p>
        )}
      </DeleteRowDialog>
    </div>
  );
}

/** Declares a unit type, or edits one when given it; its key stays as declared. */
function UnitTypeForm({
  project,
  unitType,
  onDone,
}: {
  project: string;
  unitType?: UnitType;
  onDone: () => void;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const schema = z.object({
    // A disabled field submits no value: the key of a declared unit type is shown, not sent.
    key: unitType ? z.string().optional() : keySchema(i18n),
    name: nameSchema(i18n),
    description: descriptionSchema(i18n),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      key: unitType?.key ?? "",
      name: unitType?.name ?? "",
      description: unitType?.description ?? "",
    },
  });
  // The unit type the form was filled from and the version a save names,
  // apart: keeping one's edits saves onto a newer version what was
  // changed from this one.
  const [base, setBase] = useState(unitType);
  const [version, setVersion] = useState(unitType?.version);
  const conflicts = useFormConflict({
    project,
    kind: "unit_type",
    read: () => unwrap(getUnitType({ path: { project, unit_type: unitType?.key ?? "" } })),
  });
  const list = unitTypesQuery(project).queryKey;
  const save = useListMutation(
    ({ values: { key, name, description }, at }: { values: Values; at?: number }) =>
      unwrap(
        base && at !== undefined
          ? updateUnitType({
              path: { project, unit_type: base.key },
              body: changedFields(
                { name: base.name, description: base.description },
                { name, description },
              ),
              headers: ifMatch(at),
            })
          : createUnitType({ path: { project }, body: { key: key ?? "", name, description } }),
      ),
    list,
    onDone,
  );
  const send = (at: number | undefined) =>
    form.handleSubmit((values) => {
      conflicts.clear();
      save.mutate({ values, at }, { onError: (error) => conflicts.meet(error) });
    });
  const conflict = conflicts.conflict;
  const taken = save.error instanceof ApiError && save.error.code === "already_exists";
  const { errors } = form.formState;

  return (
    <DialogForm
      onSubmit={send(version)}
      onCancel={onDone}
      submitLabel={unitType ? <Trans>Save</Trans> : <Trans>Create unit type</Trans>}
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
            form.reset({ key: current.key, name: current.name, description: current.description });
            setBase(current);
            setVersion(current.version);
            conflicts.clear();
            save.reset();
            void queryClient.invalidateQueries({ queryKey: list });
          }}
          onKeep={() => {
            setVersion(conflict.current.version);
            void send(conflict.current.version)();
          }}
        />
      ) : null}
      <Field
        label={<Trans>Key</Trans>}
        hint={
          unitType ? (
            <Trans>Fixed once declared.</Trans>
          ) : (
            <Trans>Lowercase letters, digits, - and _. It never changes.</Trans>
          )
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
          autoFocus={!unitType}
          disabled={unitType !== undefined}
          {...form.register("key")}
        />
      </Field>
      <Field
        label={<Trans>Name</Trans>}
        error={errors.name?.message}
        refusal={save.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus={unitType !== undefined} {...form.register("name")} />
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
