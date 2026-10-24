import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** A project's experiments: the list, creating one and editing one under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/experiments")({
  staticData: { crumb: msg`Experiments` },
});
