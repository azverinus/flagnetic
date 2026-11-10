import {
  type AuthMethodProvider,
  linkMyIdentity,
  type LinkedIdentity,
  removeMyPassword,
  unlinkMyIdentity,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleAlertIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { IdentityList, UnlinkIdentityDialog } from "@/components/identity-list";
import { PasswordForm } from "@/components/password-form";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { FormRefusal } from "@/components/ui/form-page";
import { SettingsSection } from "@/components/ui/settings-section";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDateTime } from "@/i18n/format";
import { unwrap } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { formRefusal } from "@/lib/problem-messages";
import { authMethodsQuery, myIdentitiesQuery } from "@/lib/queries";
import { linkErrorMessage } from "@/lib/sso-errors";

/** Where linking returns the browser to: the page the member started it from. */
export const signInMethodsPath = "/settings/sign-in-methods";

export type SignInMethodsProps = {
  /** The code a failed linking sent the member back with. */
  error?: string;
  /** Sends the browser to the provider; replaced in tests, since a page cannot leave one. */
  navigate?: (url: string) => void;
};

/**
 * The member's ways in: whether they hold a password, which they may
 * change or remove, and the identities they linked at auth providers,
 * which they may unlink, with the providers they may still link. Removing
 * the password or an identity is refused while it is their last usable way
 * in, and the page says why rather than leaving it to a toast.
 */
export function SignInMethods({
  error,
  navigate = (url) => window.location.assign(url),
}: SignInMethodsProps) {
  const { i18n, t } = useLingui();
  const methods = useQuery(myIdentitiesQuery);
  const [unlinking, setUnlinking] = useState<LinkedIdentity>();

  if (methods.isError) {
    return <QueryError error={methods.error} onRetry={() => void methods.refetch()} />;
  }
  const password = methods.data?.password;
  const identities = methods.data?.identities;

  return (
    <div className="flex flex-col gap-10">
      {error ? (
        <p
          role="alert"
          className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
        >
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          {linkErrorMessage(i18n, error)}
        </p>
      ) : null}
      <SettingsSection
        title={<Trans>Password</Trans>}
        description={
          <Trans>
            Changing it signs out every other device you are signed in on; this one stays signed in.
          </Trans>
        }
      >
        {password === undefined ? (
          <Skeleton className="h-24 w-full" aria-busy />
        ) : password.set ? (
          <PasswordSet setAt={password.set_at} />
        ) : (
          <p className="text-sm text-muted-foreground">
            <Trans>
              You have no password, so you sign in through a provider below. To set one, use “Forgot
              your password?” on the sign-in page, or ask an instance admin for a reset link.
            </Trans>
          </p>
        )}
      </SettingsSection>
      <SettingsSection
        title={<Trans>Linked identities</Trans>}
        description={
          <Trans>
            An identity is the account an identity provider knows you by. Signing in through a
            provider you linked opens your account here; an identity at a disabled provider is no
            way in until the provider is enabled again.
          </Trans>
        }
      >
        <IdentityList
          identities={identities}
          caption={t`The identities you linked`}
          empty={<Trans>You have linked no identity.</Trans>}
          onUnlink={setUnlinking}
        />
      </SettingsSection>
      <SettingsSection
        title={<Trans>Link a provider</Trans>}
        description={
          <Trans>
            Sign in at the provider once to link the account you choose there, whatever its email.
            You come back to this page.
          </Trans>
        }
      >
        <LinkableProviders linked={identities} navigate={navigate} />
      </SettingsSection>
      <UnlinkIdentityDialog
        identity={unlinking}
        onClose={() => setUnlinking(undefined)}
        unlink={(identity) => unwrap(unlinkMyIdentity({ path: { identity: identity.id } }))}
        list={myIdentitiesQuery.queryKey}
      >
        {(identity) => {
          const name = identity.provider_name;
          return (
            <p>
              <Trans>
                You will no longer be able to sign in through {name} with this account. Your own
                account here, and everything in it, stays. You can link it again later.
              </Trans>
            </p>
          );
        }}
      </UnlinkIdentityDialog>
    </div>
  );
}

/** The password is set: when, how to change it, and how to remove it. */
function PasswordSet({ setAt }: { setAt?: string }) {
  const { i18n, t } = useLingui();
  const [removing, setRemoving] = useState(false);
  const remove = useListMutation(
    (_: void) => unwrap(removeMyPassword()),
    myIdentitiesQuery.queryKey,
    () => {
      setRemoving(false);
      toast.success(t`Your password is removed.`);
    },
  );
  const setOn = setAt ? formatDateTime(i18n, setAt) : "";
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {setAt ? (
            <Trans>
              Your password was set on <time dateTime={setAt}>{setOn}</time>.
            </Trans>
          ) : (
            <Trans>You have a password.</Trans>
          )}
        </p>
        <Button variant="outline" onClick={() => setRemoving(true)}>
          <Trans>Remove password</Trans>
        </Button>
      </div>
      <PasswordForm />
      <ConfirmDialog
        open={removing}
        onOpenChange={(open) => {
          if (!open) {
            remove.reset();
            setRemoving(false);
          }
        }}
        title={t`Remove your password`}
        confirmLabel={<Trans>Remove password</Trans>}
        destructive
        pending={remove.isPending}
        refusal={remove.error}
        onConfirm={() => remove.mutate()}
      >
        <p>
          <Trans>
            You will sign in only through the providers you linked. Any invitation link or password
            reset link still open for you stops working. This device and your other sessions stay
            signed in. It is refused while no linked provider is left that lets you in.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}

/**
 * A button for each enabled provider the member has not linked yet, which
 * asks the server to start linking and sends the browser where it says.
 */
function LinkableProviders({
  linked,
  navigate,
}: {
  linked: readonly LinkedIdentity[] | undefined;
  navigate: (url: string) => void;
}) {
  const { t } = useLingui();
  const methods = useQuery(authMethodsQuery);
  const link = useMutation({
    mutationFn: async (provider: AuthMethodProvider) =>
      unwrap(
        linkMyIdentity({
          path: { provider: provider.slug },
          body: { return_to: signInMethodsPath },
        }),
      ),
    meta: { toast: false, readsAuditAgain: false },
    onSuccess: ({ start_url }) => navigate(start_url),
  });

  if (methods.isError) {
    return <QueryError error={methods.error} onRetry={() => void methods.refetch()} />;
  }
  if (methods.data === undefined || linked === undefined) {
    return <Skeleton className="h-9 w-48" aria-busy />;
  }
  const held = new Set(linked.map((identity) => identity.provider));
  const providers = methods.data.providers.filter((provider) => !held.has(provider.slug));
  if (providers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {methods.data.providers.length === 0 ? (
          <Trans>No provider is enabled on this instance.</Trans>
        ) : (
          <Trans>You have linked every enabled provider.</Trans>
        )}
      </p>
    );
  }
  const refusal = formRefusal(link.error, []);
  return (
    <div className="flex flex-col gap-3">
      <ul aria-label={t`Providers you can link`} className="flex flex-wrap gap-2">
        {providers.map((provider) => {
          const name = provider.display_name;
          return (
            <li key={provider.slug}>
              <Button
                variant="outline"
                disabled={link.isPending}
                onClick={() => link.mutate(provider)}
              >
                <Trans>Link {name}</Trans>
              </Button>
            </li>
          );
        })}
      </ul>
      <FormRefusal refusal={refusal} />
    </div>
  );
}
