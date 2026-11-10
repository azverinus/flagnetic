import { msg } from "@lingui/core/macro";
import { Trans } from "@lingui/react/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { ProfileForm } from "@/components/profile-form";
import { ThemeChoice } from "@/components/theme-choice";
import { SettingsSection } from "@/components/ui/settings-section";
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
        <SettingsSection title={<Trans>Profile</Trans>}>
          <ProfileForm member={me.member} />
        </SettingsSection>
      ) : null}
      <SettingsSection title={<Trans>Appearance</Trans>}>
        <ThemeChoice />
      </SettingsSection>
    </div>
  );
}
