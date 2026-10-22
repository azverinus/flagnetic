import {
  archiveFlag,
  deleteFlag,
  type Environment,
  type Flag,
  type FlagConfiguration,
  putFlagConfiguration,
  restoreFlag,
  type UnitType,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { ArchiveIcon, OctagonXIcon } from "lucide-react";
import { useCallback, useId, useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { FlagDefinitionSheet } from "@/components/flag-definition-sheet";
import { KillSwitchDialog } from "@/components/kill-switch-dialog";
import { OverridesSection } from "@/components/overrides-section";
import { QueryError } from "@/components/query-error";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Tabs } from "@/components/ui/tabs";
import { formatDateTime } from "@/i18n/format";
import { ApiError, unwrap } from "@/lib/errors";
import {
  configurationPut,
  type FlagDetailSearch,
  validateFlagDetailSearch,
  withConfiguration,
} from "@/lib/flag-detail";
import { flagTypeName } from "@/lib/flags";
import { ifMatch } from "@/lib/http";
import { problemMessage } from "@/lib/problem-messages";
import { environmentsQuery, flagQuery, flagsQueryKey, unitTypesQuery } from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/flags/$flag")({
  staticData: { crumb: { param: "flag" } },
  validateSearch: (search: Record<string, unknown>): FlagDetailSearch =>
    validateFlagDetailSearch(search),
  component: FlagDetail,
});

/**
 * The History tab's value among the environments' keys: an environment
 * key starts with a lowercase letter, so this one is never one of them.
 */
const historyTab = "#history";

function FlagDetail() {
  const { project, flag: flagKey } = Route.useParams();
  const flag = useQuery(flagQuery(project, flagKey));

  if (flag.data === undefined) {
    if (flag.isError) {
      if (flag.error instanceof ApiError && flag.error.status === 404) {
        return <FlagNotFound project={project} />;
      }
      return (
        <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
          <QueryError error={flag.error} onRetry={() => void flag.refetch()} />
        </div>
      );
    }
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  // Another flag's page starts afresh: its drafts and dialogs are its own.
  return <FlagPage key={`${project}/${flagKey}`} project={project} flag={flag.data} />;
}

function FlagNotFound({ project }: { project: string }) {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Flag not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>The project has no flag with this key. It may have been deleted.</Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/projects/$project/flags" params={{ project }}>
          <Trans>Go to the flags</Trans>
        </Link>
      </Button>
    </div>
  );
}

/** A member's name by identifier, as far as the caller can know it. */
function useNameOf(project: string): (member: string) => string {
  const { names } = useKnownMembers(project);
  return useCallback((member: string) => names?.get(member) ?? member, [names]);
}

function FlagPage({ project, flag }: { project: string; flag: Flag }) {
  const { i18n, t } = useLingui();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const environments = useQuery(environmentsQuery(project));
  const unitTypes = useQuery(unitTypesQuery(project));
  const nameOf = useNameOf(project);
  const [editing, setEditing] = useState(false);

  const first = flag.environments[0]?.environment;
  const environment =
    flag.environments.find((configuration) => configuration.environment === search.environment)
      ?.environment ?? first;
  const open = search.tab === "history" ? historyTab : environment;
  const archived = flag.archived_at !== null;
  const expected = flag.expected_removal_at
    ? formatDateTime(i18n, flag.expected_removal_at)
    : undefined;

  const tabs = [
    ...flag.environments.map((configuration) => {
      const known = environments.data?.find((env) => env.key === configuration.environment);
      return {
        value: configuration.environment,
        label: known?.name ?? configuration.environment,
        content: (
          <EnvironmentPanel
            project={project}
            flag={flag}
            configuration={configuration}
            environment={known}
            unitTypes={unitTypes.data ?? []}
            nameOf={nameOf}
          />
        ),
      };
    }),
    {
      value: historyTab,
      label: t`History`,
      content: <HistoryPlaceholder project={project} flag={flag} />,
    },
  ];

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={flag.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-foreground">{flag.key}</span>
            <span aria-hidden>·</span>
            <span>{flagTypeName(i18n, flag.type)}</span>
            {archived ? (
              <Badge variant="muted">
                <ArchiveIcon aria-hidden className="size-3" />
                <Trans>Archived</Trans>
              </Badge>
            ) : null}
          </span>
        }
        actions={
          <>
            <FlagLifecycleActions project={project} flag={flag} />
            <Can permission="flag.edit" whenMissing="disable">
              <Button variant="outline" onClick={() => setEditing(true)}>
                <Trans>Edit definition</Trans>
              </Button>
            </Can>
          </>
        }
      />
      <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-3">
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Owner</Trans>
          </dt>
          <dd>{nameOf(flag.owner)}</dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Lifetime</Trans>
          </dt>
          <dd>
            {flag.lifetime === "permanent" ? (
              <Trans>Permanent</Trans>
            ) : expected ? (
              <Trans>Temporary, to be removed by {expected}</Trans>
            ) : (
              <Trans>Temporary</Trans>
            )}
          </dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-muted-foreground">
            <Trans>Tags</Trans>
          </dt>
          <dd className="flex flex-wrap gap-1">
            {flag.tags.length === 0 ? (
              <span className="text-muted-foreground">
                <Trans>None</Trans>
              </span>
            ) : (
              flag.tags.map((tag) => (
                <Badge key={tag} variant="muted">
                  {tag}
                </Badge>
              ))
            )}
          </dd>
        </div>
        {flag.description ? (
          <div className="flex flex-col gap-0.5 sm:col-span-3">
            <dt className="text-muted-foreground">
              <Trans>Description</Trans>
            </dt>
            <dd className="whitespace-pre-wrap">{flag.description}</dd>
          </div>
        ) : null}
      </dl>
      <Tabs
        label={t`Environments and history`}
        tabs={tabs}
        value={open}
        onValueChange={(next) => {
          void navigate({
            search: (prev) =>
              next === historyTab
                ? { ...prev, tab: "history" as const }
                : { environment: next === first ? undefined : next, tab: undefined },
            replace: true,
          });
        }}
      />
      <FlagDefinitionSheet
        project={project}
        flag={flag}
        open={editing}
        onOpenChange={setEditing}
        nameOf={nameOf}
      />
    </div>
  );
}

/** Archiving, restoring and deleting: the steps of retiring a flag. */
function FlagLifecycleActions({ project, flag }: { project: string; flag: Flag }) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [asking, setAsking] = useState<"archive" | "restore" | "delete">();
  const key = flag.key;
  const detailKey = flagQuery(project, flag.key).queryKey;

  const act = useMutation({
    mutationFn: async (action: "archive" | "restore" | "delete") => {
      const options = { path: { project, flag: flag.key }, headers: ifMatch(flag.version) };
      if (action === "archive") return unwrap(archiveFlag(options));
      if (action === "restore") return unwrap(restoreFlag(options));
      await unwrap(deleteFlag(options));
      return undefined;
    },
    meta: { toast: false },
    onSuccess: async (saved, action) => {
      void queryClient.invalidateQueries({ queryKey: [...flagsQueryKey(project), "list"] });
      setAsking(undefined);
      if (action === "delete") {
        toast.success(t`The flag ${key} is deleted.`);
        await navigate({ to: "/projects/$project/flags", params: { project } });
        queryClient.removeQueries({ queryKey: detailKey });
        return;
      }
      if (saved) queryClient.setQueryData(detailKey, saved);
      toast.success(
        action === "archive" ? t`The flag ${key} is archived.` : t`The flag ${key} is restored.`,
      );
    },
    onError: (error) => {
      // The dialog explains the refusal; a newer version is read so that
      // confirming again is based on it.
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: detailKey });
      }
    },
  });
  const ask = (action: "archive" | "restore" | "delete") => {
    act.reset();
    setAsking(action);
  };
  const dialog = (action: "archive" | "restore" | "delete") => ({
    open: asking === action,
    onOpenChange: (open: boolean) => !open && setAsking(undefined),
    pending: act.isPending,
    refusal: act.error,
    onConfirm: () => act.mutate(action),
  });

  return (
    <>
      {flag.archived_at === null ? (
        <Can permission="flag.archive" whenMissing="disable">
          <Button variant="outline" onClick={() => ask("archive")}>
            <Trans>Archive</Trans>
          </Button>
        </Can>
      ) : (
        <>
          <Can permission="flag.archive" whenMissing="disable">
            <Button variant="outline" onClick={() => ask("restore")}>
              <Trans>Restore</Trans>
            </Button>
          </Can>
          <Can permission="flag.archive" whenMissing="disable">
            <Button variant="destructive" onClick={() => ask("delete")}>
              <Trans>Delete</Trans>
            </Button>
          </Can>
        </>
      )}
      <ConfirmDialog
        {...dialog("archive")}
        title={t`Archive ${key}`}
        confirmLabel={<Trans>Archive flag</Trans>}
      >
        <p>
          <Trans>
            SDKs stop receiving the flag in every environment and code falls back to its default. It
            can be restored later; refused while another flag requires it.
          </Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        {...dialog("restore")}
        title={t`Restore ${key}`}
        confirmLabel={<Trans>Restore flag</Trans>}
      >
        <p>
          <Trans>
            SDKs receive the flag again, configured in every environment as it was left.
          </Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        {...dialog("delete")}
        title={t`Delete ${key}`}
        confirmLabel={<Trans>Delete flag</Trans>}
        destructive
        typeToConfirm={flag.key}
      >
        <p>
          <Trans>
            The flag and its configuration in every environment are deleted. This cannot be taken
            back; its audit entries stay.
          </Trans>
        </p>
      </ConfirmDialog>
    </>
  );
}

type PanelProps = {
  project: string;
  flag: Flag;
  configuration: FlagConfiguration;
  /** The environment, once the project's environments are read. */
  environment: Environment | undefined;
  unitTypes: readonly UnitType[];
  nameOf: (member: string) => string;
};

/**
 * What the flag serves in one environment, in the order evaluation reads
 * it: whether it is on, its off variation, overrides, prerequisites,
 * rules and fallthrough.
 */
function EnvironmentPanel(props: PanelProps) {
  const { t } = useLingui();
  const { flag, configuration, nameOf } = props;
  const archived = flag.archived_at !== null;
  const frozen = archived
    ? t`The flag is archived. Restore it to change its configurations.`
    : undefined;
  const updated = configuration.updated_by ? nameOf(configuration.updated_by) : undefined;
  const version = configuration.version;

  return (
    <div className="flex flex-col gap-8">
      {frozen ? (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          {frozen}
        </p>
      ) : null}
      <OnOff {...props} frozen={frozen !== undefined} />
      <section className="grid gap-4 text-sm sm:grid-cols-2">
        <div className="flex flex-col gap-0.5">
          <span className="text-muted-foreground">
            <Trans>Off variation</Trans>
          </span>
          <span className="font-mono">{configuration.off_variation}</span>
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="text-muted-foreground">
            <Trans>Fallthrough variation</Trans>
          </span>
          <span className="font-mono">{configuration.fallthrough}</span>
        </div>
      </section>
      <OverridesSection {...props} frozen={frozen} />
      <p className="text-xs text-muted-foreground">
        {updated ? (
          <Trans>
            Version {version}, last saved by {updated}.
          </Trans>
        ) : (
          <Trans>Version {version}.</Trans>
        )}
      </p>
    </div>
  );
}

/**
 * Switching the flag on or off in the environment, which is saved at
 * once, and the kill switch beside it while it is on.
 */
function OnOff({
  project,
  flag,
  configuration,
  environment,
  frozen,
}: PanelProps & { frozen: boolean }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const switchId = useId();
  const [killing, setKilling] = useState(false);
  const name = environment?.name ?? configuration.environment;
  const key = flag.key;

  const toggle = useMutation({
    mutationFn: (on: boolean) =>
      unwrap(
        putFlagConfiguration({
          path: { project, flag: flag.key, environment: configuration.environment },
          body: { ...configurationPut(configuration), on },
          headers: ifMatch(configuration.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: (next) => {
      queryClient.setQueryData(
        flagQuery(project, flag.key).queryKey,
        (current: Flag | undefined) => (current ? withConfiguration(current, next) : current),
      );
      void queryClient.invalidateQueries({ queryKey: [...flagsQueryKey(project), "list"] });
      toast.success(next.on ? t`${key} is on in ${name}.` : t`${key} is off in ${name}.`);
    },
    onError: (error) => {
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: flagQuery(project, flag.key).queryKey });
        toast.error(
          t`Someone changed this configuration a moment ago. It is read again: switch it once more if you still mean to.`,
        );
        return;
      }
      toast.error(error instanceof ApiError ? problemMessage(i18n, error) : String(error));
    },
  });

  return (
    <section className="flex flex-wrap items-center justify-between gap-4 rounded-md border p-4">
      <div className="flex items-center gap-3">
        <Can permission="flag_configuration.update" whenMissing="disable">
          <Switch
            id={switchId}
            checked={configuration.on}
            disabled={frozen || toggle.isPending}
            aria-label={
              configuration.on ? t`Switch ${key} off in ${name}` : t`Switch ${key} on in ${name}`
            }
            onCheckedChange={(on) => toggle.mutate(on)}
          />
        </Can>
        <span className="text-sm font-medium">
          {configuration.on ? <Trans>On</Trans> : <Trans>Off</Trans>}
        </span>
        <span className="text-sm text-muted-foreground">
          {configuration.on ? (
            <Trans>Contexts are served by the overrides, rules and fallthrough below.</Trans>
          ) : (
            <Trans>Every context receives the off variation.</Trans>
          )}
        </span>
      </div>
      {configuration.on && !frozen ? (
        <>
          <Can permission="flag_configuration.kill" whenMissing="disable">
            <Button variant="destructive" onClick={() => setKilling(true)}>
              <OctagonXIcon aria-hidden />
              <Trans>Kill switch</Trans>
            </Button>
          </Can>
          {environment ? (
            <KillSwitchDialog
              project={project}
              flag={flag}
              environment={environment}
              open={killing}
              onOpenChange={setKilling}
            />
          ) : null}
        </>
      ) : null}
    </section>
  );
}

function HistoryPlaceholder({ project, flag }: { project: string; flag: Flag }) {
  const { t } = useLingui();
  return (
    <EmptyState
      title={t`History`}
      description={
        <Trans>
          Every change to this flag, its definition and its configurations, is recorded in the
          project's audit log.
        </Trans>
      }
      action={
        <Button asChild variant="outline">
          <Link
            to="/projects/$project/audit"
            params={{ project }}
            search={{ entity_kind: "flag", q: flag.key }}
          >
            <Trans>Open the audit log</Trans>
          </Link>
        </Button>
      }
    />
  );
}
