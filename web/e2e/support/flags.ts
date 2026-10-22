import {
  createFlag,
  type FlagConfiguration,
  type FlagConfigurationPut,
  type FlagCreate,
  getFlag,
  getFlagConfiguration,
  putFlagConfiguration,
} from "@flagnetic/api";
import { expect, type Page } from "@playwright/test";

import { type Credentials, managementClient, must, signIn } from "./console";
import { admin } from "./instance";

/** A string flag with the variations control and treatment, as most flag specs need one. */
export function stringFlag(key: string, over: Partial<FlagCreate> = {}): FlagCreate {
  return {
    key,
    type: "string",
    name: key,
    variations: [
      { key: "control", value: "control" },
      { key: "treatment", value: "treatment" },
    ],
    ...over,
  };
}

/** Creates a flag through the management API, as the instance admin. */
export async function seedFlag(project: string, body: FlagCreate): Promise<void> {
  must(
    `creating ${body.key}`,
    await createFlag({ client: managementClient(), path: { project }, body }),
  );
}

export async function readFlag(project: string, flag: string) {
  return must(
    `reading ${flag}`,
    await getFlag({ client: managementClient(), path: { project, flag } }),
  );
}

export async function readConfiguration(
  project: string,
  flag: string,
  environment: string,
): Promise<FlagConfiguration> {
  return must(
    `reading ${flag} in ${environment}`,
    await getFlagConfiguration({
      client: managementClient(),
      path: { project, flag, environment },
    }),
  );
}

/**
 * Replaces a configuration through the management API, as someone else
 * saving while a page is open would, starting from what it is now.
 */
export async function writeConfiguration(
  project: string,
  flag: string,
  environment: string,
  change: (current: FlagConfiguration) => Partial<FlagConfigurationPut>,
): Promise<FlagConfiguration> {
  const current = await readConfiguration(project, flag, environment);
  const body: FlagConfigurationPut = {
    on: current.on,
    off_variation: current.off_variation,
    fallthrough: current.fallthrough,
    prerequisites: current.prerequisites,
    overrides: current.overrides.map(({ unit_type, unit_id, variation, note }) => ({
      unit_type,
      unit_id,
      variation,
      note,
    })),
    rules: current.rules,
    ...change(current),
  };
  return must(
    `writing ${flag} in ${environment}`,
    await putFlagConfiguration({
      client: managementClient(),
      path: { project, flag, environment },
      body,
      headers: { "If-Match": `"${current.version}"` },
    }),
  );
}

/** Signs in and opens a flag's page, in an environment's tab when one is named. */
export async function openFlag(
  page: Page,
  project: string,
  flag: string,
  { environment, who = admin }: { environment?: string; who?: Credentials } = {},
): Promise<void> {
  await signIn(page, who);
  const search = environment ? `?environment=${environment}` : "";
  await page.goto(`/projects/${project}/flags/${flag}${search}`);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
}

/** Picks an option of one of the console's selects. */
export async function choose(page: Page, field: string, option: string): Promise<void> {
  await page.getByRole("combobox", { name: field, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}
