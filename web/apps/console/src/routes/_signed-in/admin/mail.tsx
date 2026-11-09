import { msg } from "@lingui/core/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { MailSettingsScreen } from "@/components/mail-settings";
import { meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/admin/mail")({
  staticData: { crumb: msg`Mail` },
  component: MailPage,
});

/** The instance's SMTP server; the test mail goes to the member looking at it. */
function MailPage() {
  const { data: me } = useSuspenseQuery(meQuery);
  return <MailSettingsScreen recipient={me.member?.email ?? ""} />;
}
