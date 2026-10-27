import {
  getChangeRequest,
  listChangeRequests,
  type ChangeRequest,
  type ChangeRequestList,
  getAuthMethods,
  listMySessions,
  listMyTokens,
  type ApiToken,
  type Session,
  getProjectAuditEntry,
  listAuditEntries,
  listMembers,
  getAuthSettings,
  getInvitation,
  getMember,
  listMemberSessions,
  listMemberTokens,
  type AuthSettings,
  type Invitation,
  type MemberStatus,
  listProjectAuditEntries,
  listProjectMembers,
  listProjectTokens,
  type ProjectToken,
  type AuditEntry,
  type AuditEntryList,
  type Member,
  type ProjectMember,
  getMe,
  getProject,
  getProjectMe,
  listProjects,
  listRoles,
  listAttributes,
  listEnvironments,
  getEnvironment,
  listSdkKeys,
  listSdkInventory,
  type SdkInventoryItem,
  listDiscoveredAttributes,
  type DiscoveredAttribute,
  type SdkKey,
  listFlags,
  getFlag,
  getFlagEvaluations,
  listStaleFlags,
  type FlagEvaluations,
  type StaleFlagList,
  getEventVolume,
  type EventVolume,
  listExperiments,
  getExperiment,
  listExperimentAlerts,
  getExperimentAssignment,
  getExperimentConflicts,
  getExperimentExposures,
  getExperimentExposureTimeline,
  type Experiment,
  type ExperimentAssignment,
  type ExperimentAlertList,
  type ExperimentExposures,
  type ExposureConflicts,
  type ExposureTimeline,
  type ExperimentList,
  listSegments,
  getSegment,
  listSegmentUsages,
  type Flag,
  type Reference,
  type Segment,
  type SegmentListItem,
  listUnitTypes,
  type AttributeListItem,
  type AuthMethods,
  type Environment,
  type FlagList,
  type Me,
  type Project,
  type ProjectMe,
  type Role,
  type UnitTypeListItem,
} from "@flagnetic/api";
import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import { type AuditSearch, auditQueryOf } from "./audit";
import { type ChangeRequestsSearch, listChangeRequestsQueryOf } from "./change-requests";
import { unwrap } from "./errors";
import { type ExperimentsSearch, listExperimentsQueryOf } from "./experiments";
import { type FlagsSearch, listFlagsQueryOf } from "./flags";
import { listStaleFlagsQueryOf, type StaleFlagsSearch } from "./stale-flags";

/** The caller: who is signed in, and with which credential. */
export const meQuery = queryOptions<Me>({
  queryKey: ["me"],
  queryFn: () => unwrap(getMe()),
  staleTime: 60_000,
  retry: false,
});

/** The devices the caller is signed in on, the newest first, theirs marked current. */
export const mySessionsQuery = queryOptions<Session[]>({
  queryKey: ["me", "sessions"],
  queryFn: async () => (await unwrap(listMySessions())).items,
});

/** The caller's personal access tokens, revoked and expired ones included, the newest first. */
export const myTokensQuery = queryOptions<ApiToken[]>({
  queryKey: ["me", "tokens"],
  queryFn: async () => (await unwrap(listMyTokens())).items,
});

/** The ways this instance lets a member sign in, read before anyone is signed in. */
export const authMethodsQuery = queryOptions<AuthMethods>({
  queryKey: ["auth-methods"],
  queryFn: () => unwrap(getAuthMethods()),
  staleTime: 5 * 60_000,
});

/**
 * Every role a grant may hold, built-in and custom, with its permissions:
 * what the disabled-action sentence is built from.
 */
export const rolesQuery = queryOptions<Role[]>({
  queryKey: ["roles"],
  queryFn: async () => (await unwrap(listRoles())).items,
  staleTime: Infinity,
});

/** The projects the caller holds a grant in; every project for an instance admin. */
export const projectsQuery = queryOptions<Project[]>({
  queryKey: ["projects"],
  queryFn: async () => (await unwrap(listProjects())).items,
  staleTime: 30_000,
});

export function projectQuery(project: string) {
  return queryOptions<Project>({
    queryKey: ["projects", project],
    queryFn: () => unwrap(getProject({ path: { project } })),
    staleTime: 30_000,
    retry: false,
  });
}

/** The caller's grants and effective permissions in one project. */
export function projectMeQuery(project: string) {
  return queryOptions<ProjectMe>({
    queryKey: ["projects", project, "me"],
    queryFn: () => unwrap(getProjectMe({ path: { project } })),
    staleTime: 30_000,
    retry: false,
  });
}

/** A project's environments in their sort order. */
export function environmentsQuery(project: string) {
  return queryOptions<Environment[]>({
    queryKey: ["projects", project, "environments"],
    queryFn: async () => (await unwrap(listEnvironments({ path: { project } }))).items,
  });
}

/** One environment of a project. */
export function environmentQuery(project: string, environment: string) {
  return queryOptions<Environment>({
    queryKey: ["projects", project, "environments", environment],
    queryFn: () => unwrap(getEnvironment({ path: { project, environment } })),
  });
}

/** An environment's SDK keys, the oldest first, revoked and expired ones included. */
export function sdkKeysQuery(project: string, environment: string) {
  return queryOptions<SdkKey[]>({
    queryKey: ["projects", project, "environments", environment, "sdk-keys"],
    queryFn: async () => (await unwrap(listSdkKeys({ path: { project, environment } }))).items,
  });
}

/** The SDKs seen using an environment, the most recently seen first. */
export function sdkInventoryQuery(project: string, environment: string) {
  return queryOptions<SdkInventoryItem[]>({
    queryKey: ["projects", project, "environments", environment, "sdk-inventory"],
    queryFn: async () => (await unwrap(listSdkInventory({ path: { project, environment } }))).items,
  });
}

/** The attributes SDKs sent that the project has not declared or dismissed. */
export function discoveredAttributesQuery(project: string) {
  return queryOptions<DiscoveredAttribute[]>({
    queryKey: ["projects", project, "attributes", "discovered"],
    queryFn: async () => (await unwrap(listDiscoveredAttributes({ path: { project } }))).items,
  });
}

export function unitTypesQuery(project: string) {
  return queryOptions<UnitTypeListItem[]>({
    queryKey: ["projects", project, "unit-types"],
    queryFn: async () => (await unwrap(listUnitTypes({ path: { project } }))).items,
  });
}

/** What every read of a project's flags is cached under. */
export function flagsQueryKey(project: string) {
  return ["projects", project, "flags"] as const;
}

/**
 * One page of a project's flags for a search. The page shown stays while
 * the next one loads, so paging and filtering do not blank the table.
 */
export function flagsQuery(project: string, search: FlagsSearch) {
  return queryOptions<FlagList>({
    queryKey: [...flagsQueryKey(project), "list", search],
    queryFn: () => unwrap(listFlags({ path: { project }, query: listFlagsQueryOf(search) })),
    placeholderData: keepPreviousData,
  });
}

/** One flag with its configuration in every environment. */
export function flagQuery(project: string, flag: string) {
  return queryOptions<Flag>({
    queryKey: [...flagsQueryKey(project), "detail", flag],
    queryFn: () => unwrap(getFlag({ path: { project, flag } })),
    retry: false,
  });
}

/**
 * A flag's evaluations per UTC day in one environment over a range of
 * days. Kept under the flags' key, so that a write to the flag reads them
 * again with it; the range shown stays while another loads.
 */
export function flagEvaluationsQuery(
  project: string,
  flag: string,
  environment: string,
  range: { from: string; to: string },
) {
  return queryOptions<FlagEvaluations>({
    queryKey: [...flagsQueryKey(project), "evaluations", flag, environment, range.from, range.to],
    queryFn: () =>
      unwrap(getFlagEvaluations({ path: { project, flag, environment }, query: range })),
    placeholderData: keepPreviousData,
  });
}

/**
 * One page of a project's stale flags for a search. Kept under the flags'
 * key, since a write to a flag can make it stale or not; the page shown
 * stays while the next one loads.
 */
export function staleFlagsQuery(project: string, search: StaleFlagsSearch) {
  return queryOptions<StaleFlagList>({
    queryKey: [...flagsQueryKey(project), "stale", search],
    queryFn: () =>
      unwrap(listStaleFlags({ path: { project }, query: listStaleFlagsQueryOf(search) })),
    placeholderData: keepPreviousData,
  });
}

/** The events a project received per UTC day and type over a range of days, and the gaps in it. */
export function eventVolumeQuery(project: string, range: { from: string; to: string }) {
  return queryOptions<EventVolume>({
    queryKey: ["projects", project, "events", "volume", range.from, range.to],
    queryFn: () => unwrap(getEventVolume({ path: { project }, query: range })),
    placeholderData: keepPreviousData,
  });
}

/**
 * Every flag of a project that is not archived, read page by page: the
 * flags a prerequisite can name, with their variations.
 */
export function flagChoicesQuery(project: string) {
  return queryOptions<FlagList["items"]>({
    queryKey: [...flagsQueryKey(project), "choices"],
    queryFn: async () => {
      const items: FlagList["items"] = [];
      let cursor: string | undefined;
      do {
        const page = await unwrap(
          listFlags({ path: { project }, query: { limit: 200, ...(cursor ? { cursor } : {}) } }),
        );
        items.push(...page.items);
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      return items;
    },
  });
}

/** What every read of a project's experiments is cached under. */
export function experimentsQueryKey(project: string) {
  return ["projects", project, "experiments"] as const;
}

/**
 * One page of a project's experiments for a search. The page shown stays
 * while the next one loads, so paging and filtering do not blank the table.
 */
export function experimentsQuery(project: string, search: ExperimentsSearch) {
  return queryOptions<ExperimentList>({
    queryKey: [...experimentsQueryKey(project), "list", search],
    queryFn: () =>
      unwrap(listExperiments({ path: { project }, query: listExperimentsQueryOf(search) })),
    placeholderData: keepPreviousData,
  });
}

/**
 * Every experiment comparing a flag's variations, archived ones included,
 * read page by page: what a rule may serve, and the status of the one a
 * rule serves already.
 */
export function flagExperimentsQuery(project: string, flag: string) {
  return queryOptions<Experiment[]>({
    queryKey: [...experimentsQueryKey(project), "flag", flag],
    queryFn: async () => {
      const items: Experiment[] = [];
      let cursor: string | undefined;
      do {
        const page = await unwrap(
          listExperiments({
            path: { project },
            query: { flag, include_archived: true, limit: 200, ...(cursor ? { cursor } : {}) },
          }),
        );
        items.push(...page.items);
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      return items;
    },
  });
}

export function experimentQuery(project: string, experiment: string) {
  return queryOptions<Experiment>({
    queryKey: [...experimentsQueryKey(project), "detail", experiment],
    queryFn: () => unwrap(getExperiment({ path: { project, experiment } })),
    retry: false,
  });
}

/** What every read of a project's change requests is cached under. */
export function changeRequestsQueryKey(project: string) {
  return ["projects", project, "change-requests"] as const;
}

/**
 * One page of a project's change requests for a search, by the caller's
 * member identifier for the caller's own. The page shown stays while the
 * next one loads, so paging and filtering do not blank the table.
 */
export function changeRequestsQuery(
  project: string,
  search: ChangeRequestsSearch,
  member: string | undefined,
) {
  return queryOptions<ChangeRequestList>({
    queryKey: [...changeRequestsQueryKey(project), "list", search, member],
    queryFn: () =>
      unwrap(
        listChangeRequests({
          path: { project },
          query: listChangeRequestsQueryOf(search, member),
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

/** How many change requests the navigation counts before it says "and more". */
export const awaitingCountLimit = 99;

/**
 * The open change requests awaiting the caller's decision, at most one
 * more than the navigation counts, so it knows when there are more. Read
 * again every minute, so a new request is noticed without reloading.
 */
export function awaitingDecisionQuery(project: string) {
  return queryOptions<ChangeRequestList>({
    queryKey: [...changeRequestsQueryKey(project), "awaiting"],
    queryFn: () =>
      unwrap(
        listChangeRequests({
          path: { project },
          query: { awaiting_my_decision: true, limit: awaitingCountLimit + 1 },
        }),
      ),
    refetchInterval: 60_000,
  });
}

/**
 * The open and approved change requests of one flag in every
 * environment: what waits on its configurations.
 */
export function flagWaitingQuery(project: string, flag: string) {
  return queryOptions<ChangeRequest[]>({
    queryKey: [...changeRequestsQueryKey(project), "flag", flag],
    queryFn: async () => {
      const pages = await Promise.all(
        (["open", "approved"] as const).map((state) =>
          unwrap(listChangeRequests({ path: { project }, query: { flag, state, limit: 200 } })),
        ),
      );
      return pages.flatMap((page) => page.items);
    },
  });
}

export function changeRequestQuery(project: string, changeRequest: string) {
  return queryOptions<ChangeRequest>({
    queryKey: [...changeRequestsQueryKey(project), "detail", changeRequest],
    queryFn: () => unwrap(getChangeRequest({ path: { project, change_request: changeRequest } })),
    retry: false,
  });
}

/**
 * What an experiment's exposures in one environment came to, as the
 * exposure rollup last counted them: under the experiment's own key, so a
 * transition refreshes them with it.
 */
export function experimentExposuresQuery(project: string, experiment: string, environment: string) {
  return queryOptions<ExperimentExposures>({
    queryKey: [...experimentsQueryKey(project), "detail", experiment, "exposures", environment],
    queryFn: () =>
      unwrap(getExperimentExposures({ path: { project, experiment }, query: { environment } })),
  });
}

/**
 * Every alert an experiment has had, the most recently raised first, or
 * only its active ones: under the experiment's own key, so a transition
 * or an acknowledgement refreshes them with it.
 */
export function experimentAlertsQuery(
  project: string,
  experiment: string,
  { active }: { active?: true } = {},
) {
  return queryOptions<ExperimentAlertList>({
    queryKey: [
      ...experimentsQueryKey(project),
      "detail",
      experiment,
      "alerts",
      active ? "active" : "every",
    ],
    queryFn: () =>
      unwrap(
        listExperimentAlerts({ path: { project, experiment }, query: active ? { active } : {} }),
      ),
  });
}

/** An experiment's exposures per UTC day in one environment, every day it has. */
export function experimentTimelineQuery(project: string, experiment: string, environment: string) {
  return queryOptions<ExposureTimeline>({
    queryKey: [...experimentsQueryKey(project), "detail", experiment, "timeline", environment],
    queryFn: () =>
      unwrap(
        getExperimentExposureTimeline({ path: { project, experiment }, query: { environment } }),
      ),
  });
}

/** The latest of an experiment's conflicting units in one environment, at most 100. */
export function experimentConflictsQuery(project: string, experiment: string, environment: string) {
  return queryOptions<ExposureConflicts>({
    queryKey: [...experimentsQueryKey(project), "detail", experiment, "conflicts", environment],
    queryFn: () =>
      unwrap(getExperimentConflicts({ path: { project, experiment }, query: { environment } })),
  });
}

/**
 * How an experiment treats one unit ID. The answer follows from the
 * experiment's salts and allocation, so it is cached per version: an
 * edit or a transition asks again.
 */
export function experimentAssignmentQuery(
  project: string,
  experiment: Pick<Experiment, "key" | "version">,
  unitId: string,
) {
  return queryOptions<ExperimentAssignment>({
    queryKey: [
      ...experimentsQueryKey(project),
      "detail",
      experiment.key,
      "assignment",
      experiment.version,
      unitId,
    ],
    queryFn: () =>
      unwrap(
        getExperimentAssignment({
          path: { project, experiment: experiment.key },
          query: { unit_id: unitId },
        }),
      ),
    retry: false,
  });
}

/** What every read of a project's segments is cached under. */
export function segmentsQueryKey(project: string) {
  return ["projects", project, "segments"] as const;
}

/**
 * A project's segments, each with how many flags' rules name it: what a
 * rule's conditions choose from, and the segments list.
 */
export function segmentsQuery(project: string) {
  return queryOptions<SegmentListItem[]>({
    queryKey: [...segmentsQueryKey(project), "list"],
    queryFn: async () => (await unwrap(listSegments({ path: { project } }))).items,
  });
}

export function segmentQuery(project: string, segment: string) {
  return queryOptions<Segment>({
    queryKey: [...segmentsQueryKey(project), "detail", segment],
    queryFn: () => unwrap(getSegment({ path: { project, segment } })),
    retry: false,
  });
}

/** Every flag and environment whose rules name a segment. */
export function segmentUsagesQuery(project: string, segment: string) {
  return queryOptions<Reference[]>({
    queryKey: [...segmentsQueryKey(project), "usages", segment],
    queryFn: async () => (await unwrap(listSegmentUsages({ path: { project, segment } }))).items,
  });
}

/** A project's declared attributes, the standard ones among them. */
export function attributesQuery(project: string) {
  return queryOptions<AttributeListItem[]>({
    queryKey: ["projects", project, "attributes"],
    queryFn: async () => (await unwrap(listAttributes({ path: { project } }))).items,
  });
}

/** The members holding a grant in a project, with their grants. */
export function projectMembersQuery(project: string) {
  return queryOptions<ProjectMember[]>({
    queryKey: ["projects", project, "members"],
    queryFn: async () => (await unwrap(listProjectMembers({ path: { project } }))).items,
  });
}

/** A project's tokens, revoked and expired ones included, the newest first. */
export function projectTokensQuery(project: string) {
  return queryOptions<ProjectToken[]>({
    queryKey: ["projects", project, "tokens"],
    queryFn: async () => (await unwrap(listProjectTokens({ path: { project } }))).items,
  });
}

/** Every member of the instance, for an instance admin. */
export const membersQuery = queryOptions<Member[]>({
  queryKey: ["members"],
  queryFn: async () => (await unwrap(listMembers())).items,
});

/** The members a search of the members screen finds, by text and by status. */
export function memberSearchQuery(search: { q?: string; status?: MemberStatus }) {
  return queryOptions<Member[]>({
    queryKey: ["members", "search", search],
    queryFn: async () => (await unwrap(listMembers({ query: search }))).items,
    placeholderData: keepPreviousData,
  });
}

export function memberQuery(member: string) {
  return queryOptions<Member>({
    queryKey: ["members", member],
    queryFn: () => unwrap(getMember({ path: { member } })),
    retry: false,
  });
}

/** The devices a member is signed in on, for an instance admin. */
export function memberSessionsQuery(member: string) {
  return queryOptions<Session[]>({
    queryKey: ["members", member, "sessions"],
    queryFn: async () => (await unwrap(listMemberSessions({ path: { member } }))).items,
  });
}

/** A member's personal access tokens, revoked and expired ones included, for an instance admin. */
export function memberTokensQuery(member: string) {
  return queryOptions<ApiToken[]>({
    queryKey: ["members", member, "tokens"],
    queryFn: async () => (await unwrap(listMemberTokens({ path: { member } }))).items,
  });
}

/** The instance's rules for sessions, API tokens and passwords. */
export const authSettingsQuery = queryOptions<AuthSettings>({
  queryKey: ["instance", "auth-settings"],
  queryFn: () => unwrap(getAuthSettings()),
});

/** What an invitation's link opens, read before anyone is signed in. */
export function invitationQuery(secret: string) {
  return queryOptions<Invitation>({
    queryKey: ["invitations", secret],
    queryFn: () => unwrap(getInvitation({ path: { secret } })),
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  });
}

/** What every read of an audit log is cached under. */
export const auditQueryKey = ["audit"] as const;

/** Whose audit log: one project's, by its key, or the instance's. */
export type AuditScope = { project: string } | "instance";

/**
 * One page of an audit log for a search, of the server's size or of
 * `limit` entries. The page shown stays while the next one loads, so
 * paging and filtering do not blank the table.
 */
export function auditEntriesQuery(scope: AuditScope, search: AuditSearch, limit?: number) {
  return queryOptions<AuditEntryList>({
    queryKey: [...auditQueryKey, scope, search, limit],
    queryFn: () => {
      if (scope === "instance") {
        return unwrap(listAuditEntries({ query: { ...auditQueryOf(search), limit } }));
      }
      const { project: _instanceOnly, ...rest } = search;
      return unwrap(
        listProjectAuditEntries({
          path: { project: scope.project },
          query: { ...auditQueryOf(rest), limit },
        }),
      );
    },
    placeholderData: keepPreviousData,
  });
}

/**
 * The newest audit entries of one flag, by its identifier, which follows
 * it whatever its key, in one environment or in all:
 * what tells a member who made the version they ran into, and when.
 */
export function flagEntriesQuery(
  project: string,
  flagId: string,
  { environment, killSwitch, limit }: { environment?: string; killSwitch?: true; limit: number },
) {
  return queryOptions<AuditEntry[]>({
    queryKey: [...auditQueryKey, { project }, "flag", flagId, { environment, killSwitch, limit }],
    queryFn: async () =>
      (
        await unwrap(
          listProjectAuditEntries({
            path: { project },
            query: {
              entity_kind: "flag",
              entity_id: flagId,
              ...(environment ? { environment } : {}),
              ...(killSwitch ? { kill_switch: true } : {}),
              limit,
            },
          }),
        )
      ).items,
  });
}

/**
 * One page of a flag's history, by the flag's identifier: its audit
 * entries in every environment, newest first, from the page's cursor.
 * The page shown stays while the next one loads.
 */
export function flagHistoryQuery(project: string, flagId: string, cursor: string | undefined) {
  return queryOptions<AuditEntryList>({
    queryKey: [...auditQueryKey, { project }, "flag", flagId, "history", cursor],
    queryFn: () =>
      unwrap(
        listProjectAuditEntries({
          path: { project },
          query: { entity_kind: "flag", entity_id: flagId, ...(cursor ? { cursor } : {}) },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

/**
 * The newest audit entries of one entity of a project, such as a segment
 * or an environment, by its kind and identifier: who made the version a
 * save ran into, and when.
 */
export function entityEntriesQuery(project: string, kind: string, id: string, limit: number) {
  return queryOptions<AuditEntry[]>({
    queryKey: [...auditQueryKey, { project }, kind, id, { limit }],
    queryFn: async () =>
      (
        await unwrap(
          listProjectAuditEntries({
            path: { project },
            query: { entity_kind: kind, entity_id: id, limit },
          }),
        )
      ).items,
  });
}

export function projectAuditEntryQuery(project: string, entry: string) {
  return queryOptions<AuditEntry>({
    queryKey: [...auditQueryKey, { project }, "entry", entry],
    queryFn: () => unwrap(getProjectAuditEntry({ path: { project, entry } })),
    retry: false,
  });
}
