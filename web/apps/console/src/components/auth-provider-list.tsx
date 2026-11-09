import type { AuthProvider } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { PlusIcon, PowerIcon, PowerOffIcon, ShieldCheckIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { AuthProviderKind, AuthProviderStateBadge } from "@/components/auth-provider-badges";
import { QueryError } from "@/components/query-error";
import { NewAuthProvider } from "@/components/new-auth-provider";
import { DeleteRowDialog, RowAction, withRowActions } from "@/components/row-dialogs";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { authMethodsQuery } from "@/lib/queries";
import { authProviderApi, authProvidersKey, authProvidersQuery } from "@/lib/auth-providers";

/**
 * The instance's auth providers: which protocol each speaks, whether it is
 * offered on the sign-in page and the email domains it vouches for. Adding
 * an OIDC or a SAML provider, enabling and disabling one in a click, and
 * deleting one that nothing depends on.
 */
export function AuthProviderList() {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const providers = useQuery(authProvidersQuery);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<string>();

  const { mutate: toggle } = useMutation({
    mutationFn: (provider: AuthProvider) =>
      provider.enabled
        ? authProviderApi.disable(provider.slug)
        : authProviderApi.enable(provider.slug),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: authProvidersKey });
      // The sign-in page offers what is enabled.
      await queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
      const { display_name: name } = saved;
      toast.success(saved.enabled ? t`${name} is enabled.` : t`${name} is disabled.`);
    },
  });

  const columns = useMemo<TableColumn<AuthProvider>[]>(() => {
    const base: TableColumn<AuthProvider>[] = [
      {
        id: "name",
        header: t`Name`,
        cell: ({ row }) => (
          <span className="flex flex-col gap-1">
            <Link
              to="/admin/auth-providers/$provider"
              params={{ provider: row.original.slug }}
              className="font-medium hover:underline"
            >
              {row.original.display_name}
            </Link>
            <span>
              <AuthProviderStateBadge provider={row.original} />
            </span>
          </span>
        ),
      },
      {
        id: "kind",
        header: t`Kind`,
        cell: ({ row }) => <AuthProviderKind kind={row.original.kind} />,
      },
      {
        id: "slug",
        header: t`Slug`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.slug}</code>,
      },
      {
        id: "domains",
        header: t`Domains`,
        cell: ({ row }) => <span className="break-all">{row.original.domains.join(", ")}</span>,
      },
    ];
    return withRowActions(base, true, (provider) => {
      const { display_name: name } = provider;
      return (
        <>
          <RowAction
            label={provider.enabled ? t`Disable ${name}` : t`Enable ${name}`}
            icon={provider.enabled ? PowerOffIcon : PowerIcon}
            onClick={() => toggle(provider)}
          />
          <RowAction
            label={t`Delete ${name}`}
            icon={Trash2Icon}
            onClick={() => setDeleting(provider.slug)}
          />
        </>
      );
    });
  }, [t, toggle]);

  const nameOf = (slug: string) =>
    providers.data?.find((provider) => provider.slug === slug)?.display_name ?? slug;

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Auth providers`}
        description={
          <Trans>
            The identity providers members sign in through. Each is offered on the sign-in page and
            vouches only for the email domains you give it.
          </Trans>
        }
        actions={
          <Button onClick={() => setCreating(true)}>
            <PlusIcon aria-hidden />
            <Trans>New provider</Trans>
          </Button>
        }
      />
      {providers.isError ? (
        <QueryError error={providers.error} onRetry={() => void providers.refetch()} />
      ) : providers.data?.length === 0 ? (
        <EmptyState
          icon={<ShieldCheckIcon />}
          title={<Trans>No auth provider yet.</Trans>}
          description={
            <Trans>
              Add one to let members sign in through your company's identity provider. Members keep
              signing in with their password until you do.
            </Trans>
          }
        />
      ) : (
        <Table
          caption={t`Auth providers`}
          hideCaption
          columns={columns}
          data={providers.data ?? []}
          loading={providers.isPending}
          getRowId={(provider) => provider.slug}
        />
      )}

      <Dialog open={creating} onOpenChange={setCreating} title={t`New auth provider`}>
        <NewAuthProvider
          onCancel={() => setCreating(false)}
          onSaved={(saved) => {
            setCreating(false);
            void queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
            // Its page is where the addresses to register are.
            void navigate({
              to: "/admin/auth-providers/$provider",
              params: { provider: saved.slug },
            });
          }}
        />
      </Dialog>
      <DeleteRowDialog
        rowKey={deleting}
        onDone={() => {
          setDeleting(undefined);
          void queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
        }}
        title={(slug) => {
          const name = nameOf(slug);
          return t`Delete ${name}`;
        }}
        confirmLabel={<Trans>Delete provider</Trans>}
        remove={(slug) => authProviderApi.remove(slug)}
        list={authProvidersKey}
      >
        {() => (
          <p>
            <Trans>
              Members can no longer sign in through it. A provider that members have signed in
              through cannot be deleted; disable it instead. This cannot be undone.
            </Trans>
          </p>
        )}
      </DeleteRowDialog>
    </div>
  );
}
