import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { SinkScreen } from "@/components/sink-screen";

export const Route = createFileRoute("/_signed-in/projects/$project/sinks/$sink")({
  staticData: { crumb: msg`Sink` },
  component: ProjectSink,
});

function ProjectSink() {
  const { project, sink } = Route.useParams();
  return <SinkScreen project={project} sink={sink} />;
}
