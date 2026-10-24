import type { FlagListItem, UnitTypeListItem } from "@flagnetic/api";
import { useQuery } from "@tanstack/react-query";

import { flagChoicesQuery, unitTypesQuery } from "./queries";
import type { ReadFailure } from "./use-known-members";

export type ExperimentChoices =
  | { state: "loading" }
  | { state: "failed"; error: unknown; retry: () => void }
  | { state: "ready"; flags: FlagListItem[]; unitTypes: UnitTypeListItem[] };

/**
 * What the experiment form chooses from: the project's flags that are
 * not archived, with their variations, and its declared unit types.
 */
export function useExperimentChoices(project: string): ExperimentChoices {
  const flags = useQuery(flagChoicesQuery(project));
  const unitTypes = useQuery(unitTypesQuery(project));
  const failed = [flags, unitTypes].find((read) => read.isError);
  if (failed) {
    const failure: ReadFailure = { error: failed.error, retry: () => void failed.refetch() };
    return { state: "failed", ...failure };
  }
  if (flags.data === undefined || unitTypes.data === undefined) return { state: "loading" };
  return {
    state: "ready",
    flags: flags.data.filter((flag) => flag.archived_at === null),
    unitTypes: unitTypes.data,
  };
}
