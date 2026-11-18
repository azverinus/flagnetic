import {
  archiveFlag,
  deleteFlag,
  type Environment,
  type Flag,
  type FlagConfiguration,
  restoreFlag,
  type UnitType,
} from "@flagnetic/api";
import { Page } from "@/components/ui/page";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { ArchiveIcon, FlaskConicalIcon, GitCompareArrowsIcon } from "lucide-react";
import { useCallback, useContext, useEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { CompareEnvironments } from "@/components/compare-environments";
import { ConfigurationEditor } from "@/components/configuration-editor";
import { FlagDefinitionSheet } from "@/components/flag-definition-sheet";
import { FlagEvaluations } from "@/components/flag-evaluations";
import { FlagHistory } from "@/components/flag-history";
import { FlagOnOff } from "@/components/flag-on-off";
import { EnvironmentName } from "@/components/protected-mark";
import { OverridesSection } from "@/components/overrides-section";
import { QueryError } from "@/components/query-error";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { WaitingChangeRequests } from "@/components/waiting-change-requests";
import { formatDateTime } from "@/i18n/format";
import { type DayRange, dayRangeOf, daysSearchOf } from "@/lib/daily-counts";
import { ApiError, unwrap } from "@/lib/errors";
import { type FlagDetailSearch, validateFlagDetailSearch } from "@/lib/flag-detail";
import { flagTypeName } from "@/lib/flags";
import { ifMatch } from "@/lib/http";
import { AccessContext, missingPermissionReason } from "@/lib/access";
import { configurationEditing } from "@/lib/change-requests";
import {
  attributesQuery,
  environmentsQuery,
  flagChoicesQuery,
  flagExperimentsQuery,
  flagQuery,
  flagsQueryKey,
  segmentsQuery,
  unitTypesQuery,
} from "@/lib/queries";
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
        <Page>
          <QueryError error={flag.error} onRetry={() => void flag.refetch()} />
        </Page>
      );
    }
    return (
      <Page aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </Page>
    );
  }
  // Another flag's page starts afresh: its drafts and dialogs are its own.
  return <FlagPage key={`${project}/${flagKey}`} project={project} flag={flag.data} />;
}

function FlagNotFound({ project }: { project: string }) {
  return (
    <Page measure="form">
      <h1 className="text-2xl/7 font-semibold">
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
    </Page>
  );
}

/** A member's name by identifier, as far as the caller can know it. */
function useNameOf(project: string): (member: string) => string {
  const { names } = useKnownMembers(project);
  return useCallback((member: string) => names?.get(member) ?? member, [names]);
}

/** Where a key press is typing or choosing, which a page's shortcut leaves alone. */
const typingPlaces = [
  "input",
  "textarea",
  "select",
  "[contenteditable='']",
  "[contenteditable=true]",
  "[role=dialog]",
  "[role=listbox]",
  "[role=combobox]",
].join(", ");

function FlagPage({ project, flag }: { project: string; flag: Flag }) {
  const { i18n, t } = useLingui();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const environments = useQuery(environmentsQuery(project));
  const unitTypes = useQuery(unitTypesQuery(project));
  const nameOf = useNameOf(project);
  const [editing, setEditing] = useState(false);
  const [comparing, setComparing] = useState(false);
  const testerButton = useRef<HTMLButtonElement>(null);
  // Closing the tester returns the keyboard to the button that opens it.
  const closeTester = () => {
    setTester(false);
    testerButton.current?.focus();
  };

  const first = flag.environments[0]?.environment;
  const environment =
    flag.environments.find((configuration) => configuration.environment === search.environment)
      ?.environment ?? first;
  const open = search.tab === "history" ? historyTab : environment;
  const testerOpen = search.tester === true && search.tab !== "history";
  const archived = flag.archived_at !== null;
  const setTester = useCallback(
    (tester: boolean) =>
      void navigate({
        search: (prev) => ({
          ...prev,
          tester: tester ? (true as const) : undefined,
          ...(tester ? { tab: undefined, entry: undefined } : {}),
        }),
        replace: true,
      }),
    [navigate],
  );

  // `t` opens the tester from anywhere on the page but where it would be
  // typed: a field, a list, or a dialog over the page.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "t" || event.ctrlKey || event.metaKey || event.altKey) return;
      if (event.defaultPrevented) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest(typingPlaces)) return;
      event.preventDefault();
      setTester(true);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [setTester]);
  const expected = flag.expected_removal_at
    ? formatDateTime(i18n, flag.expected_removal_at)
    : undefined;

  const setDays = (days: DayRange) =>
    void navigate({
      search: (prev) => ({ ...prev, days: daysSearchOf(days) }),
      replace: true,
    });

  const tabs = [
    ...flag.environments.map((configuration) => {
      const known = environments.data?.find((env) => env.key === configuration.environment);
      return {
        value: configuration.environment,
        label: known ? <EnvironmentName environment={known} /> : configuration.environment,
        content: (
          <EnvironmentPanel
            project={project}
            flag={flag}
            configuration={configuration}
            environment={known}
            environmentsRead={{
              error: environments.error,
              retry: () => void environments.refetch(),
            }}
            unitTypes={unitTypes.data ?? []}
            nameOf={nameOf}
            tester={testerOpen ? { onClose: closeTester } : undefined}
            evaluations={{ days: dayRangeOf(search), onDays: setDays }}
          />
        ),
      };
    }),
    {
      value: historyTab,
      label: t`History`,
      content: (
        <FlagHistory
          project={project}
          flag={flag}
          entry={search.entry}
          onCloseEntry={() =>
            void navigate({ search: (prev) => ({ ...prev, entry: undefined }), replace: true })
          }
          nameOf={nameOf}
        />
      ),
    },
  ];

  return (
    <Page reserve={testerOpen}>
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
            <Button
              variant="outline"
              ref={testerButton}
              aria-keyshortcuts="t"
              aria-pressed={testerOpen}
              onClick={() => setTester(!testerOpen)}
            >
              <FlaskConicalIcon aria-hidden />
              <Trans>Tester</Trans>
            </Button>
            {flag.environments.length > 1 ? (
              <Button variant="outline" onClick={() => setComparing(true)}>
                <GitCompareArrowsIcon aria-hidden />
                <Trans>Compare environments</Trans>
              </Button>
            ) : null}
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
                ? { ...prev, tab: "history" as const, tester: undefined }
                : {
                    environment: next === first ? undefined : next,
                    tester: prev.tester,
                    days: prev.days,
                  },
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
      <CompareEnvironments
        project={project}
        flag={flag}
        environments={environments.data ?? []}
        open={comparing}
        onOpenChange={setComparing}
        initial={environment}
      />
    </Page>
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
  /** Reading the environments again, and why they could not be read. */
  environmentsRead: { error: unknown; retry: () => void };
  unitTypes: readonly UnitType[];
  nameOf: (member: string) => string;
  /** The tester beside the configuration, while it is open. */
  tester?: { onClose: () => void };
  /** The days the flag's evaluations in the environment cover. */
  evaluations: { days: DayRange; onDays: (days: DayRange) => void };
};

/**
 * What the flag serves in one environment, in the order evaluation reads
 * it: whether it is on, its off variation, overrides, prerequisites,
 * rules and fallthrough; then whether SDKs still evaluate it there.
 */
function EnvironmentPanel(props: PanelProps) {
  const { i18n, t } = useLingui();
  const { project, flag, configuration, environment } = props;
  const access = useContext(AccessContext);
  const archived = flag.archived_at !== null;
  const frozen = archived
    ? t`The flag is archived. Restore it to change its configurations.`
    : undefined;
  // A protected environment's configuration is proposed for review, so it
  // is edited by whoever may propose; until the environments are read it
  // is taken as unprotected, which the server judges on saving anyway.
  const editing = configurationEditing(environment?.protected ?? false, access.permissions);
  const missing =
    editing.kind === "read"
      ? missingPermissionReason(i18n, editing.missing, access.roles)
      : undefined;
  const attributes = useQuery(attributesQuery(project));
  const segments = useQuery(segmentsQuery(project));
  const flags = useQuery(flagChoicesQuery(project));
  const experiments = useQuery(flagExperimentsQuery(project, flag.key));
  const reads = [attributes, segments, flags, experiments];
  const failed = reads.find((read) => read.isError);

  return (
    <div className="flex flex-col gap-8">
      <WaitingChangeRequests
        project={project}
        flag={flag.key}
        environment={configuration.environment}
      />
      {frozen ? (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          {frozen}
        </p>
      ) : missing !== undefined ? (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          {missing}
        </p>
      ) : editing.kind === "propose" ? (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          <Trans>
            The environment is protected: changes to its configuration are proposed as change
            requests and go live once approved and applied.
          </Trans>
        </p>
      ) : null}
      <FlagOnOff {...props} frozen={frozen !== undefined} />
      {failed ? (
        <QueryError error={failed.error} onRetry={() => void failed.refetch()} />
      ) : environment && attributes.data && segments.data && flags.data && experiments.data ? (
        <ConfigurationEditor
          // A configuration of another environment starts a draft of its own.
          key={configuration.environment}
          project={project}
          flag={flag}
          configuration={configuration}
          environment={environment}
          declarations={{
            attributes: attributes.data,
            unitTypes: props.unitTypes,
            segments: segments.data,
            flags: flags.data,
            experiments: experiments.data,
          }}
          overrides={<OverridesSection {...props} frozen={frozen} />}
          readOnly={frozen ?? missing}
          proposes={editing.kind === "propose"}
          tester={props.tester}
        />
      ) : (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-9 w-64" />
          <Skeleton className="h-32 w-full" />
        </div>
      )}
      <FlagEvaluations
        project={project}
        flag={flag}
        environment={configuration.environment}
        days={props.evaluations.days}
        onDays={props.evaluations.onDays}
      />
    </div>
  );
}
