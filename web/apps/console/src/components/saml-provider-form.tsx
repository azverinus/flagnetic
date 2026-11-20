import type { AuthProvider } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQueryClient } from "@tanstack/react-query";
import { Controller, useForm, useWatch } from "react-hook-form";

import { FieldsConflictBanner } from "@/components/conflict-banner";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { SwitchField } from "@/components/ui/switch-field";
import { Textarea } from "@/components/ui/textarea";
import { authProviderApi, authProvidersKey, changedFieldNames } from "@/lib/auth-providers";
import { ApiError } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { useFormConflict, useNewerArrival } from "@/lib/use-form-conflict";
import {
  type MetadataSource,
  pastedMetadata,
  samlBodyOf,
  samlFieldsExplain,
  samlFieldViolation,
  samlFormOf,
  samlFormSchema,
  samlPatchOf,
  type SamlProviderForm as SamlForm,
} from "@/lib/saml-providers";

export type SamlProviderFormProps = {
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
 * A SAML auth provider: where the identity provider's metadata comes from
 * (a URL the instance fetches and refreshes daily, or a pasted document,
 * which is write-only), how the instance asks for a sign-in, which
 * attributes it reads, and which email domains the provider vouches for.
 * The server's refusal of a field is said under the field.
 */
export function SamlProviderForm({
  provider,
  layout,
  autoFocus = true,
  onCancel,
  onSaved,
}: SamlProviderFormProps) {
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
  const form = useForm<SamlForm>({
    resolver: zodResolver(samlFormSchema(i18n, { creating, stored: base })),
    defaultValues: samlFormOf(provider),
  });
  const save = useListMutation(
    ({ values, at }: { values: SamlForm; at?: number }) =>
      base && provider
        ? authProviderApi.update(
            { slug: provider.slug, version: at ?? conflicts.version ?? provider.version },
            samlPatchOf(base, values),
          )
        : authProviderApi.create(samlBodyOf(values)),
    authProvidersKey,
    (saved) => {
      // A save makes a new version, which the form starts over from.
      if (provider) {
        form.reset(samlFormOf(saved));
        conflicts.restart(saved);
      }
      onSaved(saved);
    },
  );
  // A newer version of the provider the screen came to hold (the header's
  // toggle, someone's save) starts a clean form over from it, and is
  // offered as a conflict to one with edits.
  useNewerArrival(
    provider,
    { version: conflicts.version, dirty: form.formState.isDirty, saving: save.isPending },
    {
      clean: (fresh) => {
        form.reset(samlFormOf(fresh));
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
  const error = (field: keyof SamlForm) => errors[field]?.message;
  const source = useWatch({ control: form.control, name: "metadata_source" });
  const keepsDocument = provider !== undefined && pastedMetadata(provider);

  const fields = (
    <>
      {base && conflict ? (
        <FieldsConflictBanner
          entry={conflict.entry}
          names={changedFieldNames(i18n, base, conflict.current)}
          pending={save.isPending}
          onReload={() => {
            const { current } = conflict;
            form.reset(samlFormOf(current));
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
        error={error("display_name") ?? samlFieldViolation(i18n, refusal, "display_name")}
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
              Names the provider in sign-in addresses, in the ACS URL and in the entity ID; it never
              changes
            </Trans>
          ) : (
            <Trans>The slug never changes</Trans>
          )
        }
        error={
          error("slug") ??
          samlFieldViolation(i18n, refusal, "slug") ??
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
      <Controller
        control={form.control}
        name="metadata_source"
        render={({ field }) => (
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">
              <Trans>Identity provider metadata</Trans>
            </legend>
            <p className="text-sm text-muted-foreground">
              <Trans>
                The instance reads the identity provider's entity ID, sign-on address and signing
                certificates from it. Metadata fetched from a URL is read again every day, so the
                identity provider's certificate rotations reach the instance; pasted metadata is
                never read again.
              </Trans>
            </p>
            {(
              [
                ["url", t`Fetch from a URL`],
                ["document", t`Paste the metadata`],
              ] as const satisfies readonly [MetadataSource, string][]
            ).map(([value, label]) => (
              <label key={value} className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name={field.name}
                  value={value}
                  checked={field.value === value}
                  onChange={() => field.onChange(value)}
                  onBlur={field.onBlur}
                  className="size-4 accent-primary"
                />
                {label}
              </label>
            ))}
          </fieldset>
        )}
      />
      {source === "url" ? (
        <Field
          label={<Trans>Metadata URL</Trans>}
          hint={<Trans>Where the identity provider publishes its metadata</Trans>}
          details={<Trans>Plain http is accepted only on a loopback address.</Trans>}
          error={error("idp_metadata_url") ?? samlFieldViolation(i18n, refusal, "idp_metadata_url")}
        >
          <Input
            type="url"
            autoComplete="off"
            spellCheck={false}
            {...form.register("idp_metadata_url")}
          />
        </Field>
      ) : (
        <Field
          label={<Trans>Metadata</Trans>}
          hint={
            keepsDocument ? (
              <Trans>
                The pasted metadata is stored and not shown again; leave it empty to keep it
              </Trans>
            ) : (
              <Trans>The identity provider's metadata document, at most 1 MiB</Trans>
            )
          }
          details={keepsDocument ? <Trans>Paste a new document to replace it.</Trans> : undefined}
          error={error("idp_metadata_xml") ?? samlFieldViolation(i18n, refusal, "idp_metadata_xml")}
        >
          <Textarea
            rows={6}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            {...form.register("idp_metadata_xml")}
          />
        </Field>
      )}
      <Field
        label={<Trans>Domains</Trans>}
        hint={
          <Trans>
            The email domains this provider vouches for, one per line or separated by spaces
          </Trans>
        }
        details={
          <Trans>
            A subdomain is not included. An email counts as verified, and linking and creating
            members apply, only inside these domains.
          </Trans>
        }
        error={error("domains") ?? samlFieldViolation(i18n, refusal, "domains")}
      >
        <Textarea
          rows={3}
          autoComplete="off"
          spellCheck={false}
          placeholder="example.com"
          {...form.register("domains")}
        />
      </Field>
      <Controller
        control={form.control}
        name="name_id_format"
        render={({ field }) => (
          <Field
            label={<Trans>Name identifier format</Trans>}
            hint={
              <Trans>The name identifier format the instance asks the identity provider for</Trans>
            }
            details={
              <Trans>
                The name identifier links the person to their member; with the email address format
                it is also their email.
              </Trans>
            }
            error={error("name_id_format") ?? samlFieldViolation(i18n, refusal, "name_id_format")}
          >
            <Select
              ref={field.ref}
              value={field.value}
              onValueChange={field.onChange}
              onBlur={field.onBlur}
            >
              <SelectItem value="unspecified">{t`Unspecified: the identity provider chooses`}</SelectItem>
              <SelectItem value="emailAddress">{t`Email address`}</SelectItem>
              <SelectItem value="persistent">{t`Persistent: the same at every sign-in`}</SelectItem>
              <SelectItem value="transient">
                {t`Transient: new at every sign-in, so nothing stays linked`}
              </SelectItem>
            </Select>
          </Field>
        )}
      />
      <fieldset className="flex flex-col gap-4">
        <legend className="text-sm font-medium">
          <Trans>Attributes</Trans>
        </legend>
        <p className="text-sm text-muted-foreground">
          <Trans>
            The names of the assertion attributes the instance reads, matched by name or friendly
            name. The email is the name identifier when its format is email address.
          </Trans>
        </p>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field
            label={<Trans>Email attribute</Trans>}
            error={error("claim_email") ?? samlFieldViolation(i18n, refusal, "claim_email")}
          >
            <Input autoComplete="off" spellCheck={false} {...form.register("claim_email")} />
          </Field>
          <Field
            label={<Trans>Name attribute</Trans>}
            error={error("claim_name") ?? samlFieldViolation(i18n, refusal, "claim_name")}
          >
            <Input autoComplete="off" spellCheck={false} {...form.register("claim_name")} />
          </Field>
          <Field
            label={<Trans>Groups attribute</Trans>}
            error={error("claim_groups") ?? samlFieldViolation(i18n, refusal, "claim_groups")}
          >
            <Input autoComplete="off" spellCheck={false} {...form.register("claim_groups")} />
          </Field>
        </div>
      </fieldset>
      <Controller
        control={form.control}
        name="sign_requests"
        render={({ field }) => (
          <SwitchField
            label={<Trans>Sign authentication requests</Trans>}
            hint={<Trans>Sign the requests the instance sends with its current key</Trans>}
            details={
              <Trans>
                For an identity provider that checks them against the certificate in the SP
                metadata.
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
                Link a first sign-in from the domains above to the existing member with that email
              </Trans>
            }
            details={
              <Trans>
                Anyone who controls such an address at the identity provider then signs in as that
                member, so turn it on only for a provider you trust with these domains.
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
            hint={<Trans>Create an active member on a first sign-in from the domains above</Trans>}
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
  const formLevel = samlFieldsExplain(refusal) || taken ? undefined : conflicts.refusal(save.error);
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
