import { createFileRoute } from "@tanstack/react-router";

import { SinkList } from "@/components/sink-list";

export const Route = createFileRoute("/_signed-in/projects/$project/sinks/")({
  component: ProjectSinks,
});

function ProjectSinks() {
  const { project } = Route.useParams();
  return <SinkList project={project} />;
}
