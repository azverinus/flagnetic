import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useSearch } from "@tanstack/react-router";

import { AccessProvider } from "@/components/access-provider";
import { ContextPanel, PanelGroup } from "@/components/ui/context-panel";
import { PanelEnvironment, PanelFact, PanelFacts } from "@/components/ui/panel-parts";
import { formatDateTime } from "@/i18n/format";
import { exposureEnvironment, type ExperimentSearch } from "@/lib/experiment-health";
import { environmentsQuery, experimentQuery, flagQuery } from "@/lib/queries";
import { useNameOf } from "@/lib/use-known-members";

const experimentRoute = "/_signed-in/projects/$project/experiments/$experiment";

/**
 * The experiment's context panel: the environments whose rules serve it,
 * each with the lamp of whether its flag is serving there, which open that
 * environment's exposures; and the facts that identify it. Everything it
 * shows comes from the experiment, its flag and the environments.
 */
export function ExperimentPanel() {
  const { project, experiment } = useParams({ from: experimentRoute });
  const search = useSearch({ from: experimentRoute });
  // The shell draws the panel outside the project's layout, which is where
  // the caller's permissions are provided; they are read by then.
  return (
    <AccessProvider project={project}>
      <ExperimentPanelContent project={project} experimentKey={experiment} search={search} />
    </AccessProvider>
  );
}

/** The panel for the experiment of an address: the part that does not need the router's route. */
export function ExperimentPanelContent({
  project,
  experimentKey,
  search,
}: {
  project: string;
  experimentKey: string;
  search: ExperimentSearch;
}) {
  const { i18n, t } = useLingui();
  const experiment = useQuery(experimentQuery(project, experimentKey));
  // The flag is read once the experiment names it.
  const flag = useQuery({
    ...flagQuery(project, experiment.data?.flag ?? ""),
    enabled: experiment.data !== undefined,
  });
  const environments = useQuery(environmentsQuery(project));
  const nameOf = useNameOf(project);

  const flagKey = experiment.data?.flag;
  const configurations = (flag.data?.environments ?? []).filter((configuration) =>
    configuration.rules.some(
      (rule) => rule.serve.kind === "experiment" && rule.serve.experiment === experimentKey,
    ),
  );
  // The same environment the exposures open on, so the current link is the
  // one whose numbers the page shows; the first is the address without one.
  const keys = (environments.data ?? []).map(({ key }) => key);
  const holding = configurations.map(({ environment }) => environment);
  const first = exposureEnvironment(keys, holding, undefined);
  const open = exposureEnvironment(keys, holding, search.environment);
  const loading = flag.isPending || environments.isPending;

  return (
    <ContextPanel label={t`Environments and details`}>
      <PanelGroup label={t`Holding environments`}>
        {configurations.map((configuration) => {
          const known = environments.data?.find((env) => env.key === configuration.environment);
          return (
            <PanelEnvironment
              key={configuration.environment}
              to="/projects/$project/experiments/$experiment"
              params={{ project, experiment: experimentKey }}
              search={{
                environment:
                  configuration.environment === first ? undefined : configuration.environment,
              }}
              name={known?.name ?? configuration.environment}
              protected={known?.protected ?? false}
              on={configuration.on}
              current={configuration.environment === open}
            />
          );
        })}
        {!loading && configurations.length === 0 ? (
          <p className="px-2 py-1 text-sm text-muted-foreground">
            <Trans>No rule serves it</Trans>
          </p>
        ) : null}
      </PanelGroup>
      {experiment.data ? (
        <PanelGroup label={t`Details`}>
          <PanelFacts>
            <PanelFact term={t`Key`}>
              <span className="font-mono text-[12.5px]">{experiment.data.key}</span>
            </PanelFact>
            <PanelFact term={t`Flag`}>
              <Link
                to="/projects/$project/flags/$flag"
                params={{ project, flag: flagKey ?? "" }}
                className="font-mono text-[12.5px] hover:underline"
              >
                {flagKey}
              </Link>
            </PanelFact>
            <PanelFact term={t`Inclusion salt`}>
              <span className="font-mono text-[12.5px] break-all">
                {experiment.data.inclusion_salt}
              </span>
            </PanelFact>
            <PanelFact term={t`Variation salt`}>
              <span className="font-mono text-[12.5px] break-all">
                {experiment.data.variation_salt}
              </span>
            </PanelFact>
            <PanelFact term={t`Created`}>
              {formatDateTime(i18n, experiment.data.created_at)}
              {experiment.data.created_by ? (
                <span className="block text-muted-foreground">
                  {nameOf(experiment.data.created_by)}
                </span>
              ) : null}
            </PanelFact>
          </PanelFacts>
        </PanelGroup>
      ) : null}
    </ContextPanel>
  );
}
