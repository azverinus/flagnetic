import {
  createSdkKey,
  revokeSdkKey,
  rotateSdkKey,
  type SdkKey,
  type SdkKeyKind,
  updateSdkKey,
} from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { BanIcon, PencilIcon, PlusIcon, RotateCwIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { z } from "zod";

import { Can } from "@/components/can";
import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { TagsInput } from "@/components/tags-input";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RevealSecretDialog } from "@/components/ui/reveal-secret";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { useCan } from "@/lib/access";
import { ApiError, unwrap } from "@/lib/errors";
import { nameSchema } from "@/lib/forms";
import { useListMutation } from "@/lib/mutations";
import { violationMessage } from "@/lib/problem-messages";
import { sdkKeysQuery } from "@/lib/queries";
import {
  canChange,
  canRotate,
  defaultOverlap,
  editedOrigins,
  invalidOrigins,
  kindName,
  maxAllowedOrigins,
  type OverlapChoice,
  overlapChoices,
  overlapName,
  overlapSecondsOf,
  statusName,
} from "@/lib/sdk-keys";

export type SdkKeysProps = {
  project: string;
  environment: string;
};

/** The one key value on screen, until the member says it is stored. */
type Revealed = { title: "created" | "replacement"; value: string; kind: SdkKeyKind };

/**
 * An environment's SDK keys: each with its kind, key ID, status, use,
 * expiry and allowed origins, never its value. A maintainer creates keys,
 * rotates one with an overlap, renames it, edits its allowed origins and
 * revokes it; a value is shown once, at creation and in a rotation's
 * replacement, and lives only in that dialog.
 */
export function SdkKeys({ project, environment }: SdkKeysProps) {
  const { i18n, t } = useLingui();
  const list = sdkKeysQuery(project, environment);
  const keys = useQuery(list);
  const canManage = useCan("sdk_key.manage");
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<SdkKey>();
  const [rotating, setRotating] = useState<SdkKey>();
  const [revoking, setRevoking] = useState<SdkKey>();
  const [revealed, setRevealed] = useState<Revealed>();

  const columns = useMemo<TableColumn<SdkKey>[]>(() => {
    const columns: TableColumn<SdkKey>[] = [
      { id: "name", header: t`Name`, cell: ({ row }) => row.original.name },
      { id: "kind", header: t`Kind`, cell: ({ row }) => kindName(i18n, row.original.kind) },
      {
        id: "key_id",
        header: t`Key ID`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.key_id}</code>,
      },
      {
        id: "status",
        header: t`Status`,
        cell: ({ row }) => (
          <Badge variant={row.original.status === "active" ? "default" : "muted"}>
            {statusName(i18n, row.original.status)}
          </Badge>
        ),
      },
      {
        id: "created",
        header: t`Created`,
        cell: ({ row }) => <Moment value={row.original.created_at} />,
      },
      {
        id: "last_used",
        header: t`Last used`,
        cell: ({ row }) => <Moment value={row.original.last_used_at} />,
      },
      {
        id: "expires",
        header: t`Expires`,
        cell: ({ row }) => <Moment value={row.original.expires_at} />,
      },
      {
        id: "origins",
        header: t`Allowed origins`,
        cell: ({ row }) => <Origins sdkKey={row.original} />,
      },
    ];
    return withRowActions(columns, canManage, (key) => {
      const { name, key_id: keyId } = key;
      return (
        <>
          {canChange(key) ? (
            <RowAction
              label={t`Edit ${name} (${keyId})`}
              icon={PencilIcon}
              onClick={() => setEditing(key)}
            />
          ) : null}
          {canRotate(key) ? (
            <RowAction
              label={t`Rotate ${name} (${keyId})`}
              icon={RotateCwIcon}
              onClick={() => setRotating(key)}
            />
          ) : null}
          {canChange(key) ? (
            <RowAction
              label={t`Revoke ${name} (${keyId})`}
              icon={BanIcon}
              onClick={() => setRevoking(key)}
            />
          ) : null}
        </>
      );
    });
  }, [canManage, i18n, t]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-prose text-sm text-muted-foreground">
          <Trans>
            A server key lets a backend SDK download this environment’s ruleset. A client key lets a
            browser or app ask for the values served to one context, and can be limited to the
            origins it is used from. A key’s value is shown once, when it is created.
          </Trans>
        </p>
        <Can permission="sdk_key.manage" whenMissing="disable">
          <Button onClick={() => setCreating(true)}>
            <PlusIcon aria-hidden />
            <Trans>New SDK key</Trans>
          </Button>
        </Can>
      </div>
      {keys.isError ? (
        <QueryError error={keys.error} onRetry={() => void keys.refetch()} />
      ) : (
        <Table
          caption={t`SDK keys`}
          hideCaption
          columns={columns}
          data={keys.data ?? []}
          loading={keys.isPending}
          empty={<Trans>This environment has no SDK key yet.</Trans>}
          getRowId={(key) => key.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New SDK key`}>
        <CreateKey
          project={project}
          environment={environment}
          onDone={() => setCreating(false)}
          onCreated={(value, kind) => {
            setCreating(false);
            setRevealed({ title: "created", value, kind });
          }}
        />
      </Dialog>
      <RowDialog
        row={editing}
        onClose={() => setEditing(undefined)}
        title={({ name }) => t`Edit key ${name}`}
      >
        {(key) => (
          <EditKey
            project={project}
            environment={environment}
            sdkKey={key}
            onDone={() => setEditing(undefined)}
          />
        )}
      </RowDialog>
      <RowDialog
        row={rotating}
        onClose={() => setRotating(undefined)}
        title={({ name }) => t`Rotate key ${name}`}
      >
        {(key) => (
          <RotateKey
            project={project}
            environment={environment}
            sdkKey={key}
            onDone={() => setRotating(undefined)}
            onRotated={(value) => {
              setRotating(undefined);
              setRevealed({ title: "replacement", value, kind: key.kind });
            }}
          />
        )}
      </RowDialog>
      <RevokeKey
        project={project}
        environment={environment}
        sdkKey={revoking}
        onDone={() => setRevoking(undefined)}
      />
      <RevealSecretDialog
        open={revealed !== undefined}
        title={revealed?.title === "replacement" ? t`The replacement SDK key` : t`The new SDK key`}
        value={revealed?.value ?? ""}
        kept="hash"
        onClose={() => setRevealed(undefined)}
      >
        {revealed?.kind === "client" ? (
          <Trans>
            Put it in the client SDK’s configuration. It ships inside the browser or the app, so
            anyone can read it: it learns only the values served to a context, never the ruleset.
          </Trans>
        ) : (
          <Trans>
            Put it in the server SDK’s configuration, where only the service that uses it can read
            it. Never ship it in a browser or an app.
          </Trans>
        )}
      </RevealSecretDialog>
    </div>
  );
}

/** The origins a key is accepted from; a server key has none to show. */
function Origins({ sdkKey }: { sdkKey: SdkKey }) {
  if (sdkKey.kind === "server") return <span className="text-muted-foreground">—</span>;
  if (sdkKey.allowed_origins.length === 0) {
    return (
      <span className="text-muted-foreground">
        <Trans>Any origin</Trans>
      </span>
    );
  }
  return (
    <ul className="flex flex-col gap-0.5">
      {sdkKey.allowed_origins.map((origin) => (
        <li key={origin}>
          <code className="font-mono text-xs">{origin}</code>
        </li>
      ))}
    </ul>
  );
}

/**
 * The allowed origins of a client key, typed one at a time. What is typed
 * is shown as the server will keep it, in lower case without the default
 * port; what is no origin stays as typed, named under the field, so the
 * member removes it rather than lose it.
 */
function OriginsField({
  value,
  onChange,
  error,
  refusal,
}: {
  value: string[];
  onChange: (origins: string[]) => void;
  error: string | undefined;
  refusal: unknown;
}) {
  const refused =
    refusal instanceof ApiError
      ? refusal.violations.find((violation) => violation.pointer.startsWith("/allowed_origins"))
      : undefined;
  const { i18n } = useLingui();
  return (
    <Field
      label={<Trans>Allowed origins</Trans>}
      hint={
        <Trans>
          The sites the key may be used from, such as https://app.example.com. Leave it empty to
          allow any origin.
        </Trans>
      }
      error={error ?? (refused ? violationMessage(i18n, refused) : undefined)}
    >
      <TagsInput value={value} onChange={(origins) => onChange(editedOrigins(origins))} />
    </Field>
  );
}

/** The schema of a form that lists origins, naming what is no origin or too many. */
function originsSchema(i18n: I18n) {
  return z.array(z.string()).superRefine((origins, context) => {
    const [bad] = invalidOrigins(origins);
    if (bad !== undefined) {
      context.addIssue({
        code: "custom",
        message: i18n._(
          msg`“${bad}” is not an origin. Use a scheme, a host and an optional port, such as https://app.example.com.`,
        ),
      });
    } else if (origins.length > maxAllowedOrigins) {
      context.addIssue({
        code: "custom",
        message: i18n._(msg`At most ${maxAllowedOrigins} origins.`),
      });
    }
  });
}

function CreateKey({
  project,
  environment,
  onDone,
  onCreated,
}: {
  project: string;
  environment: string;
  onDone: () => void;
  onCreated: (value: string, kind: SdkKeyKind) => void;
}) {
  const { i18n } = useLingui();
  const schema = z.object({
    kind: z.enum(["server", "client"]),
    name: nameSchema(i18n),
    origins: originsSchema(i18n),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { kind: "server", name: "", origins: [] },
  });
  const kind = useWatch({ control: form.control, name: "kind" });
  const create = useListMutation(
    ({ kind, name, origins }: Values) =>
      unwrap(
        createSdkKey({
          path: { project, environment },
          body: { kind, name, allowed_origins: kind === "client" ? origins : [] },
        }),
      ),
    sdkKeysQuery(project, environment).queryKey,
    () => undefined,
    // The answer carries the key's value.
    { secret: true },
  );
  const { errors } = form.formState;

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) =>
        create.mutate(values, { onSuccess: ({ value }) => onCreated(value, values.kind) }),
      )}
      onCancel={onDone}
      submitLabel={<Trans>Create key</Trans>}
      pending={create.isPending}
      refusal={create.error}
    >
      <Controller
        control={form.control}
        name="kind"
        render={({ field }) => (
          <Field
            label={<Trans>Kind</Trans>}
            hint={
              <Trans>
                A server key is for a backend; a client key is for a browser or an app. It cannot be
                changed later.
              </Trans>
            }
            refusal={create.error}
            pointer="/kind"
          >
            <Select
              value={field.value}
              onValueChange={(next) => {
                field.onChange(next);
                // A server key takes no origins. The origins field leaves the
                // screen but keeps its value, which would otherwise still be
                // checked and block the form with an error nobody can see.
                if (next === "server") form.setValue("origins", []);
              }}
              onBlur={field.onBlur}
            >
              <SelectItem value="server">{kindName(i18n, "server")}</SelectItem>
              <SelectItem value="client">{kindName(i18n, "client")}</SelectItem>
            </Select>
          </Field>
        )}
      />
      <Field
        label={<Trans>Name</Trans>}
        hint={<Trans>What uses the key, to tell it apart later.</Trans>}
        error={errors.name?.message}
        refusal={create.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus {...form.register("name")} />
      </Field>
      {kind === "client" ? (
        <Controller
          control={form.control}
          name="origins"
          render={({ field }) => (
            <OriginsField
              value={field.value}
              onChange={field.onChange}
              error={errors.origins?.message}
              refusal={create.error}
            />
          )}
        />
      ) : null}
    </DialogForm>
  );
}

function EditKey({
  project,
  environment,
  sdkKey,
  onDone,
}: {
  project: string;
  environment: string;
  sdkKey: SdkKey;
  onDone: () => void;
}) {
  const { i18n } = useLingui();
  const schema = z.object({ name: nameSchema(i18n), origins: originsSchema(i18n) });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: sdkKey.name, origins: sdkKey.allowed_origins },
  });
  const edit = useListMutation(
    ({ name, origins }: Values) =>
      unwrap(
        updateSdkKey({
          path: { project, environment, sdk_key: sdkKey.id },
          // The origins are always sent: a request without them is refused,
          // so a restriction is never lifted by leaving them out.
          body: { name, allowed_origins: sdkKey.kind === "client" ? origins : [] },
        }),
      ),
    sdkKeysQuery(project, environment).queryKey,
    onDone,
  );
  const { errors } = form.formState;

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => edit.mutate(values))}
      onCancel={onDone}
      submitLabel={<Trans>Save</Trans>}
      pending={edit.isPending}
      refusal={edit.error}
    >
      <Field
        label={<Trans>Name</Trans>}
        error={errors.name?.message}
        refusal={edit.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus {...form.register("name")} />
      </Field>
      {sdkKey.kind === "client" ? (
        <Controller
          control={form.control}
          name="origins"
          render={({ field }) => (
            <OriginsField
              value={field.value}
              onChange={field.onChange}
              error={errors.origins?.message}
              refusal={edit.error}
            />
          )}
        />
      ) : null}
    </DialogForm>
  );
}

function RotateKey({
  project,
  environment,
  sdkKey,
  onDone,
  onRotated,
}: {
  project: string;
  environment: string;
  sdkKey: SdkKey;
  onDone: () => void;
  onRotated: (value: string) => void;
}) {
  const { i18n } = useLingui();
  const form = useForm<{ overlap: OverlapChoice }>({ defaultValues: { overlap: defaultOverlap } });
  const rotate = useListMutation(
    (overlap: OverlapChoice) =>
      unwrap(
        rotateSdkKey({
          path: { project, environment, sdk_key: sdkKey.id },
          body: { overlap_seconds: overlapSecondsOf(overlap) },
        }),
      ),
    sdkKeysQuery(project, environment).queryKey,
    () => undefined,
    // The answer carries the replacement's value.
    { secret: true },
  );

  return (
    <DialogForm
      onSubmit={form.handleSubmit(({ overlap }) =>
        rotate.mutate(overlap, { onSuccess: ({ value }) => onRotated(value) }),
      )}
      onCancel={onDone}
      submitLabel={<Trans>Rotate key</Trans>}
      pending={rotate.isPending}
      refusal={rotate.error}
    >
      <p className="text-sm text-muted-foreground">
        <Trans>
          A replacement key is created with the same kind, name and allowed origins. The old key
          keeps working for the overlap, so services can move to the new one without an outage; then
          it is refused.
        </Trans>
      </p>
      <Controller
        control={form.control}
        name="overlap"
        render={({ field }) => (
          <Field
            label={<Trans>Keep the old key working for</Trans>}
            refusal={rotate.error}
            pointer="/overlap_seconds"
          >
            <Select
              value={field.value}
              onValueChange={(next) => field.onChange(next as OverlapChoice)}
            >
              {overlapChoices.map((choice) => (
                <SelectItem key={choice} value={choice}>
                  {overlapName(i18n, choice)}
                </SelectItem>
              ))}
            </Select>
          </Field>
        )}
      />
    </DialogForm>
  );
}

/** The confirmation of revoking a key, open while a key is chosen. */
function RevokeKey({
  project,
  environment,
  sdkKey,
  onDone,
}: {
  project: string;
  environment: string;
  sdkKey: SdkKey | undefined;
  onDone: () => void;
}) {
  const { t } = useLingui();
  const revoke = useListMutation(
    (key: SdkKey) => unwrap(revokeSdkKey({ path: { project, environment, sdk_key: key.id } })),
    sdkKeysQuery(project, environment).queryKey,
    onDone,
  );
  const name = sdkKey?.name ?? "";
  const keyId = sdkKey?.key_id ?? "";
  return (
    <ConfirmDialog
      open={sdkKey !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          revoke.reset();
          onDone();
        }
      }}
      title={t`Revoke key ${name}`}
      confirmLabel={<Trans>Revoke key</Trans>}
      destructive
      pending={revoke.isPending}
      refusal={revoke.error}
      onConfirm={() => sdkKey && revoke.mutate(sdkKey)}
    >
      <p>
        <Trans>
          Every SDK using the key {keyId} is refused at once, even within a rotation overlap. This
          cannot be undone.
        </Trans>
      </p>
    </ConfirmDialog>
  );
}
