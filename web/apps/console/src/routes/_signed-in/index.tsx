import { msg } from "@lingui/core/macro";
import { Trans } from "@lingui/react/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/")({
  staticData: { crumb: msg`Home` },
  component: Home,
});

function Home() {
  const { data: me } = useSuspenseQuery(meQuery);
  const name = me.member?.display_name || me.member?.email || "";
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-2 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Welcome, {name}</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>You are signed in to Flagnetic.</Trans>
      </p>
    </div>
  );
}
