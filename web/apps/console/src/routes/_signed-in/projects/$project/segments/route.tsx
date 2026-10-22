import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

/** A project's segments: the list, creating one and editing one under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/segments")({
  staticData: { crumb: msg`Segments` },
});
