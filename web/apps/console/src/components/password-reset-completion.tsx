import { completePasswordReset, type Me, type PasswordReset } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { FormRefusal } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDateTime } from "@/i18n/format";
import { ApiError, unwrap } from "@/lib/errors";
import { forgetsSecret } from "@/lib/mutations";
import { formRefusal } from "@/lib/problem-messages";
import { passwordResetQuery } from "@/lib/queries";

export type PasswordResetCompletionProps = {
  /** The password reset's secret, the last segment of its link. */
  secret: string;
  /** Called with the member once their new password is set and they are signed in. */
  onReset: (me: Me) => void;
};

function isGone(error: unknown): boolean {
  return error instanceof ApiError && error.code === "reset_gone";
}

/**
 * What a password reset's link opens, mailed or passed on by an instance
 * admin: whose password it resets, and the new password, which signs the
 * member in and every other session of theirs out. A link that opens
 * nothing any more says why, whether it was used, ended or expired, alike.
 */
export function PasswordResetCompletion({ secret, onReset }: PasswordResetCompletionProps) {
  const reset = useQuery(passwordResetQuery(secret));
  if (reset.data === undefined) {
    if (isGone(reset.error)) return <ResetGone />;
    if (reset.isError) {
      return <QueryError error={reset.error} onRetry={() => void reset.refetch()} />;
    }
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-7 w-2/3" />
        <Skeleton className="h-16 w-full" />
      </div>
    );
  }
  return <ResetForm secret={secret} reset={reset.data} onReset={onReset} />;
}

/** The fields whose refusals are shown under them. */
const fieldPointers = ["/password"];

function ResetForm({
  secret,
  reset,
  onReset,
}: { secret: string; reset: PasswordReset } & Pick<PasswordResetCompletionProps, "onReset">) {
  const { i18n } = useLingui();
  const schema = z
    .object({
      password: z.string().min(1, i18n._(msg`Choose a password.`)),
      confirm_password: z.string(),
    })
    .refine((values) => values.password === values.confirm_password, {
      path: ["confirm_password"],
      message: i18n._(msg`The passwords do not match.`),
    });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { password: "", confirm_password: "" },
  });
  const complete = useMutation({
    mutationFn: ({ password }: Values) =>
      unwrap(completePasswordReset({ path: { secret }, body: { password } })),
    // The password chosen is in the variables.
    ...forgetsSecret,
    meta: { toast: false, readsAuditAgain: false },
    onSuccess: (me) => onReset(me),
  });
  if (isGone(complete.error)) return <ResetGone />;

  const { errors } = form.formState;
  const { display_name: name, email } = reset;
  const until = formatDateTime(i18n, reset.expires_at);

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">
        <Trans>Choose a new password</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Choose a new password for {name} ({email}); the link works until {until}. Setting it signs
          you out everywhere else.
        </Trans>
      </p>
      <form
        noValidate
        className="flex flex-col gap-4"
        onSubmit={form.handleSubmit((values) => complete.mutate(values))}
      >
        <Field
          label={<Trans>New password</Trans>}
          hint={
            <Trans>
              At least 12 characters, or as many as this instance asks for. Length is what makes a
              password hard to guess.
            </Trans>
          }
          error={errors.password?.message}
          refusal={complete.error}
          pointer="/password"
        >
          <Input
            type="password"
            autoComplete="new-password"
            autoFocus
            {...form.register("password")}
          />
        </Field>
        <Field
          label={<Trans>Confirm the new password</Trans>}
          error={errors.confirm_password?.message}
        >
          <Input
            type="password"
            autoComplete="new-password"
            {...form.register("confirm_password")}
          />
        </Field>
        <FormRefusal refusal={formRefusal(complete.error, fieldPointers)} />
        <Button type="submit" disabled={complete.isPending}>
          {complete.isPending ? <Trans>Saving…</Trans> : <Trans>Set password</Trans>}
        </Button>
      </form>
    </div>
  );
}

function ResetGone() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-xl font-semibold">
        <Trans>This reset link no longer works</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>
          A reset link works once, for an hour, and ends when the password is changed. Ask for a new
          link, or sign in if you remember your password.
        </Trans>
      </p>
      <div className="flex flex-wrap gap-2">
        <Button asChild>
          <Link to="/forgot-password">
            <Trans>Ask for a new link</Trans>
          </Link>
        </Button>
        <Button asChild variant="outline">
          <Link to="/sign-in">
            <Trans>Sign in</Trans>
          </Link>
        </Button>
      </div>
    </div>
  );
}
