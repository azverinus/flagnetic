import type { AuthProvider } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { Controller, useForm } from "react-hook-form";

import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { SwitchField } from "@/components/ui/switch-field";
import { Textarea } from "@/components/ui/textarea";
import {
  authProviderApi,
  authProvidersKey,
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
import { Button } from "@/components/ui/button";

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
  /** Reads the provider again after someone else saved first. */
  onReload?: () => void;
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
  onReload,
}: AuthProviderFormProps) {
  const { i18n, t } = useLingui();
  const creating = provider === undefined;
  const form = useForm<OidcProviderForm>({
    resolver: zodResolver(oidcFormSchema(i18n, { creating })),
    defaultValues: formOf(provider),
  });
  const save = useListMutation(
    (values: OidcProviderForm) =>
      provider
        ? authProviderApi.update(provider, patchOf(provider, values))
        : authProviderApi.create(bodyOf(values)),
    authProvidersKey,
    onSaved,
    // The variables carry the client secret.
    { secret: true },
  );
  const { errors } = form.formState;
  const submit = form.handleSubmit((values) => save.mutate(values));
  const refusal = save.error instanceof ApiError ? save.error : undefined;
  // A slug already taken has no violation to point at: say it under the slug.
  const taken = refusal?.code === "already_exists";
  const error = (field: keyof OidcProviderForm) => errors[field]?.message;

  const fields = (
    <>
      <Field
        label={<Trans>Name</Trans>}
        hint={<Trans>What the sign-in button says: “Sign in with …”.</Trans>}
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
              Names the provider in sign-in addresses and in the redirect URI. It never changes.
            </Trans>
          ) : (
            <Trans>The slug never changes.</Trans>
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
            The identity provider's issuer URL; its discovery document is read from it. Plain http
            is accepted only on a loopback address.
          </Trans>
        }
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
            <Trans>
              A secret is stored and never shown again. Leave this empty to keep it, or type a new
              one to replace it.
            </Trans>
          ) : (
            <Trans>Stored encrypted and never shown again.</Trans>
          )
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
            The email domains this provider vouches for, one per line or separated by spaces. A
            subdomain is not included. Linking and creating members apply to these emails only.
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
        hint={<Trans>Separated by spaces. The defaults read the email and the name.</Trans>}
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
            hint={
              <Trans>
                Refuse a sign-in whose token does not say the email is verified. Turn it off only
                for a provider that does not send the claim and checks addresses itself.
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
                A first sign-in whose verified email is in the domains above is linked to the
                existing member with that email. Anyone who controls such an address at the provider
                then signs in as that member, so turn it on only for a provider you trust with these
                domains.
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
                A sign-in whose verified email is in the domains above and has no member yet creates
                one, active, who holds no access until a project grants them a role.
              </Trans>
            }
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
                  Offer the provider on the sign-in page now. Turn it off to test it first.
                </Trans>
              }
              checked={field.value}
              onCheckedChange={field.onChange}
            />
          )}
        />
      ) : null}
      {refusal?.code === "version_mismatch" && onReload ? (
        <div>
          <Button type="button" variant="outline" onClick={onReload}>
            <Trans>Load their version</Trans>
          </Button>
        </div>
      ) : null}
    </>
  );

  // A violation a field says under itself is left out of the form's own.
  const formLevel = fieldsExplain(refusal) || taken ? undefined : save.error;
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
