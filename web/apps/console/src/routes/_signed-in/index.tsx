import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { PageHeader } from "@/components/ui/page-header";
import { meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/")({
  staticData: { crumb: msg`Home` },
  component: Home,
});

function Home() {
  const { t } = useLingui();
  const { data: me } = useSuspenseQuery(meQuery);
  const name = me.member?.display_name || me.member?.email || "";
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Welcome, ${name}`}
        description={<Trans>You are signed in to Flagnetic.</Trans>}
      />
    </div>
  );
}
