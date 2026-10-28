import type { Webhook, WebhookEventTypeInfo, WebhookWithSecret } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import { QueryError } from "@/components/query-error";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { ApiError } from "@/lib/errors";
import { nameSchema } from "@/lib/forms";
import { useListMutation } from "@/lib/mutations";
import { problemMessage } from "@/lib/problem-messages";
import { environmentsQuery } from "@/lib/queries";
import {
  auditEntryType,
  bodyOf,
  eventGroupName,
  eventGroups,
  everyType,
  fieldOfRefusal,
  formOf,
  isPlatformDisabled,
  patchOf,
  type WebhookForm,
  type WebhookScope,
  webhookApi,
  webhookEventTypesQuery,
  webhooksKey,
} from "@/lib/webhooks";

export type WebhookEditorProps = {
  scope: WebhookScope;
  /** The subscription being edited; none for a new one. */
  webhook?: Webhook;
  /** Whether the form is a dialog's content or a page's. */
  layout: "dialog" | "page";
  onCancel?: () => void;
  /** The subscription as it is now; for a new one, with its secret, shown this once. */
  onSaved: (saved: Webhook | WebhookWithSecret) => void;
};

/**
 * A webhook subscription's name, target, event types, environments and
 * whether it is enabled. The target an instance refuses and an event type
 * the catalogue lacks are explained under their fields; the event types
 * are chosen from the catalogue the server answers.
 */
export function WebhookEditor({ scope, webhook, layout, onCancel, onSaved }: WebhookEditorProps) {
  const { i18n, t } = useLingui();
  const catalogue = useQuery(webhookEventTypesQuery);
  const environments = useQuery({
    ...environmentsQuery(scope === "instance" ? "" : scope.project),
    enabled: scope !== "instance",
  });
  const instance = scope === "instance";
  const api = webhookApi(scope);

  const schema = z.object({
    name: nameSchema(i18n),
    url: z
      .string()
      .trim()
      .min(1, t`Enter the URL the events are posted to.`)
      .refine(
        (value) => {
          try {
            const url = new URL(value);
            return url.protocol === "https:" || url.protocol === "http:";
          } catch {
            return false;
          }
        },
        t`Enter an absolute URL, such as https://example.com/flagnetic.`,
      ),
    events: z.array(z.string()).min(1, t`Choose at least one event type.`),
    environments: z.array(z.string()),
    enabled: z.boolean(),
  });
  const form = useForm<WebhookForm>({
    resolver: zodResolver(schema),
    defaultValues: webhook
      ? formOf(webhook)
      : {
          name: "",
          url: "",
          events: instance ? [auditEntryType] : [],
          environments: [],
          enabled: true,
        },
  });
  const save = useListMutation(
    (values: WebhookForm) =>
      webhook ? api.update(webhook, patchOf(webhook, values)) : api.create(bodyOf(values)),
    webhooksKey(scope),
    // A save makes a new version, which starts the form over: the screen
    // that is told must not be the one the answer replaces.
    onSaved,
    // Creating answers the secret.
    { secret: webhook === undefined },
  );
  const { errors } = form.formState;
  const refusedField = save.error instanceof ApiError ? fieldOfRefusal(save.error.code) : undefined;
  const refusalHere = refusedField ? problemMessage(i18n, save.error as ApiError) : undefined;

  const submit = form.handleSubmit((values) => save.mutate(values));
  // The platform's disable is undone by enabling, not by this switch, which
  // the server refuses for such a subscription.
  const platformDisabled = webhook !== undefined && isPlatformDisabled(webhook);

  const fields = (
    <>
      <Field
        label={<Trans>Name</Trans>}
        hint={<Trans>What receives the events, to tell the subscriptions apart.</Trans>}
        error={errors.name?.message}
        refusal={save.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus={layout === "dialog"} {...form.register("name")} />
      </Field>
      <Field
        label={<Trans>URL</Trans>}
        hint={<Trans>Where every event is posted, signed with the subscription's secret.</Trans>}
        error={errors.url?.message ?? (refusedField === "url" ? refusalHere : undefined)}
        refusal={save.error}
        pointer="/url"
      >
        <Input type="url" autoComplete="off" spellCheck={false} {...form.register("url")} />
      </Field>
      {instance ? (
        <p className="text-sm text-muted-foreground">
          <Trans>
            An instance webhook is sent one event for every audit entry that names no project:
            instance settings, members, sign-ins and project deletions.
          </Trans>
        </p>
      ) : (
        <Controller
          control={form.control}
          name="events"
          render={({ field }) => (
            <EventPicker
              value={field.value}
              onChange={field.onChange}
              catalogue={catalogue.data}
              failure={catalogue.error}
              onRetry={() => void catalogue.refetch()}
              error={
                errors.events?.message ?? (refusedField === "events" ? refusalHere : undefined)
              }
            />
          )}
        />
      )}
      {instance ? null : (
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
                  Send only the events of the environments you tick; tick none for every
                  environment. An event of no environment is always sent.
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
                  {environment.name}
                </label>
              ))}
            </fieldset>
          )}
        />
      )}
      {platformDisabled ? null : (
        <Controller
          control={form.control}
          name="enabled"
          render={({ field }) => <EnabledSwitch checked={field.value} onChange={field.onChange} />}
        />
      )}
    </>
  );

  // A refusal a field explains is left out of the form's own.
  const refusal = refusedField ? undefined : save.error;
  return layout === "dialog" ? (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel ?? (() => undefined)}
      submitLabel={webhook ? <Trans>Save</Trans> : <Trans>Create webhook</Trans>}
      pending={save.isPending}
      refusal={refusal}
    >
      {fields}
    </DialogForm>
  ) : (
    <FormPage
      onSubmit={submit}
      submitLabel={<Trans>Save</Trans>}
      pending={save.isPending}
      refusal={refusal}
    >
      {fields}
    </FormPage>
  );
}

function EnabledSwitch({
  checked,
  onChange,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="flex flex-col gap-1">
        <Label htmlFor={id}>
          <Trans>Enabled</Trans>
        </Label>
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          <Trans>A disabled subscription is sent nothing until it is enabled again.</Trans>
        </p>
      </div>
      <Switch
        id={id}
        aria-describedby={`${id}-hint`}
        checked={checked}
        onCheckedChange={onChange}
      />
    </div>
  );
}

/**
 * The event types of the catalogue as checkboxes grouped by what they are
 * about. "Every event" covers each type but the audit entries, which are
 * asked for apart since they copy every change.
 */
function EventPicker({
  value,
  onChange,
  catalogue,
  failure,
  onRetry,
  error,
}: {
  value: string[];
  onChange: (value: string[]) => void;
  catalogue: WebhookEventTypeInfo[] | undefined;
  failure: unknown;
  onRetry: () => void;
  error: string | undefined;
}) {
  const { i18n } = useLingui();
  const hint = useId();
  const every = value.includes(everyType);
  const toggle = (type: string, on: boolean) =>
    onChange(on ? [...value, type] : value.filter((chosen) => chosen !== type));
  return (
    <fieldset aria-describedby={hint} className="flex flex-col gap-3">
      <legend className="text-sm font-medium">
        <Trans>Events</Trans>
      </legend>
      <p id={hint} className="text-sm text-muted-foreground">
        <Trans>Choose the event types this subscription is sent.</Trans>
      </p>
      {failure ? (
        <QueryError error={failure} onRetry={onRetry} />
      ) : catalogue === undefined ? (
        <div className="flex flex-col gap-2" aria-busy>
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-56" />
        </div>
      ) : (
        <>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={every}
              onChange={(event) => toggle(everyType, event.target.checked)}
              className="mt-0.5 size-4 accent-primary"
            />
            <span className="flex flex-col">
              <span>
                <Trans>Every event</Trans>
              </span>
              <span className="text-muted-foreground">
                <Trans>
                  Every type below, and any added later. Audit entries are chosen apart.
                </Trans>
              </span>
            </span>
          </label>
          {eventGroups(catalogue).map((group) => (
            <fieldset key={group.entity} className="flex flex-col gap-1.5">
              <legend className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
                {eventGroupName(i18n, group.entity)}
              </legend>
              {group.types.map((info) => (
                <label key={info.type} className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={every || value.includes(info.type)}
                    disabled={every}
                    onChange={(event) => toggle(info.type, event.target.checked)}
                    className="mt-0.5 size-4 accent-primary"
                  />
                  <span className="flex flex-col">
                    <code className="font-mono text-xs">{info.type}</code>
                    <span className="text-muted-foreground">{info.description}</span>
                  </span>
                </label>
              ))}
            </fieldset>
          ))}
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={value.includes(auditEntryType)}
              onChange={(event) => toggle(auditEntryType, event.target.checked)}
              className="mt-0.5 size-4 accent-primary"
            />
            <span className="flex flex-col">
              <code className="font-mono text-xs">{auditEntryType}</code>
              <span className="text-muted-foreground">
                <Trans>
                  A copy of every audit entry of the project, so each change is sent twice when it
                  is chosen with the types above.
                </Trans>
              </span>
            </span>
          </label>
        </>
      )}
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
    </fieldset>
  );
}
