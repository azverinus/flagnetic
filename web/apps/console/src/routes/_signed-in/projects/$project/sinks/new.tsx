import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { NewSinkScreen } from "@/components/sink-screen";

export const Route = createFileRoute("/_signed-in/projects/$project/sinks/new")({
  staticData: { crumb: msg`New sink` },
  component: NewSink,
});

function NewSink() {
  const { project } = Route.useParams();
  return <NewSinkScreen project={project} />;
}
