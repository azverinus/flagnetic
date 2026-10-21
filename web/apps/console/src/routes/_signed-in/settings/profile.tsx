import { msg } from "@lingui/core/macro";
import { Trans } from "@lingui/react/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { PasswordForm } from "@/components/password-form";
import { ProfileForm } from "@/components/profile-form";
import { ThemeChoice } from "@/components/theme-choice";
import { meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/settings/profile")({
  staticData: { crumb: msg`Profile` },
  component: Profile,
});

function Profile() {
  const { data: me } = useSuspenseQuery(meQuery);
  return (
    <div className="flex flex-col gap-10">
      {me.member ? (
        <Section title={<Trans>Profile</Trans>}>
          <ProfileForm member={me.member} />
        </Section>
      ) : null}
      <Section
        title={<Trans>Password</Trans>}
        description={
          <Trans>
            Changing it signs out every other device you are signed in on; this one stays signed in.
          </Trans>
        }
      >
        <PasswordForm />
      </Section>
      <Section title={<Trans>Appearance</Trans>}>
        <ThemeChoice />
      </Section>
    </div>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-lg font-semibold">{title}</h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {children}
    </section>
  );
}
