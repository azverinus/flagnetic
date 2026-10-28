import type { QueryClient, QueryKey } from "@tanstack/react-query";

import { auditQueryKey } from "./queries";

/**
 * What the project's change stream tells of one audit entry: the entity
 * it changed and where, never its content.
 */
export type ChangeNotice = {
  /** The audit entry's identifier, by which the stream resumes. */
  id: string;
  /** The entity's kind as the audit log names it, such as `segment`. */
  kind: string;
  key: string;
  /** The key of the environment the change addressed, if any. */
  environment: string | null;
  /** The entity's version after the change; none for an entity without one. */
  version: number | null;
};

/** Reads a `change` frame's data; none when it is not a notice. */
export function parseNotice(id: string, data: string): ChangeNotice | undefined {
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return undefined;
  }
  if (typeof value !== "object" || value === null) return undefined;
  const { kind, key, environment, version } = value as Record<string, unknown>;
  if (typeof kind !== "string" || typeof key !== "string") return undefined;
  return {
    id,
    kind,
    key,
    environment: typeof environment === "string" ? environment : null,
    version: typeof version === "number" ? version : null,
  };
}

/**
 * The cache prefixes a notice makes stale, within one project. A flag's
 * configuration is audited under the flag, so a flag notice covers it;
 * what a change may also move (a segment's usages in flags, change
 * requests superseded by an entity's change) is named beside the entity.
 * An unknown kind refreshes the whole project, which costs more but never
 * leaves a screen stale.
 */
export function noticeKeys(project: string, notice: Pick<ChangeNotice, "kind">): QueryKey[] {
  const at = (...rest: string[]): QueryKey => ["projects", project, ...rest];
  switch (notice.kind) {
    case "flag":
    case "flag_configuration":
      return [at("flags"), at("change-requests"), at("experiments")];
    case "experiment":
      return [at("experiments"), at("flags"), at("change-requests")];
    case "segment":
      return [at("segments"), at("flags"), at("change-requests")];
    case "environment":
    case "sdk_key":
      return [at("environments"), at("flags"), at("experiments")];
    case "attribute":
      return [at("attributes")];
    case "unit_type":
      return [at("unit-types")];
    case "member":
    case "grant":
      return [at("members"), at("me")];
    case "api_token":
      return [at("tokens")];
    case "change_request":
      return [at("change-requests")];
    case "webhook":
      return [at("webhooks")];
    default:
      return [at()];
  }
}

/** Marks stale what a notice names, with the project's audit log: every entry is in it. */
export function applyNotice(queryClient: QueryClient, project: string, notice: ChangeNotice) {
  const refresh = (queryKey: QueryKey) => void queryClient.invalidateQueries({ queryKey });
  for (const queryKey of noticeKeys(project, notice)) refresh(queryKey);
  // A project's own change touches the caller's project list too.
  if (notice.kind === "project") refresh(["projects"]);
  refreshAudit(queryClient, project);
}

/** Marks everything the console holds of a project stale, after a notice may have been missed. */
export function applyRefetch(queryClient: QueryClient, project: string) {
  void queryClient.invalidateQueries({ queryKey: ["projects", project] });
  refreshAudit(queryClient, project);
}

function refreshAudit(queryClient: QueryClient, project: string) {
  void queryClient.invalidateQueries({
    queryKey: auditQueryKey,
    predicate: ({ queryKey }) => {
      const scope = queryKey[1];
      return typeof scope === "object" && scope !== null && "project" in scope
        ? scope.project === project
        : false;
    },
  });
}
