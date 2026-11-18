import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useMemo } from "react";

import { ContextPanel, PanelGroup } from "@/components/ui/context-panel";
import { PanelChecklist, PanelFacet, PanelTextFacet } from "@/components/ui/panel-facet";
import type { ToolbarChip } from "@/components/ui/toolbar";
import {
  experimentStatuses,
  experimentStatusName,
  type ExperimentsSearch,
} from "@/lib/experiments";
import { flagChoicesQuery } from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";

const route = "/_signed-in/projects/$project/experiments/";

/**
 * The experiments list's facets and what they are written as: the address
 * holds them, the panel and the toolbar's chips show them, and a change
 * replaces the address rather than adding to the history, starting again
 * from the first page.
 */
export function useExperimentsFilters() {
  const { i18n, t } = useLingui();
  const { project } = useParams({ from: route });
  const search = useSearch({ from: route });
  const navigate = useNavigate();
  const { names: owners } = useKnownMembers(project);

  const apply = (next: ExperimentsSearch) =>
    void navigate({
      to: "/projects/$project/experiments",
      params: { project },
      search: next,
      replace: true,
    });
  const filter = (patch: Partial<ExperimentsSearch>) =>
    apply({ ...search, ...patch, cursor: undefined });

  const chips: ToolbarChip[] = [];
  if (search.status) {
    const status = experimentStatusName(i18n, search.status);
    chips.push({ id: "status", label: t`Status: ${status}` });
  }
  if (search.flag) {
    const flag = search.flag;
    chips.push({ id: "flag", label: t`Flag: ${flag}` });
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
  if (search.archived) chips.push({ id: "archived", label: t`Archived experiments included` });
  if (search.past_expected_end)
    chips.push({ id: "past_expected_end", label: t`Past expected end` });
  if (search.awaiting_code_removal) {
    chips.push({ id: "awaiting_code_removal", label: t`Awaiting code removal` });
  }

  const patches: Record<string, Partial<ExperimentsSearch>> = {
    status: { status: undefined },
    flag: { flag: undefined },
    owner: { owner: undefined },
    tag: { tag: undefined },
    archived: { archived: undefined },
    past_expected_end: { past_expected_end: undefined },
    awaiting_code_removal: { awaiting_code_removal: undefined },
  };

  return {
    project,
    search,
    owners,
    apply,
    filter,
    chips,
    removeChip: (id: string) => filter(patches[id] ?? {}),
    clearFacets: () => apply({}),
  };
}

/** The checklist options that are switches of the address, by the parameter they write. */
type Switch = "archived" | "past_expected_end" | "awaiting_code_removal";

/** The experiments list's panel: each facet applied at once as the search parameter the address already had for it. */
export function ExperimentsPanel() {
  const { i18n, t } = useLingui();
  const { project, search, owners, filter } = useExperimentsFilters();
  const flags = useQuery(flagChoicesQuery(project));
  const flagKeys = (flags.data ?? []).map((flag) => flag.key);

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

  // The switches the address holds, as the checklist's values.
  const held = (names: readonly Switch[]) => names.filter((name) => search[name] === true);
  const setSwitches = (names: readonly Switch[]) => (values: string[]) =>
    filter(
      Object.fromEntries(names.map((name) => [name, values.includes(name) ? true : undefined])),
    );

  return (
    <ContextPanel label={t`Filter the experiments`} startsOpenWide>
      <PanelGroup label={t`Filters`}>
        <PanelFacet
          label={t`Status`}
          value={search.status}
          anyLabel={t`Any status`}
          options={experimentStatuses.map((status) => ({
            value: status,
            label: experimentStatusName(i18n, status),
          }))}
          onChange={(value) =>
            filter({ status: experimentStatuses.find((status) => status === value) })
          }
        />
        <PanelFacet
          label={t`Flag`}
          value={search.flag}
          anyLabel={t`Any flag`}
          options={[
            // A flag the address names that is not among those read: an
            // archived one, whose experiments are still listed.
            ...(search.flag && !flagKeys.includes(search.flag)
              ? [{ value: search.flag, label: search.flag }]
              : []),
            ...flagKeys.map((key) => ({ value: key, label: key })),
          ]}
          onChange={(flag) => filter({ flag })}
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
      </PanelGroup>
      <PanelGroup label={t`Show`}>
        <PanelChecklist
          label={t`Archived`}
          values={held(["archived"])}
          onChange={setSwitches(["archived"])}
          options={[{ value: "archived", label: t`Include archived experiments` }]}
        />
        <PanelChecklist
          label={t`Needs attention`}
          values={held(["past_expected_end", "awaiting_code_removal"])}
          onChange={setSwitches(["past_expected_end", "awaiting_code_removal"])}
          options={[
            { value: "past_expected_end", label: t`Past expected end` },
            { value: "awaiting_code_removal", label: t`Awaiting code removal` },
          ]}
        />
      </PanelGroup>
    </ContextPanel>
  );
}
