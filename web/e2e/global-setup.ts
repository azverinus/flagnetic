import { createClient, createConfig, createProject, listProjects } from "@flagnetic/api";

import {
  bootstrap,
  build,
  remember,
  secondProject,
  seededProject,
  serve,
} from "./support/instance";

/**
 * Builds the console and the binary that embeds it, starts it against
 * PostgreSQL with the test clock, bootstraps an instance admin through the
 * CLI, and seeds through the management API with the generated client.
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

  remember({ baseURL, token });
}
