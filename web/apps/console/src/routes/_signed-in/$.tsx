import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { NotFound } from "@/components/not-found";

/**
 * Any address no page claims, caught behind the guard so that it renders
 * inside the shell rather than throwing the member out of it.
 */
export const Route = createFileRoute("/_signed-in/$")({
  staticData: { crumb: msg`Not found` },
  component: NotFound,
});
