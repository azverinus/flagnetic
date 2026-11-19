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
import { ArchiveIcon, FlaskConicalIcon } from "lucide-react";
import { type ReactNode, useCallback, useContext, useEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { CompareEnvironments } from "@/components/compare-environments";
import { ConfigurationEditor } from "@/components/configuration-editor";
import { FlagDefinitionSheet } from "@/components/flag-definition-sheet";
import { FlagEvaluations } from "@/components/flag-evaluations";
import { FlagHistory } from "@/components/flag-history";
import { FlagOnOff } from "@/components/flag-on-off";
import { OverridesSection } from "@/components/overrides-section";
import { QueryError } from "@/components/query-error";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader, PageTitle } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Select, SelectItem } from "@/components/ui/select";
import { useContextPanelHidden } from "@/components/ui/context-panel";
import { WaitingChangeRequests } from "@/components/waiting-change-requests";
import { type DayRange, dayRangeOf, daysSearchOf } from "@/lib/daily-counts";
import { cn } from "@/lib/cn";
import { ApiError, unwrap } from "@/lib/errors";
import { type FlagDetailSearch, validateFlagDetailSearch } from "@/lib/flag-detail";
import { ifMatch } from "@/lib/http";
import {
  AccessContext,
  missingPermissionReason,
  useCan,
  useMissingPermissionReason,
} from "@/lib/access";
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
import { useNameOf } from "@/lib/use-known-members";
import { FlagPanel } from "@/components/flag-panel";
import { registerFlagActions } from "@/lib/flag-actions";
import { environmentLabel } from "@/lib/environments";

export const Route = createFileRoute("/_signed-in/projects/$project/flags/$flag")({
  staticData: { crumb: { param: "flag" }, panel: FlagPanel },
  validateSearch: (search: Record<string, unknown>): FlagDetailSearch =>
    validateFlagDetailSearch(search),
  component: FlagDetail,
});

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
      <PageTitle>
        <Trans>Flag not found</Trans>
      </PageTitle>
      <p className="text-sm text-muted-foreground">
        <Trans>The project has no flag with this key. It may have been deleted.</Trans>
      </p>
      <Button className="self-start" asChild variant="outline">
        <Link to="/projects/$project/flags" params={{ project }}>
          <Trans>Go to the flags</Trans>
        </Link>
      </Button>
    </Page>
  );
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
  const panelHidden = useContextPanelHidden();
  const [editing, setEditing] = useState(false);
  const [comparing, setComparing] = useState(false);
  const testerButton = useRef<HTMLButtonElement>(null);
  const lifecycle = useFlagLifecycle(project, flag);
  // Closing the tester returns the keyboard to the button that opens it.
  const closeTester = () => {
    setTester(false);
    testerButton.current?.focus();
  };

  const first = flag.environments[0]?.environment;
  const configuration =
    flag.environments.find((candidate) => candidate.environment === search.environment) ??
    flag.environments[0];
  const environment = configuration?.environment;
  const history = search.tab === "history";
  const testerOpen = search.tester === true && !history;
  const archived = flag.archived_at !== null;
  const known = environments.data?.find((env) => env.key === environment);
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

  // The panel's "Compare environments" and "Edit definition" open the
  // dialogs this page owns.
  useEffect(
    () => registerFlagActions({ compare: () => setComparing(true), edit: () => setEditing(true) }),
    [],
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

  const setDays = (days: DayRange) =>
    void navigate({
      search: (prev) => ({ ...prev, days: daysSearchOf(days) }),
      replace: true,
    });

  // With the panel folded away or in a sheet, the open environment is
  // chosen beside the title, as the panel's links choose it.
  const environmentSelect = panelHidden ? (
    <Select
      aria-label={t`Environment`}
      value={history ? "" : (environment ?? "")}
      placeholder={t`Choose an environment`}
      onValueChange={(next) =>
        void navigate({
          search: (prev) => ({
            environment: next === first ? undefined : next,
            tester: prev.tester,
            days: prev.days,
          }),
          replace: true,
        })
      }
      className="w-48"
    >
      {flag.environments.map((candidate) => {
        const env = environments.data?.find((item) => item.key === candidate.environment);
        return (
          <SelectItem key={candidate.environment} value={candidate.environment}>
            {env ? environmentLabel(i18n, env) : candidate.environment}
          </SelectItem>
        );
      })}
    </Select>
  ) : null;

  return (
    <Page reserve={testerOpen}>
      <PageHeader
        title={flag.name}
        description={
          archived || flag.description ? (
            <span className="flex flex-wrap items-center gap-2">
              {archived ? (
                <Badge variant="muted">
                  <ArchiveIcon aria-hidden className="size-3" />
                  <Trans>Archived</Trans>
                </Badge>
              ) : null}
              {flag.description ? (
                <span className="whitespace-pre-wrap">{flag.description}</span>
              ) : null}
            </span>
          ) : undefined
        }
        actions={
          <>
            {environmentSelect}
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
          </>
        }
        more={lifecycle.menu}
      />
      {history ? (
        <Card list>
          <CardHeader title={<Trans>History</Trans>} />
          <CardContent>
            <FlagHistory
              project={project}
              flag={flag}
              entry={search.entry}
              onCloseEntry={() =>
                void navigate({ search: (prev) => ({ ...prev, entry: undefined }), replace: true })
              }
              nameOf={nameOf}
            />
          </CardContent>
        </Card>
      ) : configuration ? (
        <EnvironmentPage
          // Another environment starts afresh: its drafts and dialogs are its own.
          key={configuration.environment}
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
      ) : null}
      {lifecycle.dialogs}
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

type Retiring = "archive" | "restore" | "delete";

/**
 * Archiving, restoring and deleting: the steps of retiring a flag. The menu
 * items go behind the header's "More actions"; the dialogs that confirm
 * them stay outside the menu, which unmounts its items once it closes.
 */
function useFlagLifecycle(project: string, flag: Flag): { menu: ReactNode; dialogs: ReactNode } {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [asking, setAsking] = useState<Retiring>();
  const key = flag.key;
  const detailKey = flagQuery(project, flag.key).queryKey;
  const can = useCan("flag.archive");
  const reason = useMissingPermissionReason("flag.archive");

  const act = useMutation({
    mutationFn: async (action: Retiring) => {
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
  const ask = (action: Retiring) => {
    act.reset();
    setAsking(action);
  };
  const dialog = (action: Retiring) => ({
    open: asking === action,
    onOpenChange: (open: boolean) => !open && setAsking(undefined),
    pending: act.isPending,
    refusal: act.error,
    onConfirm: () => act.mutate(action),
  });
  const item = (action: Retiring, label: ReactNode, destructive = false) => (
    // A refused item is marked disabled rather than made disabled: a disabled
    // menu item is skipped by the keyboard and takes no pointer, so its
    // reason could be neither reached nor hovered.
    <DropdownMenuItem
      aria-disabled={can ? undefined : true}
      title={can ? undefined : reason}
      className={cn(destructive && "text-destructive focus:text-destructive", !can && "opacity-50")}
      onSelect={(event) => {
        if (!can) {
          event.preventDefault();
          return;
        }
        ask(action);
      }}
    >
      {label}
    </DropdownMenuItem>
  );

  return {
    menu:
      flag.archived_at === null ? (
        item("archive", <Trans>Archive</Trans>)
      ) : (
        <>
          {item("restore", <Trans>Restore</Trans>)}
          {item("delete", <Trans>Delete</Trans>, true)}
        </>
      ),
    dialogs: (
      <>
        <ConfirmDialog
          {...dialog("archive")}
          title={t`Archive ${key}`}
          confirmLabel={<Trans>Archive flag</Trans>}
        >
          <p>
            <Trans>
              SDKs stop receiving the flag in every environment and code falls back to its default.
              It can be restored later; refused while another flag requires it.
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
    ),
  };
}

type EnvironmentPageProps = {
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
function EnvironmentPage(props: EnvironmentPageProps) {
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

  const switches = <FlagOnOff {...props} frozen={frozen !== undefined} />;
  const evaluations = (
    <FlagEvaluations
      project={project}
      flag={flag}
      environment={configuration.environment}
      days={props.evaluations.days}
      onDays={props.evaluations.onDays}
    />
  );

  return (
    <div className="flex flex-col gap-5">
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
      {failed ? (
        <>
          <Card>
            <CardHeader title={<Trans>Serving</Trans>} />
            <CardContent>{switches}</CardContent>
          </Card>
          <QueryError error={failed.error} onRetry={() => void failed.refetch()} />
          {evaluations}
        </>
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
          switches={switches}
          overrides={<OverridesSection {...props} frozen={frozen} />}
          evaluations={evaluations}
          readOnly={frozen ?? missing}
          proposes={editing.kind === "propose"}
          tester={props.tester}
        />
      ) : (
        <>
          <Card>
            <CardHeader title={<Trans>Serving</Trans>} />
            <CardContent>
              {switches}
              <div className="flex flex-col gap-3" aria-busy>
                <Skeleton className="h-9 w-64" />
                <Skeleton className="h-32 w-full" />
              </div>
            </CardContent>
          </Card>
          {evaluations}
        </>
      )}
    </div>
  );
}
