import type { Sink, SinkKind, SinkWithSecret } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PlugZapIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { type ReactNode, useId } from "react";
import {
  Controller,
  type FieldErrors,
  type FieldPath,
  type Resolver,
  set,
  type UseFormReturn,
  useFieldArray,
  useForm,
  useWatch,
} from "react-hook-form";

import { ConflictBanner } from "@/components/conflict-banner";
import { EnvironmentName } from "@/components/protected-mark";
import { SinkTestOutcome } from "@/components/sink-test";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { SwitchField } from "@/components/ui/switch-field";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { ApiError } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { problemMessage, violationMessage } from "@/lib/problem-messages";
import { environmentsQuery } from "@/lib/queries";
import {
  batchingDefaults,
  bodyOf,
  changedFieldNames,
  emptyForm,
  eventKindDescription,
  eventKindName,
  formOf,
  formProblems,
  kindDescription,
  kindName,
  patchOf,
  pointerOfName,
  sinkApi,
  sinkEventKinds,
  sinkKinds,
  sinkKey,
  sinksKey,
  type SinkForm,
  testBodyOf,
} from "@/lib/sinks";
import { useFormConflict } from "@/lib/use-form-conflict";

export type SinkEditorProps = {
  project: string;
  /** The sink being edited; none for a new one. */
  sink?: Sink;
  /** The kind a new sink opens with. */
  initialKind?: SinkKind;
  /** The sink as it is now; a new webhook sink comes with its secret, shown this once. */
  onSaved: (saved: Sink | SinkWithSecret) => void;
};

/** The parts of a request body the editor shows an error under. */
const shownPointers = [
  "/name",
  "/clickhouse/",
  "/object_storage/",
  "/webhook/",
  "/event_kinds",
  "/environments",
  "/batch_size",
  "/flush_interval",
];

/** Whether every violation of a refusal is explained under a field of the editor. */
function explainedByFields(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.violations.length > 0 &&
    error.violations.every((v) => shownPointers.some((prefix) => v.pointer.startsWith(prefix)))
  );
}

/**
 * A sink's name, kind, the kind's configuration, the event kinds and
 * environments it exports and its batching. The server checks a sink only
 * when it is enabled, so saving is never held back by the target: what
 * the server refuses in the configuration is explained under its field.
 * Secrets are write-only and left empty, an empty one keeping the stored
 * secret.
 */
export function SinkEditor({ project, sink, initialKind, onSaved }: SinkEditorProps) {
  const { i18n } = useLingui();
  const queryClient = useQueryClient();
  const environments = useQuery(environmentsQuery(project));
  const api = sinkApi(project);
  const editing = sink !== undefined;
  const testHeading = useId();

  const resolver: Resolver<SinkForm> = (values) => {
    const problems = formProblems(i18n, values, editing);
    if (problems.length === 0) return { values, errors: {} };
    const errors: FieldErrors<SinkForm> = {};
    for (const { path, message } of problems) set(errors, path, { type: "validate", message });
    return { values: {}, errors };
  };

  const form = useForm<SinkForm>({
    resolver,
    defaultValues: sink ? formOf(sink) : emptyForm(initialKind),
  });
  const kind = useWatch({ control: form.control, name: "kind" });

  const conflicts = useFormConflict({
    project,
    kind: "sink",
    initial: sink,
    read: () => api.get(sink?.id ?? ""),
  });
  const { base, conflict } = conflicts;

  const save = useListMutation(
    ({ values, at }: { values: SinkForm; at?: number }) =>
      base && sink
        ? api.update(
            { id: sink.id, version: at ?? conflicts.version ?? sink.version },
            patchOf(base, values),
          )
        : api.create(bodyOf(values)),
    sinksKey(project),
    onSaved,
    // Creating a webhook sink answers its secret.
    { secret: !editing },
  );
  // Testing stores nothing, so it never blocks saving: the form is only
  // checked for what a request cannot be made without.
  const test = useMutation({
    mutationFn: (values: SinkForm) =>
      sink ? api.test(sink.id, testBodyOf(sink, values)) : api.testConfig(bodyOf(values)),
    meta: { toast: false },
    onSuccess: async () => {
      // A saved sink's test stores what it found as the sink's warnings.
      if (sink) await queryClient.invalidateQueries({ queryKey: sinkKey(project, sink.id) });
    },
  });
  const runTest = form.handleSubmit((values) => {
    save.reset();
    test.mutate(values);
  });
  const send = (at: number | undefined) =>
    form.handleSubmit((values) => {
      conflicts.clear();
      test.reset();
      save.mutate({ values, at }, { onError: (error) => conflicts.meet(error) });
    });

  const refusal = conflicts.refusal(save.error);
  const hereRefusal = explainedByFields(refusal) ? undefined : refusal;
  // The refusal of a save or of a test, whichever was made last, under the fields.
  const fieldRefusal = save.error ?? test.error;
  const testRefusal = explainedByFields(test.error) ? undefined : test.error;
  const { errors } = form.formState;

  return (
    <>
      <FormPage
        onSubmit={send(conflicts.version)}
        submitLabel={editing ? <Trans>Save</Trans> : <Trans>Create sink</Trans>}
        pending={save.isPending}
        refusal={hereRefusal}
        className="max-w-3xl"
      >
        {base && conflict ? (
          <ConflictBanner
            subject="form"
            entry={conflict.entry}
            diff={
              <ul className="list-disc pl-5">
                {changedFieldNames(i18n, base, conflict.current).map((name) => (
                  <li key={name}>{name}</li>
                ))}
              </ul>
            }
            actions={
              <>
                <Button
                  type="button"
                  variant="outline"
                  disabled={save.isPending}
                  onClick={() => {
                    const { current } = conflict;
                    form.reset(formOf(current));
                    conflicts.restart(current);
                    save.reset();
                    void queryClient.invalidateQueries({ queryKey: sinksKey(project) });
                  }}
                >
                  <Trans>Reload and lose my edits</Trans>
                </Button>
                <Button
                  type="button"
                  disabled={save.isPending}
                  onClick={() => void send(conflicts.keep(conflict.current))()}
                >
                  <Trans>Keep my edits</Trans>
                </Button>
              </>
            }
          />
        ) : null}

        <Field
          label={<Trans>Name</Trans>}
          hint={<Trans>What the sink delivers to, to tell the sinks apart.</Trans>}
          error={errors.name?.message}
          refusal={fieldRefusal}
          pointer="/name"
        >
          <Input autoComplete="off" autoFocus={!editing} {...form.register("name")} />
        </Field>

        {editing ? (
          <div className="flex flex-col gap-1">
            <span className="text-sm font-medium">
              <Trans>Kind</Trans>
            </span>
            <p className="text-sm">{kindName(i18n, kind)}</p>
            <p className="text-sm text-muted-foreground">
              <Trans>A sink's kind is fixed when it is created.</Trans>
            </p>
          </div>
        ) : (
          <Controller
            control={form.control}
            name="kind"
            render={({ field }) => <KindPicker value={field.value} onChange={field.onChange} />}
          />
        )}

        {kind === "clickhouse" ? (
          <ClickHouseFields form={form} refusal={fieldRefusal} editing={editing} />
        ) : kind === "object_storage" ? (
          <ObjectStorageFields form={form} refusal={fieldRefusal} editing={editing} />
        ) : (
          <WebhookFields form={form} refusal={fieldRefusal} />
        )}

        <Controller
          control={form.control}
          name="event_kinds"
          render={({ field }) => (
            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">
                <Trans>Event kinds</Trans>
              </legend>
              <p className="text-sm text-muted-foreground">
                <Trans>Export only the kinds you tick; tick none for every kind.</Trans>
              </p>
              {sinkEventKinds.map((eventKind) => (
                <label key={eventKind} className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={field.value.includes(eventKind)}
                    onChange={(event) =>
                      field.onChange(
                        event.target.checked
                          ? [...field.value, eventKind]
                          : field.value.filter((chosen) => chosen !== eventKind),
                      )
                    }
                    className="mt-0.5 size-4 accent-primary"
                  />
                  <span className="flex flex-col">
                    <span>{eventKindName(i18n, eventKind)}</span>
                    <span className="text-muted-foreground">
                      {eventKindDescription(i18n, eventKind)}
                    </span>
                  </span>
                </label>
              ))}
              <PointerErrors error={fieldRefusal} prefix="/event_kinds" />
            </fieldset>
          )}
        />

        <Controller
          control={form.control}
          name="environments"
          render={({ field }) => (
            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">
                <Trans>Environments</Trans>
              </legend>
              <p className="text-sm text-muted-foreground">
                <Trans>
                  Export only the events of the environments you tick; tick none for every
                  environment.
                </Trans>
              </p>
              {(environments.data ?? []).map((environment) => (
                <label key={environment.key} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={field.value.includes(environment.key)}
                    onChange={(event) =>
                      field.onChange(
                        event.target.checked
                          ? [...field.value, environment.key]
                          : field.value.filter((key) => key !== environment.key),
                      )
                    }
                    className="size-4 accent-primary"
                  />
                  <EnvironmentName environment={environment} />
                </label>
              ))}
              <PointerErrors error={fieldRefusal} prefix="/environments" />
            </fieldset>
          )}
        />

        <BatchingFields form={form} kind={kind} refusal={fieldRefusal} />

        <section className="flex flex-col gap-2" aria-labelledby={testHeading}>
          <h2 id={testHeading} className="text-sm font-medium">
            <Trans>Test connection</Trans>
          </h2>
          <p className="text-sm text-muted-foreground">
            {editing ? (
              <Trans>
                Calls the target with what the form holds now. A secret left empty is the stored
                one. Nothing is saved, and saving is never held back by the test.
              </Trans>
            ) : (
              <Trans>
                Calls the target with what the form holds. Nothing is saved or created, and saving
                is never held back by the test.
              </Trans>
            )}
          </p>
          <div>
            <Button
              type="button"
              variant="outline"
              disabled={test.isPending}
              onClick={() => void runTest()}
            >
              <PlugZapIcon aria-hidden />
              <Trans>Test connection</Trans>
            </Button>
          </div>
          {test.data ? <SinkTestOutcome result={test.data} /> : null}
          {testRefusal instanceof ApiError ? (
            <p role="alert" className="text-sm text-destructive">
              {problemMessage(i18n, testRefusal)}
            </p>
          ) : null}
        </section>
      </FormPage>
      {editing ? (
        <UnsavedChangesBar
          dirty={form.formState.isDirty}
          onDiscard={() => form.reset(sink ? formOf(sink) : undefined)}
          pending={save.isPending}
        />
      ) : null}
    </>
  );
}

/** The messages of every violation under a pointer prefix, under a group of fields. */
function PointerErrors({ error, prefix }: { error: unknown; prefix: string }) {
  const { i18n } = useLingui();
  if (!(error instanceof ApiError)) return null;
  const messages = error.violations
    .filter((violation) => violation.pointer.startsWith(prefix))
    .map((violation) => violationMessage(i18n, violation));
  return messages.length === 0 ? null : (
    <p role="alert" className="text-sm text-destructive">
      {[...new Set(messages)].join(" ")}
    </p>
  );
}

function KindPicker({ value, onChange }: { value: SinkKind; onChange: (kind: SinkKind) => void }) {
  const { i18n } = useLingui();
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="text-sm font-medium">
        <Trans>Kind</Trans>
      </legend>
      <p className="text-sm text-muted-foreground">
        <Trans>Where the events are delivered. It cannot be changed afterwards.</Trans>
      </p>
      {sinkKinds.map((kind) => (
        <label key={kind} className="flex items-start gap-2 rounded-md border p-3 text-sm">
          <input
            type="radio"
            name="kind"
            value={kind}
            checked={value === kind}
            onChange={() => onChange(kind)}
            className="mt-0.5 size-4 accent-primary"
          />
          <span className="flex flex-col">
            <span className="font-medium">{kindName(i18n, kind)}</span>
            <span className="text-muted-foreground">{kindDescription(i18n, kind)}</span>
          </span>
        </label>
      ))}
    </fieldset>
  );
}

type KindFieldsProps = {
  form: UseFormReturn<SinkForm>;
  refusal: unknown;
};

/** The error the form found itself under one of the kind's fields. */
function errorOf(form: UseFormReturn<SinkForm>, path: string): string | undefined {
  const parts = path.split(".");
  let node: unknown = form.formState.errors;
  for (const part of parts) {
    node = (node as Record<string, unknown> | undefined)?.[part];
  }
  return (node as { message?: string } | undefined)?.message;
}

function ClickHouseFields({ form, refusal, editing }: KindFieldsProps & { editing: boolean }) {
  const { t } = useLingui();
  const text = (name: FieldPath<SinkForm>) => form.register(name);
  return (
    <>
      <Field
        label={<Trans>URL</Trans>}
        hint={
          <Trans>
            Where ClickHouse's HTTP interface answers, such as https://clickhouse.example.com:8443.
            The user and password have fields of their own.
          </Trans>
        }
        error={errorOf(form, "clickhouse.url")}
        refusal={refusal}
        pointer="/clickhouse/url"
      >
        <Input type="url" autoComplete="off" spellCheck={false} {...text("clickhouse.url")} />
      </Field>
      <Field
        label={<Trans>Database</Trans>}
        hint={<Trans>The database the tables are created in or already exist in.</Trans>}
        error={errorOf(form, "clickhouse.database")}
        refusal={refusal}
        pointer="/clickhouse/database"
      >
        <Input autoComplete="off" spellCheck={false} {...text("clickhouse.database")} />
      </Field>
      <Field
        label={<Trans>Table prefix</Trans>}
        hint={
          <Trans>
            Every table name starts with this, such as flagnetic_exposures. The default is
            flagnetic.
          </Trans>
        }
        error={errorOf(form, "clickhouse.table_prefix")}
        refusal={refusal}
        pointer="/clickhouse/table_prefix"
      >
        <Input
          autoComplete="off"
          spellCheck={false}
          placeholder="flagnetic"
          {...text("clickhouse.table_prefix")}
        />
      </Field>
      <Field
        label={<Trans>Username</Trans>}
        hint={<Trans>The user that inserts the events.</Trans>}
        error={errorOf(form, "clickhouse.username")}
        refusal={refusal}
        pointer="/clickhouse/username"
      >
        <Input autoComplete="off" spellCheck={false} {...text("clickhouse.username")} />
      </Field>
      <Field
        label={<Trans>Password</Trans>}
        hint={<SecretHint editing={editing} />}
        error={errorOf(form, "clickhouse.password")}
        refusal={refusal}
        pointer="/clickhouse/password"
      >
        <Input
          type="password"
          autoComplete="new-password"
          placeholder={editing ? "***" : undefined}
          {...text("clickhouse.password")}
        />
      </Field>
      <Controller
        control={form.control}
        name="clickhouse.tls_skip_verify"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Skip TLS verification</Trans>}
            hint={
              <Trans>
                Accept any certificate of the server. Use it only for a server with a self-signed
                certificate you trust.
              </Trans>
            }
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
      <Controller
        control={form.control}
        name="clickhouse.create_tables"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Create tables</Trans>}
            hint={
              <Trans>
                Create the tables when the sink is enabled. Turn it off to bring tables you made
                yourself.
              </Trans>
            }
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
      <PairsEditor
        form={form}
        name="clickhouse.settings"
        refusal={refusal}
        pointerPrefix="/clickhouse/settings"
        legend={<Trans>Settings</Trans>}
        hint={
          <Trans>
            ClickHouse settings sent with every insert. The ones delivery sets itself are refused.
          </Trans>
        }
        nameLabel={t`Setting name`}
        valueLabel={t`Setting value`}
        addLabel={t`Add a setting`}
        removeLabel={(name) => t`Remove the setting ${name}`}
      />
    </>
  );
}

function SecretHint({ editing }: { editing: boolean }) {
  return editing ? (
    <Trans>
      Stored sealed and never shown. Unchanged unless you type a new one; after changing the address
      or the user, type it again.
    </Trans>
  ) : (
    <Trans>Stored sealed and never shown again.</Trans>
  );
}

function ObjectStorageFields({ form, refusal, editing }: KindFieldsProps & { editing: boolean }) {
  const text = (name: FieldPath<SinkForm>) => form.register(name);
  const format = useWatch({ control: form.control, name: "object_storage.format" });
  return (
    <>
      <Field
        label={<Trans>Endpoint</Trans>}
        hint={
          <Trans>
            A scheme, a host and a port, such as https://s3.example.com. Leave empty for AWS S3.
          </Trans>
        }
        error={errorOf(form, "object_storage.endpoint")}
        refusal={refusal}
        pointer="/object_storage/endpoint"
      >
        <Input autoComplete="off" spellCheck={false} {...text("object_storage.endpoint")} />
      </Field>
      <Field
        label={<Trans>Region</Trans>}
        hint={<Trans>Leave empty to ask the bucket.</Trans>}
        error={errorOf(form, "object_storage.region")}
        refusal={refusal}
        pointer="/object_storage/region"
      >
        <Input autoComplete="off" spellCheck={false} {...text("object_storage.region")} />
      </Field>
      <Field
        label={<Trans>Bucket</Trans>}
        error={errorOf(form, "object_storage.bucket")}
        refusal={refusal}
        pointer="/object_storage/bucket"
      >
        <Input autoComplete="off" spellCheck={false} {...text("object_storage.bucket")} />
      </Field>
      <Field
        label={<Trans>Prefix</Trans>}
        hint={
          <Trans>
            A folder in the bucket the files are written under, without slashes at either end.
          </Trans>
        }
        error={errorOf(form, "object_storage.prefix")}
        refusal={refusal}
        pointer="/object_storage/prefix"
      >
        <Input autoComplete="off" spellCheck={false} {...text("object_storage.prefix")} />
      </Field>
      <Field
        label={<Trans>Access key ID</Trans>}
        error={errorOf(form, "object_storage.access_key_id")}
        refusal={refusal}
        pointer="/object_storage/access_key_id"
      >
        <Input autoComplete="off" spellCheck={false} {...text("object_storage.access_key_id")} />
      </Field>
      <Field
        label={<Trans>Secret access key</Trans>}
        hint={<SecretHint editing={editing} />}
        error={errorOf(form, "object_storage.secret_access_key")}
        refusal={refusal}
        pointer="/object_storage/secret_access_key"
      >
        <Input
          type="password"
          autoComplete="new-password"
          placeholder={editing ? "***" : undefined}
          {...text("object_storage.secret_access_key")}
        />
      </Field>
      <Controller
        control={form.control}
        name="object_storage.path_style"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Path-style addressing</Trans>}
            hint={
              <Trans>
                Address the bucket in the path rather than the host name, which most S3-compatible
                services other than AWS need.
              </Trans>
            }
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
      <Controller
        control={form.control}
        name="object_storage.format"
        render={({ field }) => (
          <Field
            label={<Trans>Format</Trans>}
            hint={<Trans>One file per batch.</Trans>}
            refusal={refusal}
            pointer="/object_storage/format"
          >
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectItem value="jsonl">
                <Trans>JSON lines</Trans>
              </SelectItem>
              <SelectItem value="parquet">
                <Trans>Parquet</Trans>
              </SelectItem>
            </Select>
          </Field>
        )}
      />
      {format === "jsonl" ? (
        <Controller
          control={form.control}
          name="object_storage.compression"
          render={({ field }) => (
            <Field
              label={<Trans>Compression</Trans>}
              hint={<Trans>Parquet files are always compressed with zstd.</Trans>}
              refusal={refusal}
              pointer="/object_storage/compression"
            >
              <Select value={field.value} onValueChange={field.onChange}>
                <SelectItem value="gzip">gzip</SelectItem>
                <SelectItem value="zstd">zstd</SelectItem>
                <SelectItem value="none">
                  <Trans>None</Trans>
                </SelectItem>
              </Select>
            </Field>
          )}
        />
      ) : null}
    </>
  );
}

function WebhookFields({ form, refusal }: KindFieldsProps) {
  const { t } = useLingui();
  return (
    <>
      <Field
        label={<Trans>URL</Trans>}
        hint={
          <Trans>
            Where every batch is posted, signed with the sink's secret. The secret is shown once,
            when the sink is created.
          </Trans>
        }
        error={errorOf(form, "webhook.url")}
        refusal={refusal}
        pointer="/webhook/url"
      >
        <Input type="url" autoComplete="off" spellCheck={false} {...form.register("webhook.url")} />
      </Field>
      <PairsEditor
        form={form}
        name="webhook.headers"
        refusal={refusal}
        pointerPrefix="/webhook/headers"
        legend={<Trans>Headers</Trans>}
        hint={
          <Trans>
            Sent with every call, before the platform's own. Names starting X-Flagnetic- are
            refused.
          </Trans>
        }
        nameLabel={t`Header name`}
        valueLabel={t`Header value`}
        addLabel={t`Add a header`}
        removeLabel={(name) => t`Remove the header ${name}`}
      />
    </>
  );
}

function PairsEditor({
  form,
  name,
  refusal,
  pointerPrefix,
  legend,
  hint,
  nameLabel,
  valueLabel,
  addLabel,
  removeLabel,
}: {
  form: UseFormReturn<SinkForm>;
  name: "clickhouse.settings" | "webhook.headers";
  refusal: unknown;
  pointerPrefix: string;
  legend: ReactNode;
  hint: ReactNode;
  nameLabel: string;
  valueLabel: string;
  addLabel: string;
  removeLabel: (name: string) => string;
}) {
  const { i18n } = useLingui();
  const hintId = useId();
  const { fields, append, remove } = useFieldArray({ control: form.control, name });
  const names = useWatch({ control: form.control, name }).map((pair) => pair.name);
  const violations = refusal instanceof ApiError ? refusal.violations : [];
  return (
    <fieldset aria-describedby={hintId} className="flex flex-col gap-2">
      <legend className="text-sm font-medium">{legend}</legend>
      <p id={hintId} className="text-sm text-muted-foreground">
        {hint}
      </p>
      {fields.map((row, index) => {
        const current = (names[index] as string | undefined) ?? "";
        const violation = violations.find(
          (v) => v.pointer === pointerOfName(pointerPrefix, current.trim()),
        );
        return (
          <div key={row.id} className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <Input
                aria-label={nameLabel}
                autoComplete="off"
                spellCheck={false}
                aria-invalid={violation ? true : undefined}
                {...form.register(`${name}.${index}.name`)}
              />
              <Input
                aria-label={valueLabel}
                autoComplete="off"
                spellCheck={false}
                {...form.register(`${name}.${index}.value`)}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={removeLabel(current)}
                onClick={() => remove(index)}
              >
                <Trash2Icon aria-hidden />
              </Button>
            </div>
            {violation ? (
              <p className="text-sm text-destructive">{violationMessage(i18n, violation)}</p>
            ) : null}
          </div>
        );
      })}
      <div>
        <Button type="button" variant="outline" onClick={() => append({ name: "", value: "" })}>
          <PlusIcon aria-hidden />
          {addLabel}
        </Button>
      </div>
    </fieldset>
  );
}

function BatchingFields({
  form,
  kind,
  refusal,
}: {
  form: UseFormReturn<SinkForm>;
  kind: SinkKind;
  refusal: unknown;
}) {
  const defaults = batchingDefaults[kind];
  const maxBatch = defaults.maxBatchSize;
  const [flushFrom, flushTo] = defaults.flush ?? [0, 0];
  const { errors } = form.formState;
  return (
    <>
      <Field
        label={<Trans>Batch size</Trans>}
        hint={
          defaults.maxBatchSize ? (
            <Trans>
              The most events one delivery carries, at most {maxBatch}. Empty for the default.
            </Trans>
          ) : (
            <Trans>The most events one delivery carries. Empty for the default.</Trans>
          )
        }
        error={errors.batch_size?.message}
        refusal={refusal}
        pointer="/batch_size"
      >
        <Input
          type="number"
          min={1}
          inputMode="numeric"
          placeholder={String(defaults.batch_size)}
          {...form.register("batch_size")}
        />
      </Field>
      <Field
        label={<Trans>Flush interval (seconds)</Trans>}
        hint={
          defaults.flush ? (
            <Trans>
              How long the sink waits between deliveries once it has caught up, from {flushFrom} to{" "}
              {flushTo}. Empty for the default.
            </Trans>
          ) : (
            <Trans>
              How long the sink waits between deliveries once it has caught up. Empty for the
              default.
            </Trans>
          )
        }
        error={errors.flush_interval?.message}
        refusal={refusal}
        pointer="/flush_interval"
      >
        <Input
          type="number"
          min={1}
          inputMode="numeric"
          placeholder={String(defaults.flush_interval)}
          {...form.register("flush_interval")}
        />
      </Field>
    </>
  );
}
