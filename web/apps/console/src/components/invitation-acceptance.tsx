import { acceptInvitation, type Invitation, type Me } from "@flagnetic/api";
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
import { formRefusal } from "@/lib/problem-messages";
import { invitationQuery } from "@/lib/queries";
import { forgetsSecret } from "@/lib/mutations";

export type InvitationAcceptanceProps = {
  /** The invitation's secret, the last segment of its link. */
  secret: string;
  /** Called with the new member once they are signed in. */
  onAccepted: (me: Me) => void;
};

function isGone(error: unknown): boolean {
  return error instanceof ApiError && error.code === "invitation_gone";
}

/**
 * What an invitation's link opens: whose invitation it is, and the
 * member's first password, which signs them in. A link that opens nothing
 * any more says why, whether it was used, replaced or expired, alike.
 */
export function InvitationAcceptance({ secret, onAccepted }: InvitationAcceptanceProps) {
  const invitation = useQuery(invitationQuery(secret));
  if (invitation.data === undefined) {
    if (isGone(invitation.error)) return <InvitationGone />;
    if (invitation.isError) {
      return <QueryError error={invitation.error} onRetry={() => void invitation.refetch()} />;
    }
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-7 w-2/3" />
        <Skeleton className="h-16 w-full" />
      </div>
    );
  }
  return <AcceptForm secret={secret} invitation={invitation.data} onAccepted={onAccepted} />;
}

/** The fields whose refusals are shown under them. */
const fieldPointers = ["/password"];

function AcceptForm({
  secret,
  invitation,
  onAccepted,
}: {
  secret: string;
  invitation: Invitation;
} & Pick<InvitationAcceptanceProps, "onAccepted">) {
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
  const accept = useMutation({
    mutationFn: ({ password }: Values) =>
      unwrap(acceptInvitation({ path: { secret }, body: { password } })),
    // The password chosen is in the variables.
    ...forgetsSecret,
    meta: { toast: false, readsAuditAgain: false },
    onSuccess: (me) => onAccepted(me),
  });
  if (isGone(accept.error)) return <InvitationGone />;

  const { errors } = form.formState;
  const refusal = formRefusal(accept.error, fieldPointers);
  const { display_name: name, email } = invitation;
  const until = formatDateTime(i18n, invitation.expires_at);

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">
        <Trans>Accept your invitation</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>
          You are invited to this Flagnetic instance as {name} ({email}). Choose a password to join;
          the link works until {until}.
        </Trans>
      </p>
      <form
        noValidate
        className="flex flex-col gap-4"
        onSubmit={form.handleSubmit((values) => accept.mutate(values))}
      >
        <Field
          label={<Trans>Password</Trans>}
          hint={
            <Trans>
              At least 12 characters, or as many as this instance asks for. Length is what makes a
              password hard to guess.
            </Trans>
          }
          error={errors.password?.message}
          refusal={accept.error}
          pointer="/password"
        >
          <Input
            type="password"
            autoComplete="new-password"
            autoFocus
            {...form.register("password")}
          />
        </Field>
        <Field label={<Trans>Confirm the password</Trans>} error={errors.confirm_password?.message}>
          <Input
            type="password"
            autoComplete="new-password"
            {...form.register("confirm_password")}
          />
        </Field>
        <FormRefusal refusal={refusal} />
        <Button type="submit" disabled={accept.isPending}>
          {accept.isPending ? <Trans>Joining…</Trans> : <Trans>Accept invitation</Trans>}
        </Button>
      </form>
    </div>
  );
}

function InvitationGone() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-xl font-semibold">
        <Trans>This invitation no longer works</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>
          An invitation link works once, for seven days, and only until a newer one replaces it. Ask
          an instance admin for a new link, or sign in if you have already joined.
        </Trans>
      </p>
      <Button asChild variant="outline" className="self-start">
        <Link to="/sign-in">
          <Trans>Sign in</Trans>
        </Link>
      </Button>
    </div>
  );
}
