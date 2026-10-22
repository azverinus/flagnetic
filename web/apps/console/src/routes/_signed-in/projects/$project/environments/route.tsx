import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** A project's environments: the list, and an environment's page under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/environments")({
  staticData: { crumb: msg`Environments` },
});
