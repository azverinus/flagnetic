import { acceptInvitation, type Invitation, type Me } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { FormRefusal } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDateTime } from "@/i18n/format";
import { signInStartPath } from "@/lib/auth-providers";
import { roleName } from "@/lib/access-names";
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
 * What an invitation's link opens: whose invitation it is, and the ways to
 * join it: the member's first password, which signs them in, where the
 * instance allows one, and signing in through an identity provider that
 * vouches for their email. A link that opens nothing
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
  // Accepting found the link no longer works: the page says so in place of the form.
  const [gone, setGone] = useState(false);
  const { display_name: name, email, password_allowed: passwordAllowed, providers } = invitation;
  const until = formatDateTime(i18n, invitation.expires_at);
  if (gone) return <InvitationGone />;
  const choices = passwordAllowed
    ? providers.length > 0
      ? "both"
      : "password"
    : providers.length > 0
      ? "providers"
      : "none";

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">
        <Trans>Accept your invitation</Trans>
      </h1>
      <InvitedBy invitation={invitation} />
      <p className="text-sm text-muted-foreground">
        {choices === "password" ? (
          <Trans>
            You are invited to this Flagnetic instance as {name} ({email}). Choose a password to
            join; the link works until {until}.
          </Trans>
        ) : choices === "providers" ? (
          <Trans>
            You are invited to this Flagnetic instance as {name} ({email}). Sign in through your
            identity provider to join; the link works until {until}.
          </Trans>
        ) : choices === "both" ? (
          <Trans>
            You are invited to this Flagnetic instance as {name} ({email}). Choose a password, or
            sign in through an identity provider, to join; the link works until {until}.
          </Trans>
        ) : (
          <Trans>
            You are invited to this Flagnetic instance as {name} ({email}), but no way to join it is
            available now: this instance accepts no password and none of its identity providers
            vouches for your email address. Ask an instance admin.
          </Trans>
        )}
      </p>
      {passwordAllowed ? (
        <PasswordForm secret={secret} onAccepted={onAccepted} onGone={() => setGone(true)} />
      ) : null}
      {passwordAllowed && providers.length > 0 ? (
        <p
          aria-hidden
          className="flex items-center gap-3 text-xs text-muted-foreground before:h-px before:flex-1 before:bg-border after:h-px after:flex-1 after:bg-border"
        >
          <Trans>or</Trans>
        </p>
      ) : null}
      {providers.length > 0 ? <ProviderButtons providers={providers} /> : null}
    </div>
  );
}

/**
 * Joining through an identity provider: signing in at one whose domains
 * hold the invited email activates the member and makes the grant the
 * invitation carries, with no password ever set.
 */
function ProviderButtons({ providers }: { providers: Invitation["providers"] }) {
  const { t } = useLingui();
  return (
    <ul aria-label={t`Accept with a provider`} className="flex flex-col gap-2">
      {providers.map((provider) => {
        const name = provider.display_name;
        return (
          <li key={provider.slug}>
            <Button variant="outline" className="w-full" asChild>
              <a href={signInStartPath(provider.slug)}>
                <Trans>Sign in with {name}</Trans>
              </a>
            </Button>
          </li>
        );
      })}
    </ul>
  );
}

function PasswordForm({
  secret,
  onAccepted,
  onGone,
}: { secret: string; onGone: () => void } & Pick<InvitationAcceptanceProps, "onAccepted">) {
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
    onError: (error) => {
      if (isGone(error)) onGone();
    },
  });

  const { errors } = form.formState;
  const refusal = formRefusal(accept.error, fieldPointers);

  return (
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
        <Input type="password" autoComplete="new-password" {...form.register("confirm_password")} />
      </Field>
      <FormRefusal refusal={refusal} />
      <Button type="submit" disabled={accept.isPending}>
        {accept.isPending ? <Trans>Joining…</Trans> : <Trans>Accept invitation</Trans>}
      </Button>
    </form>
  );
}

/**
 * Who invited the person and, while the invitation still grants one, the
 * role it grants in which project, so they know what they accept. An
 * invitation of the instance alone, or whose grant was dropped, names no
 * project.
 */
function InvitedBy({ invitation }: { invitation: Invitation }) {
  const { i18n } = useLingui();
  const { project, invited_by: inviter } = invitation;
  const role = invitation.role === undefined ? undefined : roleName(i18n, invitation.role);
  let sentence: ReactNode = null;
  if (project && role !== undefined) {
    const projectName = project.name;
    sentence = inviter ? (
      <Trans>
        {inviter} invites you to the project {projectName} as {role}. Accepting grants you that role
        there.
      </Trans>
    ) : (
      <Trans>
        You are invited to the project {projectName} as {role}. Accepting grants you that role
        there.
      </Trans>
    );
  } else if (inviter) {
    sentence = <Trans>{inviter} invites you to this instance.</Trans>;
  }
  if (sentence === null) return null;
  return <p className="text-sm">{sentence}</p>;
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
