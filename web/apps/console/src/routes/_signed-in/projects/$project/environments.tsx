import {
  createEnvironment,
  deleteEnvironment,
  type Environment,
  updateEnvironment,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Can } from "@/components/can";
import { QueryError } from "@/components/query-error";
import { DeleteRowDialog, RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table } from "@/components/ui/table";
import { useCan } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { ApiError, unwrap } from "@/lib/errors";
import { keySchema, nameSchema } from "@/lib/forms";
import { ifMatch } from "@/lib/http";
import { useListMutation } from "@/lib/mutations";
import { environmentsQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/environments")({
  staticData: { crumb: msg`Environments` },
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

  const columns = useMemo<ColumnDef<Environment, unknown>[]>(() => {
    const columns: ColumnDef<Environment, unknown>[] = [
      {
        id: "key",
        header: t`Key`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.key}</code>,
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
  }, [canManage, t]);

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
  const schema = z.object({ name: nameSchema(i18n) });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: environment.name },
  });
  const rename = useListMutation(
    (body: Values) =>
      unwrap(
        updateEnvironment({
          path: { project, environment: environment.key },
          body,
          headers: ifMatch(environment.version),
        }),
      ),
    environmentsQuery(project).queryKey,
    onDone,
  );

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => rename.mutate(values))}
      onCancel={onDone}
      submitLabel={<Trans>Save</Trans>}
      pending={rename.isPending}
      refusal={rename.error}
    >
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
