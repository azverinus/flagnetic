import type { AuthProvider, AuthProviderTest } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  CircleAlertIcon,
  CircleCheckIcon,
  PowerIcon,
  PowerOffIcon,
  StethoscopeIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";

import { AuthProviderKind, AuthProviderStateBadge } from "@/components/auth-provider-badges";
import { AuthProviderForm } from "@/components/auth-provider-form";
import { CopyableValue } from "@/components/copyable-value";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { authProviderApi, authProviderQuery, authProvidersKey } from "@/lib/auth-providers";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";
import { authMethodsQuery } from "@/lib/queries";

/** One auth provider's page, or why there is none. */
export function AuthProviderScreen({ slug }: { slug: string }) {
  const provider = useQuery(authProviderQuery(slug));
  if (provider.isError) {
    const missing = provider.error instanceof ApiError && provider.error.status === 404;
    return missing ? (
      <AuthProviderNotFound />
    ) : (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <QueryError error={provider.error} onRetry={() => void provider.refetch()} />
      </div>
    );
  }
  if (provider.data === undefined) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  return <AuthProviderPage provider={provider.data} onReload={() => void provider.refetch()} />;
}

function AuthProviderNotFound() {
  const navigate = useNavigate();
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Auth provider not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>It does not exist here, or it was deleted.</Trans>
      </p>
      <Button variant="outline" onClick={() => void navigate({ to: "/admin/auth-providers" })}>
        <Trans>Go to the auth providers</Trans>
      </Button>
    </div>
  );
}

function AuthProviderPage({
  provider,
  onReload,
}: {
  provider: AuthProvider;
  onReload: () => void;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState(false);
  const name = provider.display_name;

  /** Puts the provider a write answered in place of the one read. */
  const remember = (saved: AuthProvider) => {
    queryClient.setQueryData(authProviderQuery(saved.slug).queryKey, saved);
    void queryClient.invalidateQueries({ queryKey: authProvidersKey, exact: true });
    // The sign-in page offers what is enabled.
    void queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
  };
  const toggle = useMutation({
    mutationFn: () =>
      provider.enabled
        ? authProviderApi.disable(provider.slug)
        : authProviderApi.enable(provider.slug),
    onSuccess: (saved) => {
      remember(saved);
      toast.success(saved.enabled ? t`${name} is enabled.` : t`${name} is disabled.`);
    },
  });
  const test = useMutation({
    mutationFn: () => authProviderApi.test(provider.slug),
    meta: { toast: false },
  });
  const remove = useMutation({
    mutationFn: () => authProviderApi.remove(provider.slug),
    meta: { toast: false },
    onSuccess: async () => {
      // Leave the page before forgetting the provider: reading it again
      // while it is shown would answer 404 and say it was not found.
      await navigate({ to: "/admin/auth-providers" });
      queryClient.removeQueries({
        queryKey: authProviderQuery(provider.slug).queryKey,
        exact: true,
      });
      void queryClient.invalidateQueries({ queryKey: authProvidersKey, exact: true });
      void queryClient.invalidateQueries({ queryKey: authMethodsQuery.queryKey });
    },
  });

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <AuthProviderStateBadge provider={provider} />
            <AuthProviderKind kind={provider.kind} />
            <code className="font-mono text-xs">{provider.slug}</code>
          </span>
        }
        actions={
          <>
            <Button variant="outline" disabled={toggle.isPending} onClick={() => toggle.mutate()}>
              {provider.enabled ? <PowerOffIcon aria-hidden /> : <PowerIcon aria-hidden />}
              {provider.enabled ? <Trans>Disable</Trans> : <Trans>Enable</Trans>}
            </Button>
            <Button variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
              <StethoscopeIcon aria-hidden />
              {test.isPending ? <Trans>Testing…</Trans> : <Trans>Test</Trans>}
            </Button>
            <Button variant="outline" onClick={() => setDeleting(true)}>
              <Trash2Icon aria-hidden />
              <Trans>Delete</Trans>
            </Button>
          </>
        }
      />
      {provider.enabled ? null : (
        <p role="status" className="rounded-md border bg-muted p-3 text-sm">
          <Trans>
            This provider is disabled: it is not offered on the sign-in page, and its sign-ins are
            refused. Its members, identities and grants are kept.
          </Trans>
        </p>
      )}
      {provider.kind === "oidc" ? (
        <section aria-labelledby="register" className="flex flex-col gap-3">
          <h2 id="register" className="text-base font-semibold">
            <Trans>Register the instance at the identity provider</Trans>
          </h2>
          {provider.redirect_uri ? (
            <>
              <p className="text-sm text-muted-foreground">
                <Trans>
                  Add this as an allowed redirect (callback) URI of the client, with the client ID
                  and secret below.
                </Trans>
              </p>
              <CopyableValue label={t`Redirect URI`} value={provider.redirect_uri} />
            </>
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>
                The redirect URI is built from the instance's public address (FLAGNETIC_BASE_URL),
                which is not set. Set it, and the address shows here.
              </Trans>
            </p>
          )}
        </section>
      ) : null}
      <TestResult
        pending={test.isPending}
        result={test.data}
        failure={test.error instanceof ApiError ? test.error : undefined}
      />
      {provider.kind === "oidc" ? (
        <section aria-labelledby="settings" className="flex flex-col gap-4">
          <h2 id="settings" className="text-base font-semibold">
            <Trans>Settings</Trans>
          </h2>
          {/* A new version starts the form over from what was saved. */}
          <AuthProviderForm
            key={provider.version}
            provider={provider}
            layout="page"
            onSaved={(saved) => {
              remember(saved);
              // A test answered for the settings before the save; test again.
              test.reset();
              const { display_name: savedName } = saved;
              toast.success(t`${savedName} is saved.`);
            }}
            onReload={onReload}
          />
        </section>
      ) : null}
      <ConfirmDialog
        open={deleting}
        onOpenChange={(open) => {
          if (!open) {
            remove.reset();
            setDeleting(false);
          }
        }}
        title={t`Delete ${name}`}
        confirmLabel={<Trans>Delete provider</Trans>}
        destructive
        pending={remove.isPending}
        refusal={remove.error}
        onConfirm={() => remove.mutate()}
      >
        <p>
          <Trans>
            Members can no longer sign in through it. A provider that members have signed in through
            cannot be deleted; disable it instead. This cannot be undone.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}

/**
 * What the identity provider answered when the instance asked: the
 * endpoints its discovery document names, or why it could not be reached.
 * Sign-in needs PKCE with S256, so a provider that lists its methods
 * without it is flagged here, before a member meets the failure.
 */
function TestResult({
  pending,
  result,
  failure,
}: {
  pending: boolean;
  result: AuthProviderTest | undefined;
  failure: ApiError | undefined;
}) {
  const { i18n } = useLingui();
  if (pending) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        <Trans>Asking the identity provider…</Trans>
      </p>
    );
  }
  if (failure) {
    return (
      <p
        role="alert"
        className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
      >
        <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        {problemMessage(i18n, failure)}
      </p>
    );
  }
  if (!result) return null;
  const methods = result.code_challenge_methods_supported;
  const lacksS256 = methods !== undefined && !methods.includes("S256");
  return (
    <section aria-labelledby="test-result" className="flex flex-col gap-3">
      <h2 id="test-result" className="flex items-center gap-2 text-base font-semibold">
        <CircleCheckIcon aria-hidden className="size-4 text-primary" />
        <Trans>The identity provider answered</Trans>
      </h2>
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
        <Row term={<Trans>Issuer</Trans>} value={result.issuer} />
        <Row term={<Trans>Authorization endpoint</Trans>} value={result.authorization_endpoint} />
        <Row term={<Trans>Token endpoint</Trans>} value={result.token_endpoint} />
        <Row term={<Trans>Keys</Trans>} value={result.jwks_uri} />
        <Row term={<Trans>User info endpoint</Trans>} value={result.userinfo_endpoint} />
        <Row term={<Trans>Scopes it lists</Trans>} value={result.scopes_supported?.join(" ")} />
        <Row term={<Trans>PKCE methods</Trans>} value={methods?.join(" ")} />
      </dl>
      {lacksS256 ? (
        <p
          role="status"
          className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
        >
          <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <Trans>
            The provider does not list the S256 PKCE method, which sign-in needs. Sign-ins through
            it will fail.
          </Trans>
        </p>
      ) : null}
    </section>
  );
}

function Row({ term, value }: { term: ReactNode; value: string | undefined }) {
  if (value === undefined || value === "") return null;
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd>
        <code className="font-mono text-xs break-all">{value}</code>
      </dd>
    </>
  );
}
