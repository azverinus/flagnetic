import type { AuthProvider, ScimToken } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { PlusIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { CopyableValue } from "@/components/copyable-value";
import { QueryError } from "@/components/query-error";
import { RowAction, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { PageHeader } from "@/components/ui/page-header";
import { RevealSecretDialog } from "@/components/ui/reveal-secret";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime, formatLifetime } from "@/i18n/format";
import { authProvidersQuery } from "@/lib/auth-providers";
import { useListMutation } from "@/lib/mutations";
import { type ExpiryChoice, expiresAt } from "@/lib/personal-tokens";
import {
  scimBaseUrl,
  scimExpiryChoices,
  scimTokenApi,
  type ScimTokenState,
  scimTokenState,
  scimTokensKey,
  scimTokensQuery,
} from "@/lib/scim";
import { useNowPassing } from "@/lib/use-now";

/**
 * The instance's SCIM tokens: what an identity provider presents to
 * provision members, each bound to one auth provider for life. The address
 * the identity provider is told is shown to copy; a token's value is shown
 * once, when it is created, and afterwards by its hint.
 */
export function ScimTokensScreen() {
  const { t } = useLingui();
  const tokens = useQuery(scimTokensQuery);
  const providers = useQuery(authProvidersQuery);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<{ value: string }>();
  const [revoking, setRevoking] = useState<ScimToken>();

  const names = useMemo(
    () => new Map((providers.data ?? []).map((p) => [p.slug, p.display_name])),
    [providers.data],
  );
  const now = useNowPassing(
    (tokens.data ?? []).filter((token) => !token.revoked_at).map((token) => token.expires_at),
  );
  const revoke = useListMutation(
    (token: ScimToken) => scimTokenApi.revoke(token.id),
    scimTokensKey,
    () => {
      setRevoking(undefined);
      toast.success(t`The token is revoked.`);
    },
  );

  const tokenColumns = useScimTokenColumns(now, names);
  const columns = useMemo<TableColumn<ScimToken>[]>(
    () =>
      withRowActions(tokenColumns, true, (token) => {
        if (token.revoked_at) return null;
        const { hint } = token;
        return (
          <RowAction
            label={t`Revoke ${hint}`}
            icon={Trash2Icon}
            onClick={() => setRevoking(token)}
          />
        );
      }),
    [t, tokenColumns],
  );

  const revokingHint = revoking?.hint ?? "";
  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`SCIM tokens`}
        description={
          <Trans>
            What an identity provider presents to create, update and deprovision members through
            SCIM. A token belongs to one auth provider for life and reaches only that provider's
            members and groups.
          </Trans>
        }
        actions={
          <Button onClick={() => setCreating(true)}>
            <PlusIcon aria-hidden />
            <Trans>New SCIM token</Trans>
          </Button>
        }
      />
      <section aria-labelledby="scim-base-url" className="flex flex-col gap-2">
        <h2 id="scim-base-url" className="text-base font-semibold">
          <Trans>Tell the identity provider</Trans>
        </h2>
        <p className="text-sm text-muted-foreground">
          <Trans>
            Enter this as the tenant or connector base URL, with a token as the secret or bearer
            token.
          </Trans>
        </p>
        <CopyableValue label={t`SCIM base URL`} value={scimBaseUrl(window.location.origin)} />
      </section>
      {tokens.isError ? (
        <QueryError error={tokens.error} onRetry={() => void tokens.refetch()} />
      ) : (
        <Table
          caption={t`SCIM tokens`}
          hideCaption
          columns={columns}
          data={tokens.data ?? []}
          loading={tokens.isPending}
          empty={<Trans>No SCIM token yet.</Trans>}
          getRowId={(token) => token.id}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New SCIM token`}>
        <ScimTokenForm
          providers={providers.data}
          providersError={providers.error}
          onRetry={() => void providers.refetch()}
          onCancel={() => setCreating(false)}
          onCreated={(token) => {
            setCreating(false);
            setCreated(token);
          }}
        />
      </Dialog>
      <RevealSecretDialog
        open={created !== undefined}
        title={t`Your new SCIM token`}
        value={created?.value ?? ""}
        kept="hash"
        onClose={() => setCreated(undefined)}
      >
        <Trans>
          Enter it in the identity provider as the secret token or bearer token of the connection.
          Keep it where only the identity provider can read it.
        </Trans>
      </RevealSecretDialog>
      <ConfirmDialog
        open={revoking !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            revoke.reset();
            setRevoking(undefined);
          }
        }}
        title={t`Revoke ${revokingHint}`}
        confirmLabel={<Trans>Revoke token</Trans>}
        destructive
        pending={revoke.isPending}
        refusal={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking)}
      >
        <p>
          <Trans>
            The identity provider is refused from now on, and stops provisioning until it is given
            another token. Members and groups it already provisioned are kept. This cannot be
            undone.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}

/**
 * The columns of the list of SCIM tokens: the token by its hint, its
 * provider, its expiry, last use and creation, and where it is in its life
 * at now.
 */
function useScimTokenColumns(
  now: Date,
  providerNames: ReadonlyMap<string, string>,
): TableColumn<ScimToken>[] {
  const { t } = useLingui();
  return useMemo(() => {
    const stateName: Record<ScimTokenState, string> = {
      active: t`Active`,
      expired: t`Expired`,
      revoked: t`Revoked`,
    };
    return [
      {
        id: "token",
        header: t`Token`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.hint}</code>,
      },
      {
        id: "provider",
        header: t`Provider`,
        cell: ({ row }) => {
          const { provider } = row.original;
          return (
            <span className="flex flex-col">
              <Link
                to="/admin/auth-providers/$provider"
                params={{ provider }}
                className="font-medium hover:underline"
              >
                {providerNames.get(provider) ?? provider}
              </Link>
              <code className="font-mono text-xs text-muted-foreground">{provider}</code>
            </span>
          );
        },
      },
      {
        id: "expires",
        header: t`Expires`,
        cell: ({ row }) =>
          row.original.expires_at ? <ScimDate value={row.original.expires_at} /> : t`Never`,
      },
      {
        id: "used",
        header: t`Last used`,
        cell: ({ row }) =>
          row.original.last_used_at ? <ScimDate value={row.original.last_used_at} /> : t`Never`,
      },
      {
        id: "created",
        header: t`Created`,
        cell: ({ row }) => <ScimDate value={row.original.created_at} />,
      },
      {
        id: "state",
        header: t`State`,
        cell: ({ row }) => stateName[scimTokenState(row.original, now)],
      },
    ];
  }, [now, providerNames, t]);
}

function ScimDate({ value }: { value: string }) {
  const { i18n } = useLingui();
  return <time dateTime={value}>{formatDateTime(i18n, value)}</time>;
}

function ScimTokenForm({
  providers,
  providersError,
  onRetry,
  onCancel,
  onCreated,
}: {
  providers: AuthProvider[] | undefined;
  providersError: unknown;
  onRetry: () => void;
  onCancel: () => void;
  onCreated: (created: { value: string }) => void;
}) {
  const { t } = useLingui();
  const schema = z.object({
    provider: z.string().min(1, t`Choose an auth provider.`),
    expiry: z.string(),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { provider: "", expiry: "never" },
  });
  const create = useListMutation(
    (values: Values) =>
      scimTokenApi.create({
        provider: values.provider,
        expires_at: expiresAt(values.expiry as ExpiryChoice, new Date(), { expiryRequired: false }),
      }),
    scimTokensKey,
    () => undefined,
    // The answer carries the token's value.
    { secret: true },
  );
  const { errors } = form.formState;
  const { i18n } = useLingui();
  const days = (n: number) => formatLifetime(i18n, n * 86_400);
  const expiryName: Record<ExpiryChoice, string> = {
    "7": days(7),
    "30": days(30),
    "90": days(90),
    "365": t`A year`,
    never: t`Never`,
  };

  if (providersError) {
    return (
      <div className="flex flex-col gap-4">
        <QueryError error={providersError} onRetry={onRetry} />
        <Button variant="outline" onClick={onCancel}>
          <Trans>Cancel</Trans>
        </Button>
      </div>
    );
  }
  if (providers !== undefined && providers.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <p className="text-sm">
          <Trans>
            A SCIM token belongs to an auth provider, and there is none yet. Add the provider of
            your identity provider first.
          </Trans>
        </p>
        <Button asChild variant="outline">
          <Link to="/admin/auth-providers">
            <Trans>Go to the auth providers</Trans>
          </Link>
        </Button>
      </div>
    );
  }

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => create.mutate(values, { onSuccess: onCreated }))}
      onCancel={onCancel}
      submitLabel={<Trans>Create token</Trans>}
      pending={create.isPending}
      refusal={create.error}
    >
      <Controller
        control={form.control}
        name="provider"
        render={({ field }) => (
          <Field
            label={<Trans>Auth provider</Trans>}
            hint={
              <Trans>
                The provider whose members and groups the token reaches. It cannot be changed later.
              </Trans>
            }
            error={errors.provider?.message}
            refusal={create.error}
            pointer="/provider"
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
              placeholder={t`Choose a provider`}
            >
              {(providers ?? []).map((provider) => {
                const { display_name: name } = provider;
                return (
                  <SelectItem key={provider.slug} value={provider.slug}>
                    {provider.enabled ? name : t`${name} (disabled)`}
                  </SelectItem>
                );
              })}
            </Select>
          </Field>
        )}
      />
      <Controller
        control={form.control}
        name="expiry"
        render={({ field }) => (
          <Field
            label={<Trans>Expires</Trans>}
            hint={
              <Trans>After this the identity provider is refused until it gets a new token.</Trans>
            }
            refusal={create.error}
            pointer="/expires_at"
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
            >
              {scimExpiryChoices.map((choice) => (
                <SelectItem key={choice} value={choice}>
                  {expiryName[choice]}
                </SelectItem>
              ))}
            </Select>
          </Field>
        )}
      />
    </DialogForm>
  );
}
