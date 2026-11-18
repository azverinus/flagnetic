import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useParams, useSearch } from "@tanstack/react-router";

import { Can } from "@/components/can";
import { Badge } from "@/components/ui/badge";
import { ContextPanel, PanelGroup, PanelLink } from "@/components/ui/context-panel";
import { PanelButton, PanelEnvironment, PanelFact, PanelFacts } from "@/components/ui/panel-parts";
import { formatDateTime } from "@/i18n/format";
import { runFlagAction } from "@/lib/flag-actions";
import { type FlagDetailSearch } from "@/lib/flag-detail";
import { flagTypeName } from "@/lib/flags";
import { environmentsQuery, flagQuery } from "@/lib/queries";
import { useNameOf } from "@/lib/use-known-members";

const flagRoute = "/_signed-in/projects/$project/flags/$flag";

/**
 * The flag's context panel: its environments, each with the lamp of what
 * the flag does there, so the state everywhere is seen from the one open;
 * the flag's other pages; and the facts of its definition. Everything it
 * shows comes from the flag and the environments the page reads.
 */
export function FlagPanel() {
  const { project, flag } = useParams({ from: flagRoute });
  const search = useSearch({ from: flagRoute });
  return <FlagPanelContent project={project} flagKey={flag} search={search} />;
}

/** The panel for the flag of an address: the part that does not need the router's route. */
export function FlagPanelContent({
  project,
  flagKey,
  search,
}: {
  project: string;
  flagKey: string;
  search: FlagDetailSearch;
}) {
  const { i18n, t } = useLingui();
  const flag = useQuery(flagQuery(project, flagKey));
  const environments = useQuery(environmentsQuery(project));
  const nameOf = useNameOf(project);

  const configurations = flag.data?.environments ?? [];
  const first = configurations[0]?.environment;
  const open =
    search.tab === "history"
      ? undefined
      : (configurations.find((configuration) => configuration.environment === search.environment)
          ?.environment ?? first);
  const expected = flag.data?.expected_removal_at
    ? formatDateTime(i18n, flag.data.expected_removal_at)
    : undefined;

  return (
    <ContextPanel label={t`Environments and details`}>
      <PanelGroup label={t`Environments`}>
        {configurations.map((configuration) => {
          const known = environments.data?.find((env) => env.key === configuration.environment);
          return (
            <PanelEnvironment
              key={configuration.environment}
              to="/projects/$project/flags/$flag"
              params={{ project, flag: flagKey }}
              // Another environment of the same flag keeps the tester and the
              // days the evaluations cover, as the tabs did.
              search={{
                environment:
                  configuration.environment === first ? undefined : configuration.environment,
                tester: search.tester,
                days: search.days,
              }}
              name={known?.name ?? configuration.environment}
              protected={known?.protected ?? false}
              on={configuration.on}
              current={configuration.environment === open}
            />
          );
        })}
      </PanelGroup>
      {flag.data ? (
        <>
          <PanelGroup label={t`Flag`}>
            <PanelLink
              to="/projects/$project/flags/$flag"
              params={{ project, flag: flagKey }}
              search={{ tab: "history" }}
              current={search.tab === "history"}
            >
              {t`History`}
            </PanelLink>
            {configurations.length > 1 ? (
              <PanelButton onClick={() => runFlagAction("compare")}>
                {t`Compare environments`}
              </PanelButton>
            ) : null}
          </PanelGroup>
          <PanelGroup label={t`Details`}>
            <PanelFacts>
              <PanelFact term={t`Key`}>
                <span className="font-mono text-[12.5px]">{flag.data.key}</span>
              </PanelFact>
              <PanelFact term={t`Type`}>{flagTypeName(i18n, flag.data.type)}</PanelFact>
              <PanelFact term={t`Owner`}>{nameOf(flag.data.owner)}</PanelFact>
              <PanelFact term={t`Lifetime`}>
                {flag.data.lifetime === "permanent" ? (
                  <Trans>Permanent</Trans>
                ) : expected ? (
                  <Trans>Temporary, to be removed by {expected}</Trans>
                ) : (
                  <Trans>Temporary</Trans>
                )}
              </PanelFact>
              <PanelFact term={t`Tags`}>
                {flag.data.tags.length === 0 ? (
                  <span className="text-muted-foreground">
                    <Trans>None</Trans>
                  </span>
                ) : (
                  <span className="flex flex-wrap gap-1">
                    {flag.data.tags.map((tag) => (
                      <Badge key={tag} variant="muted">
                        {tag}
                      </Badge>
                    ))}
                  </span>
                )}
              </PanelFact>
            </PanelFacts>
            <Can permission="flag.edit" whenMissing="disable">
              <PanelButton onClick={() => runFlagAction("edit")}>{t`Edit definition`}</PanelButton>
            </Can>
          </PanelGroup>
        </>
      ) : null}
    </ContextPanel>
  );
}
