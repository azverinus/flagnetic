import { msg } from "@lingui/core/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { TokenList } from "@/components/token-list";
import { tokenPolicy } from "@/lib/personal-tokens";
import { meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/settings/tokens")({
  staticData: { crumb: msg`API tokens` },
  component: Tokens,
});

function Tokens() {
  const { data: me } = useSuspenseQuery(meQuery);
  return <TokenList instanceAdmin={me.member?.instance_admin ?? false} policy={tokenPolicy(me)} />;
}
