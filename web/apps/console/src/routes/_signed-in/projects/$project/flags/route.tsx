import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** A project's flags: the list, and creating one under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/flags")({
  staticData: { crumb: msg`Flags` },
});
