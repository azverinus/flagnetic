import {
  createEnvironment,
  deleteEnvironment,
  type Environment,
  getEnvironment,
  updateEnvironment,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Can } from "@/components/can";
import { NamedConflictBanner } from "@/components/conflict-banner";
import { QueryError } from "@/components/query-error";
import { DeleteRowDialog, RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { useCan } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { ApiError, unwrap } from "@/lib/errors";
import { changedFields, keySchema, nameSchema } from "@/lib/forms";
import { ifMatch } from "@/lib/http";
import { useListMutation } from "@/lib/mutations";
import { environmentsQuery } from "@/lib/queries";
import { useNewerVersionNotice } from "@/lib/change-stream";
import { useFormConflict } from "@/lib/use-form-conflict";

export const Route = createFileRoute("/_signed-in/projects/$project/environments/")({
  component: Environments,
});

const permission: Permission = "environment.manage";

/** Where a project serves from: created, renamed and deleted by those who manage them. */
function Environments() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const environments = useQuery(environmentsQuery(project));
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<Environment>();
  const [deleting, setDeleting] = useState<Environment>();

  const canManage = useCan(permission);

  const columns = useMemo<TableColumn<Environment>[]>(() => {
    const columns: TableColumn<Environment>[] = [
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/environments/$environment"
            params={{ project, environment: row.original.key }}
            className="font-mono text-xs underline-offset-4 hover:underline"
          >
            {row.original.key}
          </Link>
        ),
      },
      { id: "name", header: t`Name`, cell: ({ row }) => row.original.name },
    ];
    return withRowActions(columns, canManage, (row) => {
      const { key } = row;
      return (
        <>
          <RowAction label={t`Rename ${key}`} icon={PencilIcon} onClick={() => setRenaming(row)} />
          <RowAction label={t`Delete ${key}`} icon={Trash2Icon} onClick={() => setDeleting(row)} />
        </>
      );
    });
  }, [canManage, project, t]);

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Environments`}
        description={
          <Trans>
            Where the project serves from. A key is fixed once created; the name can change.
          </Trans>
        }
        actions={
          <Can permission={permission} whenMissing="disable">
            <Button onClick={() => setCreating(true)}>
              <PlusIcon aria-hidden />
              <Trans>New environment</Trans>
            </Button>
          </Can>
        }
      />
      {environments.isError ? (
        <QueryError error={environments.error} onRetry={() => void environments.refetch()} />
      ) : (
        <Table
          caption={t`Environments`}
          hideCaption
          columns={columns}
          data={environments.data ?? []}
          loading={environments.isPending}
          getRowId={(environment) => environment.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New environment`}>
        <CreateEnvironment project={project} onDone={() => setCreating(false)} />
      </Dialog>
      <RowDialog
        row={renaming}
        onClose={() => setRenaming(undefined)}
        title={({ key }) => t`Rename ${key}`}
      >
        {(environment) => (
          <RenameEnvironment
            project={project}
            environment={environment}
            onDone={() => setRenaming(undefined)}
          />
        )}
      </RowDialog>
      <DeleteRowDialog
        rowKey={deleting?.key}
        onDone={() => setDeleting(undefined)}
        title={(key) => t`Delete ${key}`}
        confirmLabel={<Trans>Delete environment</Trans>}
        // An environment will carry flag values and SDK keys: deleting
        // the wrong one is costly, so its key is typed out first.
        typeToConfirm
        remove={(key) =>
          unwrap(
            deleteEnvironment({
              path: { project, environment: key },
              headers: deleting && ifMatch(deleting.version),
            }),
          )
        }
        list={environmentsQuery(project).queryKey}
      >
        {(key) => (
          <p>
            <Trans>The environment {key} is deleted. This cannot be undone.</Trans>
          </p>
        )}
      </DeleteRowDialog>
    </div>
  );
}

function CreateEnvironment({ project, onDone }: { project: string; onDone: () => void }) {
  const { i18n, t } = useLingui();
  const schema = z.object({ key: keySchema(i18n), name: nameSchema(i18n) });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { key: "", name: "" },
  });
  const create = useListMutation(
    (body: Values) => unwrap(createEnvironment({ path: { project }, body })),
    environmentsQuery(project).queryKey,
    onDone,
  );
  const taken = create.error instanceof ApiError && create.error.code === "already_exists";
  const { errors } = form.formState;

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => create.mutate(values))}
      onCancel={onDone}
      submitLabel={<Trans>Create environment</Trans>}
      pending={create.isPending}
      refusal={taken ? undefined : create.error}
    >
      <Field
        label={<Trans>Key</Trans>}
        hint={<Trans>Lowercase letters, digits, - and _. It never changes.</Trans>}
        error={
          errors.key?.message ??
          (taken ? t`This project already has an environment with that key.` : undefined)
        }
        refusal={create.error}
        pointer="/key"
      >
        <Input autoComplete="off" autoFocus {...form.register("key")} />
      </Field>
      <Field
        label={<Trans>Name</Trans>}
        error={errors.name?.message}
        refusal={create.error}
        pointer="/name"
      >
        <Input autoComplete="off" {...form.register("name")} />
      </Field>
    </DialogForm>
  );
}

function RenameEnvironment({
  project,
  environment,
  onDone,
}: {
  project: string;
  environment: Environment;
  onDone: () => void;
}) {
  const { i18n } = useLingui();
  const queryClient = useQueryClient();
  const schema = z.object({ name: nameSchema(i18n) });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: environment.name },
  });
  const conflicts = useFormConflict({
    project,
    kind: "environment",
    initial: environment,
    read: () => unwrap(getEnvironment({ path: { project, environment: environment.key } })),
  });
  const { base, conflict } = conflicts;
  useNewerVersionNotice(
    { kind: "environment", key: environment.key },
    { dirty: form.formState.isDirty, version: conflicts.version },
    conflicts.meetNewer,
  );
  const list = environmentsQuery(project).queryKey;
  const rename = useListMutation(
    ({ values, at }: { values: Values; at: number }) =>
      unwrap(
        updateEnvironment({
          path: { project, environment: environment.key },
          body: changedFields({ name: base.name }, values),
          headers: ifMatch(at),
        }),
      ),
    list,
    onDone,
  );
  const send = (at: number) =>
    form.handleSubmit((values) => {
      conflicts.clear();
      rename.mutate({ values, at }, { onError: (error) => conflicts.meet(error) });
    });

  return (
    <DialogForm
      onSubmit={send(conflicts.version)}
      onCancel={onDone}
      submitLabel={<Trans>Save</Trans>}
      pending={rename.isPending}
      refusal={conflicts.refusal(rename.error)}
    >
      {conflict ? (
        <NamedConflictBanner
          base={base}
          conflict={conflict}
          pending={rename.isPending}
          onReload={() => {
            form.reset({ name: conflict.current.name });
            conflicts.restart(conflict.current);
            rename.reset();
            void queryClient.invalidateQueries({ queryKey: list });
          }}
          onKeep={() => void send(conflicts.keep(conflict.current))()}
        />
      ) : null}
      <Field
        label={<Trans>Name</Trans>}
        error={form.formState.errors.name?.message}
        refusal={rename.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus {...form.register("name")} />
      </Field>
    </DialogForm>
  );
}
