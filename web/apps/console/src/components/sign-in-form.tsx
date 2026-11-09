import { signIn, type Me, zSignInRequest } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleAlertIcon, InfoIcon } from "lucide-react";
import { useId, useMemo } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { signInStartPath } from "@/lib/auth-providers";
import { ApiError, unwrap } from "@/lib/errors";
import { problemMessage, retryWait } from "@/lib/problem-messages";
import { authMethodsQuery } from "@/lib/queries";
import { forgetsSecret } from "@/lib/mutations";
import { ssoErrorMessage } from "@/lib/sso-errors";

export type SignInFormProps = {
  /** Set when the member was sent here because their session ended. */
  ended?: boolean;
  /** Where to go once signed in, kept through a provider's sign-in. */
  returnTo?: string;
  /** The code a failed single sign-on sent the member back with. */
  error?: string;
  /** The slug of the provider the error names. */
  errorProvider?: string;
  /** Called with the caller once the session is open. */
  onSignedIn: (me: Me) => void;
};

/**
 * The ways to sign in: email and password when the instance allows them,
 * with the forgotten-password page when the instance mails resets, and a
 * link to each enabled auth provider's sign-in. The reason for a
 * refusal, or for a single sign-on that failed, is given in the member's
 * language. Every refusal the server gives is explained here rather than
 * toasted, because the member has nothing else on the page.
 */
export function SignInForm({ ended, returnTo, error, errorProvider, onSignedIn }: SignInFormProps) {
  const { i18n, t } = useLingui();
  const methods = useQuery(authMethodsQuery);
  const refusalId = useId();

  // The generated schema gives the shape the server reads; the console adds
  // what a member must fill in, in their language.
  const schema = useMemo(
    () =>
      zSignInRequest.extend({
        email: z
          .string()
          .trim()
          .min(1, t(msg`Enter your email.`)),
        password: z.string().min(1, t(msg`Enter your password.`)),
      }),
    [t],
  );
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: { email: "", password: "" },
  });
  const submit = useMutation({
    mutationFn: (body: z.infer<typeof schema>) => unwrap(signIn({ body })),
    // The password is in the variables.
    ...forgetsSecret,
    meta: { toast: false, readsAuditAgain: false },
    onSuccess: onSignedIn,
  });

  const providers = methods.data?.providers ?? [];
  const localLogin = methods.data?.local_login_enabled ?? true;
  if (methods.data && !localLogin && providers.length === 0) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        <Trans>No way to sign in is enabled on this instance. Ask its operator.</Trans>
      </p>
    );
  }

  const refusal = submit.error instanceof ApiError ? submit.error : undefined;
  const { errors } = form.formState;
  const failedProvider = providers.find((provider) => provider.slug === errorProvider);
  // The sentence names the provider, which the list of methods knows; it is
  // said once that list is read, not first without the name.
  const namesProvider = errorProvider !== undefined && methods.isPending;
  return (
    <div className="flex flex-col gap-4">
      {ended && !refusal && !error ? (
        <p role="status" className="flex items-start gap-2 rounded-md border bg-muted p-3 text-sm">
          <InfoIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
          <Trans>Your session has ended. Sign in again to continue.</Trans>
        </p>
      ) : null}
      {error && !refusal && !namesProvider ? (
        <p
          role="alert"
          className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
        >
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          {ssoErrorMessage(i18n, error, failedProvider?.display_name)}
        </p>
      ) : null}
      {localLogin ? (
        <form
          noValidate
          className="flex flex-col gap-4"
          aria-describedby={refusal ? refusalId : undefined}
          onSubmit={form.handleSubmit((values) => submit.mutate(values))}
        >
          {refusal ? (
            <p
              id={refusalId}
              role="alert"
              className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
            >
              <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
              {signInRefusalMessage(i18n, refusal)}
            </p>
          ) : null}
          <Field label={<Trans>Email</Trans>} error={errors.email?.message}>
            <Input type="email" autoComplete="username" autoFocus {...form.register("email")} />
          </Field>
          <Field label={<Trans>Password</Trans>} error={errors.password?.message}>
            <Input type="password" autoComplete="current-password" {...form.register("password")} />
          </Field>
          <Button type="submit" disabled={submit.isPending}>
            {submit.isPending ? <Trans>Signing in…</Trans> : <Trans>Sign in</Trans>}
          </Button>
          {methods.data?.password_reset_available ? (
            <Link
              to="/forgot-password"
              className="self-center text-sm text-primary underline-offset-4 hover:underline"
            >
              <Trans>Forgot your password?</Trans>
            </Link>
          ) : null}
        </form>
      ) : null}
      {providers.length > 0 ? (
        <>
          {localLogin ? (
            <p
              aria-hidden
              className="flex items-center gap-3 text-xs text-muted-foreground before:h-px before:flex-1 before:bg-border after:h-px after:flex-1 after:bg-border"
            >
              <Trans>or</Trans>
            </p>
          ) : null}
          <ul aria-label={t`Sign in with a provider`} className="flex flex-col gap-2">
            {providers.map((provider) => {
              const name = provider.display_name;
              return (
                <li key={provider.slug}>
                  <Button variant="outline" className="w-full" asChild>
                    <a href={signInStartPath(provider.slug, returnTo)}>
                      <Trans>Sign in with {name}</Trans>
                    </a>
                  </Button>
                </li>
              );
            })}
          </ul>
        </>
      ) : null}
    </div>
  );
}

/**
 * A refusal of sign-in. The address throttle refuses only sign-ins, so
 * here it can say whose attempts were too many; elsewhere the same code
 * reads as too many requests.
 */
function signInRefusalMessage(i18n: I18n, refusal: ApiError): string {
  if (refusal.code !== "rate_limited") return problemMessage(i18n, refusal);
  const retry = retryWait(i18n, refusal);
  return retry
    ? i18n._(msg`Too many sign-in attempts from your network. Try again in ${retry}.`)
    : i18n._(msg`Too many sign-in attempts from your network. Try again later.`);
}
