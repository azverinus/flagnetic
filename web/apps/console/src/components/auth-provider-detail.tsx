import type { AuthProvider, AuthProviderTest } from "@flagnetic/api";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Page } from "@/components/ui/page";
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
import { GroupMappingsSection } from "@/components/group-mappings";
import { QueryError } from "@/components/query-error";
import {
  IdPCertificates,
  SamlCertificates,
  SamlIdentityProvider,
  SamlRegistration,
} from "@/components/saml-provider-detail";
import { SamlProviderForm } from "@/components/saml-provider-form";
import { ScimGroups } from "@/components/scim-groups";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { PageHeader, PageTitle } from "@/components/ui/page-header";
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
      <Page>
        <QueryError error={provider.error} onRetry={() => void provider.refetch()} />
      </Page>
    );
  }
  if (provider.data === undefined) {
    return (
      <Page aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </Page>
    );
  }
  return <AuthProviderPage provider={provider.data} onReload={() => void provider.refetch()} />;
}

function AuthProviderNotFound() {
  const navigate = useNavigate();
  return (
    <Page measure="form">
      <PageTitle>
        <Trans>Auth provider not found</Trans>
      </PageTitle>
      <p className="text-sm text-muted-foreground">
        <Trans>It does not exist here, or it was deleted.</Trans>
      </p>
      <Button
        className="self-start"
        variant="outline"
        onClick={() => void navigate({ to: "/admin/auth-providers" })}
      >
        <Trans>Go to the auth providers</Trans>
      </Button>
    </Page>
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
  const settingsSaved = (answer: AuthProvider) => {
    remember(answer);
    // A test answered for the settings before the save; test again.
    test.reset();
    const { display_name: savedName } = answer;
    toast.success(t`${savedName} is saved.`);
  };
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
    <Page measure="form">
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
          </>
        }
        more={
          <DropdownMenuItem
            className="text-destructive focus:text-destructive"
            onSelect={() => setDeleting(true)}
          >
            <Trash2Icon aria-hidden />
            <Trans>Delete</Trans>
          </DropdownMenuItem>
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
        <Card aria-labelledby="register">
          <CardHeader
            titleId="register"
            title={<Trans>Register the instance at the identity provider</Trans>}
          />
          <CardContent>
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
          </CardContent>
        </Card>
      ) : (
        <SamlRegistration provider={provider} />
      )}
      <TestResult
        pending={test.isPending}
        result={test.data}
        failure={test.error instanceof ApiError ? test.error : undefined}
      />
      {provider.kind === "saml" ? (
        <>
          <SamlIdentityProvider provider={provider} />
          <SamlCertificates provider={provider} onChanged={remember} onReload={onReload} />
        </>
      ) : null}
      <ScimGroups provider={provider.slug} />
      <Card aria-labelledby="settings">
        <CardHeader titleId="settings" title={<Trans>Settings</Trans>} />
        <CardContent>
          {/* A new version starts the form over from what was saved. */}
          {provider.kind === "oidc" ? (
            <AuthProviderForm
              key={provider.version}
              provider={provider}
              layout="page"
              onSaved={settingsSaved}
              onReload={onReload}
            />
          ) : (
            <SamlProviderForm
              key={provider.version}
              provider={provider}
              layout="page"
              onSaved={settingsSaved}
              onReload={onReload}
            />
          )}
        </CardContent>
      </Card>
      <GroupMappingsSection provider={provider.slug} />
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
            Members can no longer sign in through it. A provider that members have signed in
            through, or that granted access through its group mappings, cannot be deleted; disable
            it instead. This cannot be undone.
          </Trans>
        </p>
      </ConfirmDialog>
    </Page>
  );
}

/**
 * What the identity provider answered when the instance asked: the
 * endpoints its discovery document names, or what its SAML metadata says
 * now, or why it could not be reached.
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
  if (result.idp_entity_id !== undefined) {
    return (
      <Card aria-labelledby="test-result">
        <CardHeader
          titleId="test-result"
          title={
            <span className="flex items-center gap-2">
              <CircleCheckIcon aria-hidden className="size-4 text-primary-ink" />
              <Trans>The identity provider answered</Trans>
            </span>
          }
        />
        <CardContent>
          <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
            <Row term={<Trans>Entity ID</Trans>} value={result.idp_entity_id} />
            <Row term={<Trans>Sign-on URL</Trans>} value={result.idp_sso_url} />
            <dt className="text-muted-foreground">
              <Trans>Signing certificates</Trans>
            </dt>
            <dd>
              <IdPCertificates certificates={result.idp_certificates ?? []} />
            </dd>
          </dl>
        </CardContent>
      </Card>
    );
  }
  const methods = result.code_challenge_methods_supported;
  const lacksS256 = methods !== undefined && !methods.includes("S256");
  return (
    <Card aria-labelledby="test-result">
      <CardHeader
        titleId="test-result"
        title={
          <span className="flex items-center gap-2">
            <CircleCheckIcon aria-hidden className="size-4 text-primary-ink" />
            <Trans>The identity provider answered</Trans>
          </span>
        }
      />
      <CardContent>
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
      </CardContent>
    </Card>
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
