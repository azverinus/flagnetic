import type { SmtpSettings } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleAlertIcon, CircleCheckIcon, SendIcon, Trash2Icon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { toast } from "sonner";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { ApiError } from "@/lib/errors";
import {
  bodyOf,
  formOf,
  type MailForm,
  mailFormSchema,
  mailProblem,
  mailSettingsApi,
  mailSettingsQuery,
  pointerOf,
} from "@/lib/mail-settings";
import { forgetsSecret } from "@/lib/mutations";
import { authMethodsQuery } from "@/lib/queries";

/**
 * The instance's SMTP server: the form that sets it, the test mail to the
 * member looking at it, and its removal. `recipient` is the member's own
 * address, where the test mail goes.
 */
export function MailSettingsScreen({ recipient }: { recipient: string }) {
  const { t } = useLingui();
  const settings = useQuery(mailSettingsQuery);
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      {settings.isSuccess ? (
        <MailSettingsPage settings={settings.data} recipient={recipient} />
      ) : (
        <>
          <PageHeader title={t`Mail`} />
          {settings.isError ? (
            <QueryError error={settings.error} onRetry={() => void settings.refetch()} />
          ) : (
            <div className="flex max-w-2xl flex-col gap-5" aria-busy>
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </div>
          )}
        </>
      )}
    </div>
  );
}

function MailSettingsPage({
  settings,
  recipient,
}: {
  settings: SmtpSettings | null;
  recipient: string;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const [removing, setRemoving] = useState(false);

  /** Puts what a write answered in place of what was read. */
  const remember = (saved: SmtpSettings | null) => {
    queryClient.setQueryData(mailSettingsQuery.queryKey, saved);
    // Whether a forgotten password can be reset by mail depends on SMTP.
    void queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
  };
  const test = useMutation({
    mutationFn: mailSettingsApi.test,
    meta: { toast: false },
    onError: (error) => {
      // Another admin removed the server since the page read it: the
      // refusal says so, and the page reads what is stored again.
      if (error instanceof ApiError && error.code === "smtp_not_configured") {
        void queryClient.invalidateQueries({ queryKey: mailSettingsQuery.queryKey });
      }
    },
  });
  const removed = () => {
    setRemoving(false);
    test.reset();
    remember(null);
    toast.success(t`The SMTP server is removed.`);
  };
  const remove = useMutation({
    mutationFn: mailSettingsApi.remove,
    meta: { toast: false },
    onSuccess: removed,
    onError: (error) => {
      // Nothing to remove means another admin removed it first, which is
      // what was asked for.
      if (error instanceof ApiError && error.status === 404) removed();
    },
  });

  return (
    <>
      <PageHeader
        title={t`Mail`}
        description={<Trans>The SMTP server the instance sends its mail through.</Trans>}
        actions={
          settings ? (
            <>
              <Button variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
                <SendIcon aria-hidden />
                {test.isPending ? <Trans>Sending…</Trans> : <Trans>Send a test mail</Trans>}
              </Button>
              <Button variant="outline" onClick={() => setRemoving(true)}>
                <Trash2Icon aria-hidden />
                <Trans>Remove</Trans>
              </Button>
            </>
          ) : null
        }
      />
      {settings ? (
        <TestResult
          pending={test.isPending}
          sent={test.isSuccess}
          failure={test.error instanceof ApiError ? test.error : undefined}
          recipient={recipient}
        />
      ) : (
        <p className="max-w-2xl rounded-md border bg-muted p-3 text-sm">
          <Trans>
            No SMTP server is configured, so the instance mails nothing, and members cannot reset a
            forgotten password themselves.
          </Trans>
        </p>
      )}
      {/* Configuring or removing the server starts the form over. */}
      <MailSettingsForm
        key={settings ? "configured" : "none"}
        settings={settings}
        onSaved={(saved) => {
          // A test answered for the settings before the save; test again.
          test.reset();
          remember(saved);
          toast.success(t`The mail settings are saved.`);
        }}
      />
      <ConfirmDialog
        open={removing}
        onOpenChange={(open) => {
          if (!open) {
            remove.reset();
            setRemoving(false);
          }
        }}
        title={t`Remove the SMTP server`}
        confirmLabel={<Trans>Remove SMTP server</Trans>}
        destructive
        pending={remove.isPending}
        refusal={remove.error}
        refusalMessage={mailProblem}
        onConfirm={() => remove.mutate()}
      >
        <p>
          <Trans>
            The instance stops mailing, and members can no longer reset a forgotten password
            themselves. The stored password is deleted.
          </Trans>
        </p>
      </ConfirmDialog>
    </>
  );
}

/** The SMTP settings' form; the password is write-only. */
function MailSettingsForm({
  settings,
  onSaved,
}: {
  settings: SmtpSettings | null;
  onSaved: (saved: SmtpSettings) => void;
}) {
  const { i18n, t } = useLingui();
  const passwordSet = settings?.password_set ?? false;
  const values = useMemo(() => formOf(settings), [settings]);
  const form = useForm<MailForm>({
    resolver: zodResolver(mailFormSchema(i18n, { stored: settings })),
    values,
    // Settings read again while the form holds edits, after another admin
    // saved, change only the fields not edited here.
    resetOptions: { keepDirtyValues: true },
  });
  const save = useMutation({
    mutationFn: (values: MailForm) => mailSettingsApi.save(bodyOf(values)),
    // The variables carry the password.
    ...forgetsSecret,
    meta: { toast: false },
    onSuccess: (saved) => {
      // An explicit reset replaces the edits with what was stored, even when
      // the save changed nothing and the settings given to the form stay
      // the same.
      form.reset(formOf(saved), { keepDirtyValues: false });
      onSaved(saved);
    },
  });
  // A refusal answers what was submitted: once a field is edited, the
  // server's word on what it held no longer stands under it.
  const forgetRefusal = save.reset;
  useEffect(
    () =>
      form.subscribe({
        formState: { values: true },
        callback: ({ type }) => {
          if (type === "change") forgetRefusal();
        },
      }),
    [form, forgetRefusal],
  );
  const { errors, dirtyFields } = form.formState;
  // A reset that keeps the edits clears isDirty but keeps the edited fields.
  const isDirty = Object.keys(dirtyFields).length > 0;
  const refusal = save.error instanceof ApiError ? save.error : undefined;
  // A refusal the fields do not say themselves is explained above the actions.
  const fieldsExplain =
    refusal !== undefined &&
    refusal.violations.length > 0 &&
    refusal.violations.every((v) => (Object.values(pointerOf) as string[]).includes(v.pointer));

  return (
    <>
      <FormPage
        onSubmit={form.handleSubmit((values) => save.mutate(values))}
        submitLabel={<Trans>Save</Trans>}
        pending={save.isPending}
      >
        <div className="grid gap-5 sm:grid-cols-[1fr_8rem]">
          <Field
            label={<Trans>Host</Trans>}
            hint={<Trans>The SMTP server's host name or address.</Trans>}
            error={errors.host?.message}
            refusal={refusal}
            pointer={pointerOf.host}
          >
            <Input
              autoComplete="off"
              spellCheck={false}
              placeholder="smtp.example.com"
              {...form.register("host")}
            />
          </Field>
          <Field
            label={<Trans>Port</Trans>}
            error={errors.port?.message}
            refusal={refusal}
            pointer={pointerOf.port}
          >
            <Input inputMode="numeric" autoComplete="off" {...form.register("port")} />
          </Field>
        </div>
        <Controller
          control={form.control}
          name="security"
          render={({ field }) => (
            <Field
              label={<Trans>Security</Trans>}
              hint={
                <Trans>
                  STARTTLS and TLS need TLS 1.2 or later and verify the server's certificate. With
                  none, nothing is encrypted, and a username and password are sent only to a server
                  on this machine.
                </Trans>
              }
              error={errors.security?.message}
              refusal={refusal}
              pointer={pointerOf.security}
            >
              <Select
                ref={field.ref}
                value={field.value}
                onValueChange={field.onChange}
                onBlur={field.onBlur}
              >
                <SelectItem value="starttls">{t`STARTTLS, usually port 587`}</SelectItem>
                <SelectItem value="tls">{t`TLS from the first byte, usually port 465`}</SelectItem>
                <SelectItem value="none">{t`None, usually port 25`}</SelectItem>
              </Select>
            </Field>
          )}
        />
        <Field
          label={<Trans>Username</Trans>}
          hint={<Trans>Leave empty when the server takes mail without signing in.</Trans>}
          error={errors.username?.message}
          refusal={refusal}
          pointer={pointerOf.username}
        >
          <Input autoComplete="off" spellCheck={false} {...form.register("username")} />
        </Field>
        <Field
          label={<Trans>Password</Trans>}
          hint={
            passwordSet ? (
              <Trans>
                A password is stored and never shown again. Leave this empty to keep it, or type a
                new one to replace it. Changing the host, port, security or username needs it again.
                Clearing the username removes both.
              </Trans>
            ) : (
              <Trans>Stored encrypted and never shown again.</Trans>
            )
          }
          error={errors.password?.message}
          refusal={refusal}
          pointer={pointerOf.password}
        >
          <Input
            type="password"
            autoComplete="new-password"
            spellCheck={false}
            {...form.register("password")}
          />
        </Field>
        <Field
          label={<Trans>Sender</Trans>}
          hint={
            <Trans>
              Who every mail comes from: one address, optionally with a name, such as Flagnetic
              &lt;flags@example.com&gt;.
            </Trans>
          }
          error={errors.from?.message}
          refusal={refusal}
          pointer={pointerOf.from}
        >
          <Input autoComplete="off" spellCheck={false} {...form.register("from")} />
        </Field>
        {refusal && !fieldsExplain ? <MailRefusal error={refusal} /> : null}
      </FormPage>
      <UnsavedChangesBar
        dirty={isDirty}
        onDiscard={() => form.reset(undefined, { keepDirtyValues: false })}
        pending={save.isPending}
      />
    </>
  );
}

function MailRefusal({ error }: { error: ApiError }) {
  const { i18n } = useLingui();
  return (
    <p
      role="alert"
      className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
    >
      <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
      {mailProblem(i18n, error)}
    </p>
  );
}

/** What became of the last test mail: on its way, accepted, or why not. */
function TestResult({
  pending,
  sent,
  failure,
  recipient,
}: {
  pending: boolean;
  sent: boolean;
  failure: ApiError | undefined;
  recipient: string;
}) {
  if (pending) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        <Trans>Sending a test mail through the saved settings…</Trans>
      </p>
    );
  }
  if (failure) return <MailRefusal error={failure} />;
  if (sent) {
    return (
      <p role="status" className="flex items-start gap-2 rounded-md border p-3 text-sm">
        <CircleCheckIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-primary" />
        <span>
          <Trans>The SMTP server accepted a test mail to {recipient}. Check that it arrived.</Trans>
        </span>
      </p>
    );
  }
  return (
    <p className="text-sm text-muted-foreground">
      <Trans>The test mail goes to {recipient} through the saved settings.</Trans>
    </p>
  );
}
