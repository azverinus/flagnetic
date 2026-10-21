import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { SessionList } from "@/components/session-list";

export const Route = createFileRoute("/_signed-in/settings/sessions")({
  staticData: { crumb: msg`Sessions` },
  component: SessionList,
});
