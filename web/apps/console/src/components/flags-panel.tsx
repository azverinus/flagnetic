import { type FlagType, type StaleReason, zFlagType } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useMemo } from "react";

import { ContextPanel, PanelGroup, PanelLink } from "@/components/ui/context-panel";
import {
  type FacetOption,
  PanelChecklist,
  PanelFacet,
  PanelTextFacet,
} from "@/components/ui/panel-facet";
import type { ToolbarChip } from "@/components/ui/toolbar";
import { environmentLabel } from "@/lib/environments";
import {
  comparedEnvironments,
  type FlagStateFilter,
  flagStateFilters,
  flagTypeName,
  flagTypes,
  type FlagsSearch,
  resolveEnvironment,
} from "@/lib/flags";
import { lastEnvironment } from "@/lib/preferences";
import { environmentsQuery } from "@/lib/queries";
import {
  staleReasonDescription,
  staleReasonName,
  reasonsInOrder,
  staleReasons,
  type StaleFlagsSearch,
} from "@/lib/stale-flags";
import { useKnownMembers } from "@/lib/use-known-members";

const flagsRoute = "/_signed-in/projects/$project/flags/";
const staleRoute = "/_signed-in/projects/$project/stale-flags";

const stateNames = {
  on: msg`On`,
  off: msg`Off`,
  rules: msg`Has rules`,
  rollout: msg`Has a rollout`,
  experiment: msg`Serves an experiment`,
} satisfies Record<FlagStateFilter, unknown>;

/**
 * The flags list's facets and what they are written as: the address holds
 * them, the panel and the toolbar's chips show them, and a change replaces
 * the address rather than adding to the history, starting again from the
 * first page. The environment the list shows is the one the address names,
 * else the one this browser showed last in the project, else the first.
 */
export function useFlagsFilters() {
  const { i18n, t } = useLingui();
  const { project } = useParams({ from: flagsRoute });
  const asked = useSearch({ from: flagsRoute });
  const navigate = useNavigate();
  const { names: owners } = useKnownMembers(project);
  const environments = useQuery(environmentsQuery(project));
  const environment = environments.data
    ? resolveEnvironment(
        environments.data.map((env) => env.key),
        asked.environment,
        lastEnvironment(project),
      )
    : asked.environment;
  const search: FlagsSearch = { ...asked, environment };

  const apply = (next: FlagsSearch) =>
    void navigate({
      to: "/projects/$project/flags",
      params: { project },
      search: next,
      replace: true,
    });
  const filter = (patch: Partial<FlagsSearch>) => apply({ ...search, ...patch, cursor: undefined });

  const environmentName = (key: string | undefined): string => {
    const known = environments.data?.find((env) => env.key === key);
    return known ? environmentLabel(i18n, known) : (key ?? "");
  };

  const chips: ToolbarChip[] = [];
  if (search.state) {
    const state = i18n._(stateNames[search.state]);
    const where = environmentName(search.environment);
    chips.push({ id: "state", label: t`State in ${where}: ${state}` });
  }
  if (search.type) {
    const type = flagTypeName(i18n, search.type);
    chips.push({ id: "type", label: t`Type: ${type}` });
  }
  // Once the members are read: until then the owner's name is not known.
  if (search.owner && owners) {
    const owner = owners.get(search.owner) ?? t`A member without a grant here`;
    chips.push({ id: "owner", label: t`Owner: ${owner}` });
  }
  if (search.tag) {
    const tag = search.tag;
    chips.push({ id: "tag", label: t`Tag: ${tag}` });
  }
  const compared = comparedEnvironments(search);
  if (compared) {
    const [first, second] = compared.map(environmentName) as [string, string];
    chips.push({ id: "differs", label: t`Differs between ${first} and ${second}` });
  }

  const patches: Record<string, Partial<FlagsSearch>> = {
    state: { state: undefined },
    type: { type: undefined },
    owner: { owner: undefined },
    tag: { tag: undefined },
    differs: { differs_between: undefined },
  };

  return {
    project,
    search,
    environmentsRead: environments,
    environments: environments.data ?? [],
    owners,
    apply,
    filter,
    chips,
    removeChip: (id: string) => filter(patches[id] ?? {}),
    // The environment, the order and the view are how the flags are shown,
    // not which: clearing the facets keeps all three.
    clearFacets: () =>
      apply({ environment, sort: search.sort, ...(search.archived ? { archived: true } : {}) }),
  };
}

/** The flag views of the list, the one the screen shows marked as current. */
function FlagViews({
  current,
  environment,
}: {
  current: "all" | "stale" | "archived";
  environment: string | undefined;
}) {
  const { t } = useLingui();
  const { project } = useParams({ strict: false });
  if (project === undefined) return null;
  return (
    <PanelGroup label={t`Views`}>
      <PanelLink
        to="/projects/$project/flags"
        params={{ project }}
        search={{ environment }}
        current={current === "all"}
      >
        {t`All flags`}
      </PanelLink>
      <PanelLink
        to="/projects/$project/stale-flags"
        params={{ project }}
        current={current === "stale"}
      >
        {t`Stale flags`}
      </PanelLink>
      <PanelLink
        to="/projects/$project/flags"
        params={{ project }}
        search={{ environment, archived: true }}
        current={current === "archived"}
      >
        {t`Archived`}
      </PanelLink>
    </PanelGroup>
  );
}

function typeOptions(i18n: I18n): FacetOption[] {
  return flagTypes.map((type) => ({ value: type, label: flagTypeName(i18n, type) }));
}

/**
 * The flags list's panel: its views, then its facets, each applied at once
 * as the search parameter the address already had for it.
 */
export function FlagsPanel() {
  const { i18n, t } = useLingui();
  const { search, environments, owners, filter } = useFlagsFilters();

  const ownerOptions = useMemo(() => {
    const options = [...(owners ?? new Map<string, string>())]
      .map(([value, label]) => ({ value, label }))
      .sort((a, b) => a.label.localeCompare(b.label, i18n.locale));
    // An owner the address names who is not among the members read: one
    // without a grant here, once the members are read.
    return search.owner && owners && !owners.has(search.owner)
      ? [{ value: search.owner, label: t`A member without a grant here` }, ...options]
      : options;
  }, [owners, search.owner, i18n.locale, t]);

  // Every pair of environments once, in the environments' order.
  const pairs = environments.flatMap((a, i) =>
    environments.slice(i + 1).map((b) => {
      const [first, second] = [a.name, b.name];
      return { value: `${a.key},${b.key}`, label: t`${first} and ${second}` };
    }),
  );
  // The pair the address names, in whichever order, as the panel offers it.
  const named = comparedEnvironments(search);
  const compared = named
    ? (pairs.find((pair) => named.every((key) => pair.value.split(",").includes(key)))?.value ??
      named.join(","))
    : undefined;

  return (
    <ContextPanel label={t`Views and filters`} startsOpenWide>
      <FlagViews current={search.archived ? "archived" : "all"} environment={search.environment} />
      <PanelGroup label={t`State in`}>
        <PanelFacet
          label={t`Environment`}
          value={search.environment}
          disabled={environments.length === 0}
          options={environments.map((env) => ({
            value: env.key,
            label: environmentLabel(i18n, env),
          }))}
          onChange={(environment) => filter({ environment })}
        />
        <PanelFacet
          label={t`State`}
          value={search.state}
          anyLabel={t`Any state`}
          options={flagStateFilters.map((state) => ({
            value: state,
            label: i18n._(stateNames[state]),
          }))}
          onChange={(value) => filter({ state: flagStateFilters.find((state) => state === value) })}
        />
      </PanelGroup>
      <PanelGroup label={t`Filters`}>
        <PanelFacet
          label={t`Type`}
          value={search.type}
          anyLabel={t`Any type`}
          options={typeOptions(i18n)}
          onChange={(value) =>
            filter({ type: zFlagType.safeParse(value).data as FlagType | undefined })
          }
        />
        <PanelFacet
          label={t`Owner`}
          value={search.owner}
          anyLabel={t`Any owner`}
          options={ownerOptions}
          onChange={(owner) => filter({ owner })}
        />
        <PanelTextFacet
          label={t`Tag`}
          placeholder={t`Tag`}
          value={search.tag ?? ""}
          onChange={(tag) => filter({ tag: tag.trim() || undefined })}
        />
        <PanelFacet
          label={t`Differs between`}
          hint={t`In what SDKs receive`}
          value={compared}
          anyLabel={t`Any environments`}
          disabled={pairs.length === 0}
          options={pairs}
          onChange={(differs_between) => filter({ differs_between })}
        />
      </PanelGroup>
    </ContextPanel>
  );
}

/** Only the reasons the server knows, in the report's order, none as no filter. */
function reasonsAsked(reasons: readonly string[]): StaleReason[] | undefined {
  return reasonsInOrder(staleReasons.filter((reason) => reasons.includes(reason)));
}

/** The stale flags screen's facets: the reasons in the address, and what each means. */
export function useStaleFlagsFilters() {
  const { i18n, t } = useLingui();
  const { project } = useParams({ from: staleRoute });
  const search = useSearch({ from: staleRoute });
  const navigate = useNavigate();
  const apply = (next: StaleFlagsSearch) =>
    void navigate({
      to: "/projects/$project/stale-flags",
      params: { project },
      search: next,
      replace: true,
    });
  const chips: ToolbarChip[] = (search.reason ?? []).map((reason) => {
    const name = staleReasonName(i18n, reason);
    return { id: reason, label: t`Stale for: ${name}` };
  });
  return {
    project,
    search,
    apply,
    chips,
    setReasons: (reasons: readonly string[]) => apply({ reason: reasonsAsked(reasons) }),
    removeChip: (id: string) =>
      apply({ reason: reasonsAsked(search.reason?.filter((reason) => reason !== id) ?? []) }),
    clearFacets: () => apply({}),
  };
}

/** The stale flags screen's panel: the same views with its own current, and its reasons as facets. */
export function StaleFlagsPanel() {
  const { i18n, t } = useLingui();
  const { search, setReasons } = useStaleFlagsFilters();
  return (
    <ContextPanel label={t`Views and filters`} startsOpenWide>
      <FlagViews current="stale" environment={undefined} />
      <PanelGroup label={t`Filters`}>
        <PanelChecklist
          label={t`Stale for`}
          hint={t`Show flags stale for any of these reasons; none checked shows every stale flag`}
          values={search.reason ?? []}
          onChange={setReasons}
          options={staleReasons.map((reason) => ({
            value: reason,
            label: staleReasonName(i18n, reason),
            description: staleReasonDescription(i18n, reason),
          }))}
        />
      </PanelGroup>
    </ContextPanel>
  );
}
