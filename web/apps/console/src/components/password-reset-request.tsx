import { requestPasswordReset } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleAlertIcon, MailCheckIcon } from "lucide-react";
import { useId, useMemo } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, unwrap } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";
import { authMethodsQuery } from "@/lib/queries";

/**
 * The forgotten-password page: a member gives the email they sign in with
 * and is told the same thing whatever it is, since the answer must not
 * say which emails belong to a member. The server mails the link, if it
 * mails one, after it has answered. An instance that mails no reset says
 * so instead of offering a form that leads nowhere.
 */
export function PasswordResetRequest() {
  const methods = useQuery(authMethodsQuery);
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">
        <Trans>Forgot your password?</Trans>
      </h1>
      {methods.data === undefined && methods.isPending ? (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : methods.data?.password_reset_available === false ? (
        <>
          <p className="text-sm text-muted-foreground">
            <Trans>
              This instance does not mail password resets. Ask an instance admin for a password
              reset link.
            </Trans>
          </p>
          <BackToSignIn />
        </>
      ) : (
        // A list of methods that could not be read leaves the form: the
        // server answers the request the same either way.
        <RequestForm />
      )}
    </div>
  );
}

function RequestForm() {
  const { i18n, t } = useLingui();
  const refusalId = useId();
  const schema = useMemo(
    () =>
      z.object({
        email: z
          .string()
          .trim()
          .min(1, t(msg`Enter your email.`)),
      }),
    [t],
  );
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { email: "" } });
  const send = useMutation({
    mutationFn: (body: Values) => unwrap(requestPasswordReset({ body })),
    meta: { toast: false, readsAuditAgain: false },
  });

  if (send.isSuccess) {
    return (
      <>
        <p role="status" className="flex items-start gap-2 rounded-md border bg-muted p-3 text-sm">
          <MailCheckIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
          <Trans>
            If this email belongs to a member who can reset their password, a link to reset it is on
            its way. It works for an hour.
          </Trans>
        </p>
        <BackToSignIn />
      </>
    );
  }

  const refusal = send.error instanceof ApiError ? send.error : undefined;
  return (
    <>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Enter the email you sign in with, and a link to choose a new password is mailed.
        </Trans>
      </p>
      <form
        noValidate
        className="flex flex-col gap-4"
        aria-describedby={refusal ? refusalId : undefined}
        onSubmit={form.handleSubmit((values) => send.mutate(values))}
      >
        {refusal ? (
          <p
            id={refusalId}
            role="alert"
            className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
          >
            <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
            {problemMessage(i18n, refusal)}
          </p>
        ) : null}
        <Field label={<Trans>Email</Trans>} error={form.formState.errors.email?.message}>
          <Input type="email" autoComplete="username" autoFocus {...form.register("email")} />
        </Field>
        <Button type="submit" disabled={send.isPending}>
          {send.isPending ? <Trans>Sending…</Trans> : <Trans>Send a password reset link</Trans>}
        </Button>
      </form>
      <BackToSignIn />
    </>
  );
}

function BackToSignIn() {
  return (
    <Button asChild variant="link" className="self-start px-0">
      <Link to="/sign-in">
        <Trans>Back to sign in</Trans>
      </Link>
    </Button>
  );
}
