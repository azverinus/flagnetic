import type { AuditEntry } from "@flagnetic/api";
import { useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useMemo } from "react";

import type { Place } from "@/components/audit-log";
import { useEnvironmentPlace } from "@/components/project-audit";
import { ContextPanel } from "@/components/ui/context-panel";
import { PanelDateFacet, PanelFacet } from "@/components/ui/panel-facet";
import { PanelGroup } from "@/components/ui/context-panel";
import type { ToolbarChip } from "@/components/ui/toolbar";
import { InstancePanel } from "@/components/settings-panels";
import {
  actionLabel,
  actorKindLabel,
  actorOptions,
  auditActions,
  auditActorKinds,
  type AuditSearch,
  auditEntityKinds,
  entityKindLabel,
  type FilterOption,
  validateAuditSearch,
  validateProjectAuditSearch,
} from "@/lib/audit";
import { formatDay } from "@/i18n/format";
import { environmentLabel } from "@/lib/environments";
import {
  type AuditScope,
  auditEntriesQuery,
  environmentsQuery,
  meQuery,
  membersQuery,
  projectMembersQuery,
  projectsQuery,
} from "@/lib/queries";

/** The scope's own filter and column: the environment in a project, the project on the instance. */
export type AuditPlace = Place & { filter: "environment" | "project"; options: FilterOption[] };

/**
 * An audit log's filters and what they are written as: the address holds
 * them, the context panel edits them, and the toolbar's chips show them.
 * A change replaces the address rather than adding to the history, and
 * starts again from the newest page.
 */
export type AuditFilters = {
  scope: AuditScope;
  search: AuditSearch;
  /** The members the log is filtered by, whoever acted in the entries shown included. */
  actors: FilterOption[];
  place: AuditPlace;
  /** Replaces the search, the page included. */
  apply: (search: AuditSearch) => void;
  /** Changes some of the filters and returns to the newest page. */
  filter: (patch: Partial<AuditSearch>) => void;
  chips: ToolbarChip[];
  removeChip: (id: string) => void;
  clearFacets: () => void;
};

/** Everything a scope's filters share once its own actors, place and way to the address are known. */
function useAuditFiltersOf(
  scope: AuditScope,
  search: AuditSearch,
  known: FilterOption[],
  place: AuditPlace,
  apply: (search: AuditSearch) => void,
): AuditFilters {
  const { i18n, t } = useLingui();
  // The same read as the log's own, so it is made once.
  const page = useQuery(auditEntriesQuery(scope, search));
  const items = page.data?.items;
  const actors = useMemo(
    () => actorOptions(i18n, known, items ?? ([] as AuditEntry[]), search.actor),
    [i18n, known, items, search.actor],
  );
  const filter = (patch: Partial<AuditSearch>) => apply({ ...search, ...patch, cursor: undefined });

  const chips: ToolbarChip[] = [];
  const chip = (id: string, name: string, value: string) =>
    chips.push({ id, label: t`${name}: ${value}` });
  const optionLabel = (options: FilterOption[], value: string) =>
    options.find((option) => option.value === value)?.label ?? value;
  if (search.action) chip("action", t`Action`, actionLabel(i18n, search.action));
  if (search.entity_kind) chip("entity_kind", t`Entity`, entityKindLabel(i18n, search.entity_kind));
  if (search.actor) chip("actor", t`Member`, optionLabel(actors, search.actor));
  if (search.actor_kind) chip("actor_kind", t`Actor kind`, actorKindLabel(i18n, search.actor_kind));
  const placed = search[place.filter];
  if (placed) chip(place.filter, place.label, optionLabel(place.options, placed));
  if (search.from) chip("from", t`From`, formatDay(i18n, search.from));
  if (search.to) chip("to", t`To`, formatDay(i18n, search.to));

  return {
    scope,
    search,
    actors,
    place,
    apply,
    filter,
    chips,
    removeChip: (id) => filter({ [id]: undefined }),
    // The search is a toolbar field of its own, not a facet: clearing the
    // facets keeps it.
    clearFacets: () => apply(search.q ? { q: search.q } : {}),
  };
}

/**
 * The open project's audit log filters. They read the address of the
 * log's route and of its entry's, whichever is open.
 */
export function useProjectAuditFilters(): AuditFilters {
  const { i18n } = useLingui();
  const { project } = useParams({ strict: false }) as { project: string };
  const address = useSearch({ strict: false }) as Record<string, unknown>;
  const search = useMemo(() => validateProjectAuditSearch(address), [address]);
  const navigate = useNavigate();
  const { data: me } = useSuspenseQuery(meQuery);
  const members = useQuery(projectMembersQuery(project));
  // An instance admin acts in every project without a grant; reading
  // every member, which only they may, names them all in the filter.
  const instanceMembers = useQuery({
    ...membersQuery,
    enabled: me.member?.instance_admin === true,
  });
  const environments = useQuery(environmentsQuery(project));
  const known = useMemo(
    () => [
      ...(members.data ?? []).map((member) => ({
        value: member.member,
        label: member.display_name || member.email,
      })),
      ...(instanceMembers.data ?? []).map((member) => ({
        value: member.id,
        label: member.display_name || member.email,
      })),
    ],
    [members.data, instanceMembers.data],
  );
  const environmentPlace = useEnvironmentPlace(project);
  const place = useMemo<AuditPlace>(
    () => ({
      ...environmentPlace,
      filter: "environment",
      options: (environments.data ?? []).map((environment) => ({
        value: environment.id,
        label: environmentLabel(i18n, environment),
      })),
    }),
    [environmentPlace, environments.data, i18n],
  );
  return useAuditFiltersOf(
    { project },
    search,
    known,
    place,
    (next) =>
      void navigate({
        to: "/projects/$project/audit",
        params: { project },
        search: next,
        replace: true,
      }),
  );
}

/** The instance's audit log filters, read from the address of the log's route and of its entry's. */
export function useInstanceAuditFilters(): AuditFilters {
  const { t } = useLingui();
  const address = useSearch({ strict: false }) as Record<string, unknown>;
  const search = useMemo(() => validateAuditSearch(address), [address]);
  const navigate = useNavigate();
  const members = useQuery(membersQuery);
  const projects = useQuery(projectsQuery);
  const known = useMemo(
    () =>
      (members.data ?? []).map((member) => ({
        value: member.id,
        label: member.display_name || member.email,
      })),
    [members.data],
  );
  const place = useMemo<AuditPlace>(() => {
    const names = new Map((projects.data ?? []).map((project) => [project.id, project.name]));
    const instance = t({
      message: "Instance",
      comment: "An audit entry that belongs to no project.",
    });
    return {
      filter: "project",
      label: t`Project`,
      options: (projects.data ?? []).map((project) => ({ value: project.id, label: project.name })),
      // A deleted project's entries stay; they show its identifier.
      of: (entry) =>
        entry.project_id ? (names.get(entry.project_id) ?? entry.project_id) : instance,
    };
  }, [projects.data, t]);
  return useAuditFiltersOf(
    "instance",
    search,
    known,
    place,
    (next) => void navigate({ to: "/admin/audit", search: next, replace: true }),
  );
}

/** The facets of an audit log, each applied at once as the search parameter the address already had. */
function AuditFacets({ filters }: { filters: AuditFilters }) {
  const { i18n, t } = useLingui();
  const { search, filter, actors, place } = filters;
  return (
    <PanelGroup label={t`Filters`}>
      <PanelFacet
        label={t`Action`}
        value={search.action}
        anyLabel={t`Any`}
        options={auditActions.map((action) => ({
          value: action,
          label: actionLabel(i18n, action),
        }))}
        onChange={(action) => filter({ action })}
      />
      <PanelFacet
        label={t`Entity`}
        value={search.entity_kind}
        anyLabel={t`Any`}
        options={auditEntityKinds.map((kind) => ({
          value: kind,
          label: entityKindLabel(i18n, kind),
        }))}
        onChange={(entity_kind) => filter({ entity_kind })}
      />
      <PanelFacet
        label={t`Member`}
        value={search.actor}
        anyLabel={t`Any`}
        options={actors}
        onChange={(actor) => filter({ actor })}
      />
      <PanelFacet
        label={t`Actor kind`}
        value={search.actor_kind}
        anyLabel={t`Any`}
        options={auditActorKinds.map((kind) => ({
          value: kind,
          label: actorKindLabel(i18n, kind),
        }))}
        onChange={(kind) => filter({ actor_kind: auditActorKinds.find((known) => known === kind) })}
      />
      <PanelFacet
        label={place.label}
        value={search[place.filter]}
        anyLabel={t`Any`}
        options={place.options}
        onChange={(value) => filter({ [place.filter]: value })}
      />
      <PanelDateFacet label={t`From`} value={search.from} onChange={(from) => filter({ from })} />
      <PanelDateFacet label={t`To`} value={search.to} onChange={(to) => filter({ to })} />
    </PanelGroup>
  );
}

/** The project audit log's panel: its facets alone. */
export function ProjectAuditPanel() {
  const { t } = useLingui();
  const filters = useProjectAuditFilters();
  return (
    <ContextPanel label={t`Filters`} startsOpenWide>
      <AuditFacets filters={filters} />
    </ContextPanel>
  );
}

/** The instance audit log's panel: the instance area's links, then the log's facets under them. */
export function InstanceAuditPanel() {
  const filters = useInstanceAuditFilters();
  return (
    <InstancePanel startsOpenWide>
      <AuditFacets filters={filters} />
    </InstancePanel>
  );
}
