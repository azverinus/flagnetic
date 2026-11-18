import type { ChangeRequestKind, ChangeRequestState } from "@flagnetic/api";
import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";

import { ContextPanel, PanelGroup, PanelLink } from "@/components/ui/context-panel";
import { PanelFacet } from "@/components/ui/panel-facet";
import type { ToolbarChip } from "@/components/ui/toolbar";
import {
  changeRequestKindName,
  changeRequestKinds,
  type ChangeRequestsSearch,
  changeRequestStateName,
  changeRequestStates,
  type ChangeRequestsTab,
} from "@/lib/change-requests";
import { environmentLabel } from "@/lib/environments";
import { environmentsQuery, flagChoicesQuery, projectMembersQuery } from "@/lib/queries";

const route = "/_signed-in/projects/$project/change-requests/";

/**
 * The change requests list's facets and what they are written as: the
 * address holds them, the panel and the toolbar's chips show them, and a
 * change replaces the address rather than adding to the history, starting
 * again from the first page.
 */
export function useChangeRequestsFilters() {
  const { i18n, t } = useLingui();
  const { project } = useParams({ from: route });
  const search = useSearch({ from: route });
  const navigate = useNavigate();
  const environments = useQuery(environmentsQuery(project));
  const members = useQuery(projectMembersQuery(project));

  const apply = (next: ChangeRequestsSearch) =>
    void navigate({
      to: "/projects/$project/change-requests",
      params: { project },
      search: next,
      replace: true,
    });
  const filter = (patch: Partial<ChangeRequestsSearch>) =>
    apply({ ...search, ...patch, cursor: undefined });

  const chips: ToolbarChip[] = [];
  // Only open change requests await a decision, so the view fixes the state.
  if (search.state && search.tab !== "awaiting") {
    const state = changeRequestStateName(i18n, search.state);
    chips.push({ id: "state", label: t`State: ${state}` });
  }
  if (search.kind) {
    const kind = changeRequestKindName(i18n, search.kind);
    chips.push({ id: "kind", label: t`Kind: ${kind}` });
  }
  if (search.flag) {
    const flag = search.flag;
    chips.push({ id: "flag", label: t`Flag: ${flag}` });
  }
  if (search.environment) {
    const known = environments.data?.find((env) => env.key === search.environment);
    const environment = known ? environmentLabel(i18n, known) : search.environment;
    chips.push({ id: "environment", label: t`Environment: ${environment}` });
  }
  if (search.scheduled !== undefined) {
    const schedule = search.scheduled ? t`Scheduled` : t`Not scheduled`;
    chips.push({ id: "scheduled", label: t`Schedule: ${schedule}` });
  }
  // The caller's own are those the caller wrote: the view fixes the author.
  if (search.author && search.tab !== "mine") {
    const author =
      members.data?.find((member) => member.member === search.author)?.display_name ??
      search.author;
    chips.push({ id: "author", label: t`Author: ${author}` });
  }

  const patches: Record<string, Partial<ChangeRequestsSearch>> = {
    state: { state: undefined },
    kind: { kind: undefined },
    flag: { flag: undefined },
    environment: { environment: undefined },
    scheduled: { scheduled: undefined },
    author: { author: undefined },
  };

  return {
    project,
    search,
    tab: (search.tab ?? "all") as ChangeRequestsTab,
    environments: environments.data ?? [],
    members: members.data ?? [],
    apply,
    filter,
    chips,
    removeChip: (id: string) => filter(patches[id] ?? {}),
    // The view is which change requests are shown, not a facet: clearing
    // the facets keeps it.
    clearFacets: () => apply({ tab: search.tab }),
  };
}

/** The scheduled facet's choices, as the address holds them. */
const scheduleChoices = ["scheduled", "unscheduled"] as const;

/**
 * The change requests list's panel: its views, as links writing the `tab`
 * search parameter, then its facets, each applied at once as the search
 * parameter the address already had for it.
 */
export function ChangeRequestsPanel() {
  const { i18n, t } = useLingui();
  const { project, search, tab, environments, members, filter } = useChangeRequestsFilters();
  const flags = useQuery(flagChoicesQuery(project));
  const flagKeys = (flags.data ?? []).map((flag) => flag.key);
  const memberIds = members.map((member) => member.member);

  // A view moves to its first page and drops the facet it fixes: only open
  // change requests await a decision, and the caller's own are those the
  // caller wrote.
  const view = (next: ChangeRequestsTab): ChangeRequestsSearch => ({
    ...search,
    cursor: undefined,
    tab: next === "all" ? undefined : next,
    ...(next === "awaiting" ? { state: undefined } : {}),
    ...(next === "mine" ? { author: undefined } : {}),
  });

  return (
    <ContextPanel label={t`Views and filters`} startsOpenWide>
      <PanelGroup label={t`Views`}>
        <PanelLink
          to="/projects/$project/change-requests"
          params={{ project }}
          search={view("awaiting")}
          current={tab === "awaiting"}
        >
          {t`Awaiting my decision`}
        </PanelLink>
        <PanelLink
          to="/projects/$project/change-requests"
          params={{ project }}
          search={view("mine")}
          current={tab === "mine"}
        >
          {t`Mine`}
        </PanelLink>
        <PanelLink
          to="/projects/$project/change-requests"
          params={{ project }}
          search={view("all")}
          current={tab === "all"}
        >
          {t`All`}
        </PanelLink>
      </PanelGroup>
      <PanelGroup label={t`Filters`}>
        {tab === "awaiting" ? null : (
          <PanelFacet
            label={t`State`}
            value={search.state}
            anyLabel={t`Any state`}
            options={changeRequestStates.map((state: ChangeRequestState) => ({
              value: state,
              label: changeRequestStateName(i18n, state),
            }))}
            onChange={(value) =>
              filter({ state: changeRequestStates.find((state) => state === value) })
            }
          />
        )}
        <PanelFacet
          label={t`Kind`}
          value={search.kind}
          anyLabel={t`Any kind`}
          options={changeRequestKinds.map((kind: ChangeRequestKind) => ({
            value: kind,
            label: changeRequestKindName(i18n, kind),
          }))}
          onChange={(value) => filter({ kind: changeRequestKinds.find((kind) => kind === value) })}
        />
        <PanelFacet
          label={t`Flag`}
          value={search.flag}
          anyLabel={t`Any flag`}
          options={[
            // A flag the address names that is not among those read: an
            // archived one, whose change requests are still listed.
            ...(search.flag && !flagKeys.includes(search.flag)
              ? [{ value: search.flag, label: search.flag }]
              : []),
            ...flagKeys.map((key) => ({ value: key, label: key })),
          ]}
          onChange={(flag) => filter({ flag })}
        />
        <PanelFacet
          label={t`Environment`}
          value={search.environment}
          anyLabel={t`Any environment`}
          options={environments.map((env) => ({
            value: env.key,
            label: environmentLabel(i18n, env),
          }))}
          onChange={(environment) => filter({ environment })}
        />
        <PanelFacet
          label={t`Schedule`}
          value={
            search.scheduled === undefined
              ? undefined
              : search.scheduled
                ? "scheduled"
                : "unscheduled"
          }
          anyLabel={t`Any schedule`}
          options={scheduleChoices.map((choice) => ({
            value: choice,
            label: choice === "scheduled" ? t`Scheduled` : t`Not scheduled`,
          }))}
          onChange={(value) =>
            filter({ scheduled: value === undefined ? undefined : value === "scheduled" })
          }
        />
        {tab === "mine" ? null : (
          <PanelFacet
            label={t`Author`}
            value={search.author}
            anyLabel={t`Any author`}
            options={[
              // An author the address names who is not among the project's
              // members: a project token, or a member whose grant is gone.
              ...(search.author && !memberIds.includes(search.author)
                ? [{ value: search.author, label: search.author }]
                : []),
              ...members.map((member) => ({ value: member.member, label: member.display_name })),
            ]}
            onChange={(author) => filter({ author })}
          />
        )}
      </PanelGroup>
    </ContextPanel>
  );
}
