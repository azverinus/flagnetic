import {
  acceptInvitation,
  createClient,
  createConfig,
  createGrant,
  createProject,
  inviteMember,
  listProjects,
} from "@flagnetic/api";

import {
  bootstrap,
  build,
  remember,
  secondProject,
  seededProject,
  serve,
  viewer,
} from "./support/instance";

/**
 * Builds the console and the binary that embeds it, starts it against
 * PostgreSQL with the test clock, bootstraps an instance admin through the
 * CLI, and seeds through the management API with the generated client:
 * two projects and a viewer of the first.
 */
export default async function globalSetup(): Promise<void> {
  const binary = await build();
  const token = await bootstrap(binary);
  const { baseURL } = await serve(binary);

  const client = createClient(
    createConfig({
      baseUrl: `${baseURL}/api/v1`,
      headers: { Authorization: `Bearer ${token}` },
    }),
  );

  for (const project of [seededProject, secondProject]) {
    const created = await createProject({ client, body: project });
    if (created.error) {
      throw new Error(`seeding ${project.key} failed: ${JSON.stringify(created.error)}`);
    }
  }
  const projects = await listProjects({ client });
  for (const project of [seededProject, secondProject]) {
    if (!projects.data?.items.some((seeded) => seeded.key === project.key)) {
      throw new Error(`the seeded project ${project.key} is not in the project list`);
    }
  }

  // The viewer: invited by the instance admin, the invitation accepted
  // with its secret (the last segment of the one-time link), then granted
  // viewer in the first project only.
  const invited = await inviteMember({
    client,
    body: { email: viewer.email, display_name: viewer.name },
  });
  const link = invited.data?.invitation?.url;
  if (!invited.data || !link) {
    throw new Error(`inviting ${viewer.email} failed: ${JSON.stringify(invited.error)}`);
  }
  const anonymous = createClient(createConfig({ baseUrl: `${baseURL}/api/v1` }));
  const accepted = await acceptInvitation({
    client: anonymous,
    path: { secret: link.slice(link.lastIndexOf("/") + 1) },
    body: { password: viewer.password },
  });
  if (accepted.error) {
    throw new Error(`accepting the invitation failed: ${JSON.stringify(accepted.error)}`);
  }
  const granted = await createGrant({
    client,
    path: { project: seededProject.key },
    body: { member: invited.data.member.id, role: "viewer" },
  });
  if (granted.error) {
    throw new Error(`granting viewer failed: ${JSON.stringify(granted.error)}`);
  }

  remember({ baseURL, token });
}
