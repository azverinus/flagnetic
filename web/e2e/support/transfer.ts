import { exportProject } from "@flagnetic/api";

import { managementClient, must } from "./console";

/** The project document the server exports for a project, parsed. */
export async function exportedDocument(project: string): Promise<{
  flags: { key: string; configurations: Record<string, { on: boolean }> }[];
}> {
  const text = must(
    `exporting ${project}`,
    await exportProject({
      client: managementClient(),
      path: { project },
      query: { format: "json" },
      parseAs: "text",
    }),
  ) as unknown as string;
  return JSON.parse(text);
}

/**
 * The project's document with a flag switched the other way in an
 * environment, as a file a member could have edited and uploaded.
 */
export async function documentWithFlagToggled(
  project: string,
  flag: string,
  environment: string,
): Promise<{ name: string; mimeType: string; buffer: Buffer }> {
  const document = await exportedDocument(project);
  const configuration = document.flags.find((entry) => entry.key === flag)!.configurations[
    environment
  ]!;
  configuration.on = !configuration.on;
  return {
    name: `${project}.json`,
    mimeType: "application/json",
    buffer: Buffer.from(JSON.stringify(document, null, 2)),
  };
}
