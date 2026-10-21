import type { ProjectMember } from "@flagnetic/api";
import { useQuery, useSuspenseQuery, type UseQueryResult } from "@tanstack/react-query";
import { useMemo } from "react";

import { type GrantCandidates, grantCandidates, memberNames } from "./project-members";
import { meQuery, membersQuery, projectMembersQuery } from "./queries";

/** A read that failed, with a way to try it again. */
export type ReadFailure = { error: unknown; retry: () => void };

export type KnownMembers = {
  /** The members holding a grant in the project, with their grants. */
  projectMembers: UseQueryResult<ProjectMember[]>;
  /** Whether the caller is an instance admin, who reads every member of the instance. */
  instanceAdmin: boolean;
  /**
   * The names of the members the caller can read, by identifier, for
   * saying who made a grant or a token; none until every list they come
   * from is read or one of them failed, so nobody is named as a member
   * without a grant here while their name is still on its way.
   */
  names: ReadonlyMap<string, string> | undefined;
  /** The first of those reads that failed, while one has. */
  failure: ReadFailure | undefined;
  /** The members a role can be granted to, as far as they are read. */
  candidates: GrantCandidates;
};

/**
 * The members the caller can know in a project: those holding a grant
 * there and, for an instance admin, every member of the instance. Only an
 * instance admin may read every member, so they grant a role to anyone
 * and see whoever made a grant or a token named; anyone else knows the
 * project's own members only.
 */
export function useKnownMembers(project: string): KnownMembers {
  const { data: me } = useSuspenseQuery(meQuery);
  const instanceAdmin = me.member?.instance_admin === true;
  const projectMembers = useQuery(projectMembersQuery(project));
  const instanceMembers = useQuery({ ...membersQuery, enabled: instanceAdmin });

  const reads = instanceAdmin ? [projectMembers, instanceMembers] : [projectMembers];
  const failed = reads.find((read) => read.isError);
  const failure = failed && { error: failed.error, retry: () => void failed.refetch() };
  const settled = failed !== undefined || reads.every((read) => read.data !== undefined);

  const known = instanceAdmin ? instanceMembers.data : undefined;
  const names = useMemo(
    () => (settled ? memberNames(projectMembers.data ?? [], known) : undefined),
    [settled, projectMembers.data, known],
  );

  // The candidates come from one list: the instance's for an instance
  // admin, the project's for anyone else.
  const source = instanceAdmin ? instanceMembers : projectMembers;
  const candidates: GrantCandidates = source.isError
    ? { status: "error", error: source.error, retry: () => void source.refetch() }
    : source.data === undefined
      ? { status: "pending" }
      : { status: "success", members: grantCandidates(projectMembers.data ?? [], known) };

  return { projectMembers, instanceAdmin, names, failure, candidates };
}
