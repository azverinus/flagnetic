import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** A project's change requests: the list, and one change request under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/change-requests")({
  staticData: { crumb: msg`Change requests` },
});
