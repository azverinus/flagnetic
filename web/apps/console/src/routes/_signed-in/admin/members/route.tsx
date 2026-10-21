import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** The instance's members: the list, and one member's page under it. */
export const Route = createFileRoute("/_signed-in/admin/members")({
  staticData: { crumb: msg`Members` },
});
