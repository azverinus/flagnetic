import { type AuthSettings, updateAuthSettings } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useMemo } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";

import { DurationInput } from "@/components/duration-input";
import { EnforcedDomainsField, type EnforcedRowErrors } from "@/components/enforced-domains-field";
import { QueryError } from "@/components/query-error";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { SwitchField } from "@/components/ui/switch-field";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import {
  type AuthSettingsValues,
  authSettingsSchema,
  authSettingsViolation,
  type DurationField,
  enforcedRowOf,
  formOf,
  pointerOf,
  settingsOf,
} from "@/lib/auth-settings";
import { authProvidersQuery } from "@/lib/auth-providers";
import { ApiError, unwrap } from "@/lib/errors";
import { formRefusal } from "@/lib/problem-messages";
import { authMethodsQuery, authSettingsQuery, meQuery } from "@/lib/queries";

/**
 * The instance's rules for sessions, API tokens, passwords, their
 * self-service reset, local login and the email domains that sign in
 * through one auth provider only. Every replica applies a change within a
 * second; a lowered timeout ends the sessions already past it.
 */
export function AuthSettingsForm({ settings }: { settings: AuthSettings }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const expiryRequired = useId();
  const selfServiceReset = useId();
  const values = useMemo(() => formOf(settings), [settings]);
  const providers = useQuery(authProvidersQuery);
  const form = useForm<AuthSettingsValues>({
    resolver: zodResolver(authSettingsSchema(i18n)),
    values,
    resetOptions: { keepDirtyValues: true },
  });
  const save = useMutation({
    mutationFn: (body: AuthSettings) => unwrap(updateAuthSettings({ body })),
    meta: { toast: false },
    onSuccess: async (saved) => {
      queryClient.setQueryData(authSettingsQuery.queryKey, saved);
      // An explicit reset replaces the edits, which the form otherwise
      // keeps when the settings it was given change under it.
      form.reset(formOf(saved), { keepDirtyValues: false });
      // Every member reads the token policy from `GET /me`, and the
      // sign-in page whether to offer a forgotten password from the methods.
      await queryClient.invalidateQueries({ queryKey: meQuery.queryKey });
      await queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
      toast.success(t`The auth settings are saved.`);
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
  const { errors, isDirty } = form.formState;
  const refusal = save.error instanceof ApiError ? save.error : undefined;
  // The server's violation of a field, said under it.
  const serverError = (field: keyof AuthSettingsValues) => {
    const violation = refusal?.violations.find((v) => v.pointer === pointerOf[field]);
    return violation ? authSettingsViolation(i18n, violation) : undefined;
  };

  // A violation of a row of the enforced domains is said under that row.
  const enforcedRows = useWatch({ control: form.control, name: "sso_enforced_domains" });
  const enforcedErrors: (EnforcedRowErrors | undefined)[] = [];
  const rowErrors = errors.sso_enforced_domains;
  const rowPointers: string[] = [];
  for (const violation of refusal?.violations ?? []) {
    const row = enforcedRowOf(violation.pointer);
    if (!row) continue;
    rowPointers.push(violation.pointer);
    enforcedErrors[row.index] = {
      ...enforcedErrors[row.index],
      [row.part]: authSettingsViolation(i18n, violation),
    };
  }
  enforcedRows.forEach((_, index) => {
    const domain = rowErrors?.[index]?.domain?.message;
    const provider = rowErrors?.[index]?.provider?.message;
    if (domain || provider) enforcedErrors[index] = { ...enforcedErrors[index], domain, provider };
  });

  const duration = (field: DurationField, label: string, unitLabel: string, hint: string) => (
    <Controller
      control={form.control}
      name={field}
      render={({ field: control }) => (
        <Field label={label} hint={hint} error={errors[field]?.message ?? serverError(field)}>
          <DurationInput
            ref={control.ref}
            value={control.value}
            onChange={control.onChange}
            onBlur={control.onBlur}
            unitLabel={unitLabel}
          />
        </Field>
      )}
    />
  );

  return (
    <>
      <FormPage
        onSubmit={form.handleSubmit((valid) => save.mutate(settingsOf(valid)))}
        submitLabel={<Trans>Save</Trans>}
        pending={save.isPending}
        refusal={formRefusal(save.error, [...Object.values(pointerOf), ...rowPointers])}
      >
        {duration(
          "session_idle_timeout",
          t`Session idle timeout`,
          t`Unit of the session idle timeout`,
          t`A session ends this long after its last request.`,
        )}
        {duration(
          "session_absolute_timeout",
          t`Session absolute timeout`,
          t`Unit of the session absolute timeout`,
          t`A session ends this long after it opened, however much it is used. Lowering it ends the sessions already older.`,
        )}
        {duration(
          "token_max_lifetime",
          t`Maximum token lifetime`,
          t`Unit of the maximum token lifetime`,
          t`The furthest ahead a new API token may expire. Tokens created before keep their expiry.`,
        )}
        <Controller
          control={form.control}
          name="token_expiry_required"
          render={({ field }) => (
            <div className="flex items-start justify-between gap-4">
              <div className="flex flex-col gap-1">
                <Label htmlFor={expiryRequired}>
                  <Trans>Token expiry required</Trans>
                </Label>
                <p id={`${expiryRequired}-hint`} className="text-sm text-muted-foreground">
                  <Trans>Every new API token must have an expiry; none may live forever.</Trans>
                </p>
              </div>
              <Switch
                id={expiryRequired}
                aria-describedby={`${expiryRequired}-hint`}
                ref={field.ref}
                checked={field.value}
                onCheckedChange={field.onChange}
                onBlur={field.onBlur}
              />
            </div>
          )}
        />
        <Field
          label={<Trans>Password minimum length</Trans>}
          hint={
            <Trans>
              The fewest characters a password chosen from now on has, from 12 to 128. Passwords set
              before keep working.
            </Trans>
          }
          error={errors.password_min_length?.message ?? serverError("password_min_length")}
        >
          <Input
            inputMode="numeric"
            autoComplete="off"
            className="w-32"
            {...form.register("password_min_length")}
          />
        </Field>
        <Controller
          control={form.control}
          name="self_service_password_reset"
          render={({ field }) => (
            <div className="flex items-start justify-between gap-4">
              <div className="flex flex-col gap-1">
                <Label htmlFor={selfServiceReset}>
                  <Trans>Self-service password reset</Trans>
                </Label>
                <p id={`${selfServiceReset}-hint`} className="text-sm text-muted-foreground">
                  <Trans>
                    A member who forgot their password asks for a password reset by mail from the
                    sign-in page. It works only while an SMTP server is configured under Mail in the
                    instance navigation. An instance admin can issue a password reset link from a
                    member's page either way.
                  </Trans>
                </p>
              </div>
              <Switch
                id={selfServiceReset}
                aria-describedby={`${selfServiceReset}-hint`}
                ref={field.ref}
                checked={field.value}
                onCheckedChange={field.onChange}
                onBlur={field.onBlur}
              />
            </div>
          )}
        />
        <Controller
          control={form.control}
          name="local_login_enabled"
          render={({ field }) => (
            <SwitchField
              label={<Trans>Local login</Trans>}
              hint={
                <Trans>
                  Members sign in with their email and password. Switching it off leaves auth
                  providers as the only way in, so it is refused unless you hold an identity at an
                  enabled provider yourself. If a provider breaks and nobody can sign in, run
                  "flagnetic admin unlock" on the server: it switches local login back on and lifts
                  every enforced domain.
                </Trans>
              }
              checked={field.value}
              onCheckedChange={field.onChange}
            />
          )}
        />
        <Controller
          control={form.control}
          name="sso_enforced_domains"
          render={({ field }) =>
            providers.data ? (
              <EnforcedDomainsField
                value={field.value}
                onChange={field.onChange}
                providers={providers.data}
                errors={enforcedErrors}
              />
            ) : providers.isError ? (
              <QueryError error={providers.error} onRetry={() => void providers.refetch()} />
            ) : (
              <Skeleton aria-busy className="h-16 w-full" />
            )
          }
        />
      </FormPage>
      <UnsavedChangesBar
        dirty={isDirty}
        onDiscard={() => form.reset(undefined, { keepDirtyValues: false })}
        pending={save.isPending}
      />
    </>
  );
}
