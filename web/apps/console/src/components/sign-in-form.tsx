import { signIn, type Me, zSignInRequest } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleAlertIcon, InfoIcon } from "lucide-react";
import { useId, useMemo } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, unwrap } from "@/lib/errors";
import { problemMessage, retryWait } from "@/lib/problem-messages";
import { authMethodsQuery } from "@/lib/queries";

export type SignInFormProps = {
  /** Set when the member was sent here because their session ended. */
  ended?: boolean;
  /** Called with the caller once the session is open. */
  onSignedIn: (me: Me) => void;
};

/**
 * The sign-in form: email and password, and the reason for a refusal in the
 * member's language. Every refusal the server gives is explained here rather
 * than toasted, because the member has nothing else on the page.
 */
export function SignInForm({ ended, onSignedIn }: SignInFormProps) {
  const { i18n, t } = useLingui();
  const methods = useQuery(authMethodsQuery);
  const ids = { email: useId(), password: useId(), error: useId() };

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
    meta: { toast: false },
    onSuccess: onSignedIn,
  });

  if (methods.data && !methods.data.local_login_enabled) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        <Trans>No way to sign in is enabled on this instance. Ask its operator.</Trans>
      </p>
    );
  }

  const refusal = submit.error instanceof ApiError ? submit.error : undefined;
  const { errors } = form.formState;
  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      aria-describedby={refusal ? ids.error : undefined}
      onSubmit={form.handleSubmit((values) => submit.mutate(values))}
    >
      {ended && !refusal ? (
        <p role="status" className="flex items-start gap-2 rounded-md border bg-muted p-3 text-sm">
          <InfoIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
          <Trans>Your session has ended. Sign in again to continue.</Trans>
        </p>
      ) : null}
      {refusal ? (
        <p
          id={ids.error}
          role="alert"
          className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
        >
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          {signInRefusalMessage(i18n, refusal)}
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor={ids.email}>
          <Trans>Email</Trans>
        </Label>
        <Input
          id={ids.email}
          type="email"
          autoComplete="username"
          autoFocus
          aria-invalid={errors.email ? true : undefined}
          aria-describedby={errors.email ? `${ids.email}-error` : undefined}
          {...form.register("email")}
        />
        {errors.email ? (
          <p id={`${ids.email}-error`} className="text-sm text-destructive">
            {errors.email.message}
          </p>
        ) : null}
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor={ids.password}>
          <Trans>Password</Trans>
        </Label>
        <Input
          id={ids.password}
          type="password"
          autoComplete="current-password"
          aria-invalid={errors.password ? true : undefined}
          aria-describedby={errors.password ? `${ids.password}-error` : undefined}
          {...form.register("password")}
        />
        {errors.password ? (
          <p id={`${ids.password}-error`} className="text-sm text-destructive">
            {errors.password.message}
          </p>
        ) : null}
      </div>
      <Button type="submit" disabled={submit.isPending}>
        {submit.isPending ? <Trans>Signing in…</Trans> : <Trans>Sign in</Trans>}
      </Button>
    </form>
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
