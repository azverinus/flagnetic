import type { AuthProvider } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQueryClient } from "@tanstack/react-query";
import { Controller, useForm } from "react-hook-form";

import { FieldsConflictBanner } from "@/components/conflict-banner";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { SwitchField } from "@/components/ui/switch-field";
import { Textarea } from "@/components/ui/textarea";
import {
  authProviderApi,
  authProvidersKey,
  changedFieldNames,
  bodyOf,
  fieldsExplain,
  fieldViolation,
  formOf,
  type OidcProviderForm,
  oidcFormSchema,
  patchOf,
} from "@/lib/auth-providers";
import { ApiError } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { useFormConflict, useNewerArrival } from "@/lib/use-form-conflict";

export type AuthProviderFormProps = {
  /** The provider being edited; none for a new one. */
  provider?: AuthProvider;
  /** Whether the form is a dialog's content or a page's. */
  layout: "dialog" | "page";
  /** Whether a dialog's form takes the focus when it opens; default true. */
  autoFocus?: boolean;
  onCancel?: () => void;
  /** The provider as it is now. */
  onSaved: (saved: AuthProvider) => void;
};

/**
 * An OIDC auth provider: how the instance reaches the identity provider
 * (issuer, client ID, the client secret, which is write-only), what it
 * reads from the tokens, and which email domains it vouches for, with the
 * two switches that decide what a sign-in from those domains may do. The
 * server's refusal of a field is said under the field.
 */
export function AuthProviderForm({
  provider,
  layout,
  autoFocus = true,
  onCancel,
  onSaved,
}: AuthProviderFormProps) {
  const { i18n, t } = useLingui();
  const creating = provider === undefined;
  const queryClient = useQueryClient();
  const conflicts = useFormConflict({
    project: undefined,
    kind: "auth_provider",
    initial: provider,
    read: () => authProviderApi.get(provider?.slug ?? ""),
  });
  const { base, conflict } = conflicts;
  const form = useForm<OidcProviderForm>({
    resolver: zodResolver(oidcFormSchema(i18n, { creating, stored: base })),
    defaultValues: formOf(provider),
  });
  const save = useListMutation(
    ({ values, at }: { values: OidcProviderForm; at?: number }) =>
      base && provider
        ? authProviderApi.update(
            { slug: provider.slug, version: at ?? conflicts.version ?? provider.version },
            patchOf(base, values),
          )
        : authProviderApi.create(bodyOf(values)),
    authProvidersKey,
    (saved) => {
      // A save makes a new version, which the form starts over from.
      if (provider) {
        form.reset(formOf(saved));
        conflicts.restart(saved);
      }
      onSaved(saved);
    },
    // The variables carry the client secret.
    { secret: true },
  );
  // A newer version of the provider the screen came to hold (the header's
  // toggle, someone's save) starts a clean form over from it, and is
  // offered as a conflict to one with edits.
  useNewerArrival(
    provider,
    { version: conflicts.version, dirty: form.formState.isDirty, saving: save.isPending },
    {
      clean: (fresh) => {
        form.reset(formOf(fresh));
        conflicts.restart(fresh);
        save.reset();
      },
      dirty: (fresh) => void conflicts.offer(fresh),
    },
  );
  const { errors } = form.formState;
  const send = (at: number | undefined) =>
    form.handleSubmit((values) => {
      conflicts.clear();
      save.mutate({ values, at }, { onError: (error) => conflicts.meet(error) });
    });
  const submit = send(conflicts.version);
  const refusal = save.error instanceof ApiError ? save.error : undefined;
  // A slug already taken has no violation to point at: say it under the slug.
  const taken = refusal?.code === "already_exists";
  const error = (field: keyof OidcProviderForm) => errors[field]?.message;

  const fields = (
    <>
      {base && conflict ? (
        <FieldsConflictBanner
          entry={conflict.entry}
          names={changedFieldNames(i18n, base, conflict.current)}
          pending={save.isPending}
          onReload={() => {
            const { current } = conflict;
            form.reset(formOf(current));
            conflicts.restart(current);
            save.reset();
            void queryClient.invalidateQueries({ queryKey: authProvidersKey });
          }}
          onKeep={() => void send(conflicts.keep(conflict.current))()}
        />
      ) : null}
      <Field
        label={<Trans>Name</Trans>}
        hint={<Trans>What the sign-in button says: “Sign in with …”</Trans>}
        error={error("display_name") ?? fieldViolation(i18n, refusal, "display_name")}
      >
        <Input
          autoComplete="off"
          autoFocus={autoFocus && layout === "dialog"}
          {...form.register("display_name")}
        />
      </Field>
      <Field
        label={<Trans>Slug</Trans>}
        hint={
          creating ? (
            <Trans>
              Names the provider in sign-in addresses and in the redirect URI; it never changes
            </Trans>
          ) : (
            <Trans>The slug never changes</Trans>
          )
        }
        error={
          error("slug") ??
          fieldViolation(i18n, refusal, "slug") ??
          (taken ? t`A provider with this slug already exists.` : undefined)
        }
      >
        <Input
          autoComplete="off"
          spellCheck={false}
          readOnly={!creating}
          className={creating ? undefined : "bg-muted text-muted-foreground"}
          {...form.register("slug")}
        />
      </Field>
      <Field
        label={<Trans>Issuer</Trans>}
        hint={
          <Trans>
            The identity provider's issuer URL, from which its discovery document is read
          </Trans>
        }
        details={<Trans>Plain http is accepted only on a loopback address.</Trans>}
        error={error("issuer") ?? fieldViolation(i18n, refusal, "issuer")}
      >
        <Input type="url" autoComplete="off" spellCheck={false} {...form.register("issuer")} />
      </Field>
      <Field
        label={<Trans>Client ID</Trans>}
        error={error("client_id") ?? fieldViolation(i18n, refusal, "client_id")}
      >
        <Input autoComplete="off" spellCheck={false} {...form.register("client_id")} />
      </Field>
      <Field
        label={<Trans>Client secret</Trans>}
        hint={
          provider?.client_secret_set ? (
            <Trans>A secret is stored and never shown again; leave it empty to keep it</Trans>
          ) : (
            <Trans>Stored encrypted and never shown again</Trans>
          )
        }
        details={
          provider?.client_secret_set ? (
            <Trans>
              Type a new one to replace it. Changing the issuer or the client ID needs it again.
            </Trans>
          ) : undefined
        }
        error={error("client_secret") ?? fieldViolation(i18n, refusal, "client_secret")}
      >
        <Input
          type="password"
          autoComplete="new-password"
          spellCheck={false}
          {...form.register("client_secret")}
        />
      </Field>
      <Field
        label={<Trans>Domains</Trans>}
        hint={
          <Trans>
            The email domains this provider vouches for, one per line or separated by spaces
          </Trans>
        }
        details={
          <Trans>
            A subdomain is not included. Linking and creating members apply to these emails only.
          </Trans>
        }
        error={error("domains") ?? fieldViolation(i18n, refusal, "domains")}
      >
        <Textarea
          rows={3}
          autoComplete="off"
          spellCheck={false}
          placeholder="example.com"
          {...form.register("domains")}
        />
      </Field>
      <Field
        label={<Trans>Scopes</Trans>}
        hint={<Trans>Separated by spaces; the defaults read the email and the name</Trans>}
        error={error("scopes") ?? fieldViolation(i18n, refusal, "scopes")}
      >
        <Input autoComplete="off" spellCheck={false} {...form.register("scopes")} />
      </Field>
      <fieldset className="flex flex-col gap-4">
        <legend className="text-sm font-medium">
          <Trans>Claims</Trans>
        </legend>
        <p className="text-sm text-muted-foreground">
          <Trans>
            The names of the claims the instance reads from the identity provider's tokens.
          </Trans>
        </p>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field
            label={<Trans>Email claim</Trans>}
            error={error("claim_email") ?? fieldViolation(i18n, refusal, "claim_email")}
          >
            <Input autoComplete="off" spellCheck={false} {...form.register("claim_email")} />
          </Field>
          <Field
            label={<Trans>Name claim</Trans>}
            error={error("claim_name") ?? fieldViolation(i18n, refusal, "claim_name")}
          >
            <Input autoComplete="off" spellCheck={false} {...form.register("claim_name")} />
          </Field>
          <Field
            label={<Trans>Groups claim</Trans>}
            error={error("claim_groups") ?? fieldViolation(i18n, refusal, "claim_groups")}
          >
            <Input autoComplete="off" spellCheck={false} {...form.register("claim_groups")} />
          </Field>
        </div>
      </fieldset>
      <Controller
        control={form.control}
        name="require_verified_email"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Require a verified email</Trans>}
            hint={<Trans>Refuse a sign-in whose token does not say the email is verified</Trans>}
            details={
              <Trans>
                Turn it off only for a provider that does not send the claim and checks addresses
                itself.
              </Trans>
            }
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
      <Controller
        control={form.control}
        name="link_by_email"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Link members by email</Trans>}
            hint={
              <Trans>
                Link a first sign-in with a verified email from the domains above to the existing
                member with that email
              </Trans>
            }
            details={
              <Trans>
                Anyone who controls such an address at the provider then signs in as that member, so
                turn it on only for a provider you trust with these domains.
              </Trans>
            }
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
      <Controller
        control={form.control}
        name="auto_provision"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Create members on first sign-in</Trans>}
            hint={
              <Trans>
                Create an active member on a first sign-in with a verified email from the domains
                above
              </Trans>
            }
            details={<Trans>They hold no access until a project grants them a role.</Trans>}
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
      {creating ? (
        <Controller
          control={form.control}
          name="enabled"
          render={({ field }) => (
            <SwitchField
              label={<Trans>Enabled</Trans>}
              hint={
                <Trans>
                  Offer the provider on the sign-in page now; turn it off to test it first
                </Trans>
              }
              checked={field.value}
              onCheckedChange={field.onChange}
            />
          )}
        />
      ) : null}
    </>
  );

  // A violation a field says under itself is left out of the form's own.
  const formLevel = fieldsExplain(refusal) || taken ? undefined : conflicts.refusal(save.error);
  return layout === "dialog" ? (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel ?? (() => undefined)}
      submitLabel={<Trans>Add provider</Trans>}
      pending={save.isPending}
      refusal={formLevel}
    >
      {fields}
    </DialogForm>
  ) : (
    <FormPage
      onSubmit={submit}
      submitLabel={<Trans>Save</Trans>}
      pending={save.isPending}
      refusal={formLevel}
    >
      {fields}
    </FormPage>
  );
}
