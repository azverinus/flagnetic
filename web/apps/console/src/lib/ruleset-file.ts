import { getEnvironmentRuleset } from "@flagnetic/api";

import { unwrap } from "@/lib/errors";

/** A ruleset document as a file to save: its name and its text. */
export type RulesetFile = { name: string; text: string };

/**
 * The file an environment's ruleset is saved as, named after the project,
 * the environment and the ruleset's version so that two downloads of one
 * environment never overwrite each other, and written readably since it
 * is meant to be opened, diffed and fed to a test helper.
 */
export function rulesetFile(
  project: string,
  environment: string,
  document: Record<string, unknown>,
): RulesetFile {
  const { version } = document;
  const suffix = typeof version === "number" ? `-${version}` : "";
  return {
    name: `${project}-${environment}-ruleset${suffix}.json`,
    text: `${JSON.stringify(document, null, 2)}\n`,
  };
}

/** Reads the environment's ruleset and hands it to the browser as a download. */
export async function downloadRuleset(project: string, environment: string): Promise<void> {
  const document = await unwrap(getEnvironmentRuleset({ path: { project, environment } }));
  const file = rulesetFile(project, environment, document);
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
