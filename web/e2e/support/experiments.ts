import { createExperiment, startExperiment } from "@flagnetic/api";

import { managementClient, must, protectProduction, unprotectProduction } from "./console";
import { seedFlag, stringFlag, writeConfiguration } from "./flags";

/** A running experiment whose rule in the protected production serves it. */
export async function runningExperimentInProduction(project: string): Promise<void> {
  const client = managementClient();
  await seedFlag(project, stringFlag("checkout_cta"));
  const draft = must(
    "creating cta_copy",
    await createExperiment({
      client,
      path: { project },
      body: {
        key: "cta_copy",
        name: "CTA copy",
        hypothesis: "The shorter checkout converts better.",
        flag: "checkout_cta",
        unit_type: "user",
        variations: [
          { variation: "control", weight: 5000 },
          { variation: "treatment", weight: 5000 },
        ],
        expected_end_at: "2027-01-31",
      },
    }),
  );
  await unprotectProduction(project);
  await writeConfiguration(project, "checkout_cta", "production", () => ({
    rules: [
      {
        description: "Beta testers",
        conditions: [],
        serve: { kind: "experiment", experiment: "cta_copy" },
      },
    ],
  }));
  must(
    "starting cta_copy",
    await startExperiment({
      client,
      path: { project, experiment: "cta_copy" },
      headers: { "If-Match": `"${draft.version}"` },
    }),
  );
  await protectProduction(project);
}
