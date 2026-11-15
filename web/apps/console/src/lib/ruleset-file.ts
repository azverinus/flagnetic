import { getEnvironmentRuleset } from "@flagnetic/api";

import { unwrap } from "@/lib/errors";

/** A ruleset document as a file to save: its name and its text. */
export type RulesetFile = { name: string; text: string };

/**
 * The file an environment's ruleset is saved as, named after the project,
 * the environment and the ruleset's version so that two downloads of one
 * environment never overwrite each other. Its text is the document as the
 * server sent it, byte for byte: the file is what the SDKs receive, and
 * is meant to be diffed and fed to a test helper, so it is neither laid
 * out again nor has a number in it rewritten.
 */
export function rulesetFile(project: string, environment: string, text: string): RulesetFile {
  let version: unknown;
  try {
    version = (JSON.parse(text) as { version?: unknown } | null)?.version;
  } catch {
    // Not JSON: it is still saved as it came, under a name without a version.
  }
  const suffix = typeof version === "number" ? `-${version}` : "";
  return { name: `${project}-${environment}-ruleset${suffix}.json`, text };
}

/** Reads the environment's ruleset and hands it to the browser as a download. */
export async function downloadRuleset(project: string, environment: string): Promise<void> {
  const text = await unwrap(
    getEnvironmentRuleset({ path: { project, environment }, parseAs: "text" }),
  );
  const file = rulesetFile(project, environment, text as unknown as string);
  const url = URL.createObjectURL(new Blob([file.text], { type: "application/json" }));
  const link = Object.assign(window.document.createElement("a"), {
    href: url,
    download: file.name,
  });
  link.click();
  // Some browsers start the download only after the click returns, and a
  // URL revoked before then saves nothing; it is released on the next tick.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
