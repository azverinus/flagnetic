import {
  type AuditEntry,
  type Environment,
  type Flag,
  type FlagConfiguration,
  revertFlagConfiguration,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { HistoryIcon, Undo2Icon } from "lucide-react";
import { type ComponentProps, useState } from "react";
import { toast } from "sonner";

import { AuditEntriesTable, type EntryLinkComponent } from "@/components/audit-log";
import { AuditEntrySheet } from "@/components/audit-entry-sheet";
import { Can } from "@/components/can";
import { useEnvironmentPlace } from "@/components/project-audit";
import { QueryError } from "@/components/query-error";
import { ConfigurationDiff, DefinitionDiff } from "@/components/structural-diff-view";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { ApiError, unwrap } from "@/lib/errors";
import { configurationSides, definitionSides, revertVersion } from "@/lib/flag-history";
import { ifMatch } from "@/lib/http";
import {
  auditQueryKey,
  environmentsQuery,
  flagHistoryQuery,
  flagQuery,
  projectAuditEntryQuery,
} from "@/lib/queries";
import { diffConfigurations, diffDefinitions } from "@/lib/structural-diff";
import { useConfigurationAnswer } from "@/lib/use-configuration-answer";

export type FlagHistoryProps = {
  project: string;
  flag: Flag;
  /** The entry open in the drawer, by identifier. */
  entry: string | undefined;
  onCloseEntry: () => void;
  nameOf: (member: string) => string;
};

/** The link to one entry of the flag's history, which opens it in the drawer. */
function HistoryEntryLink({ entry, className, children }: ComponentProps<EntryLinkComponent>) {
  const { project, flag } = useParams({ from: "/_signed-in/projects/$project/flags/$flag" });
  return (
    <Link
      to="/projects/$project/flags/$flag"
      params={{ project, flag }}
      search={{ tab: "history", entry: entry.id }}
      replace
      className={className}
    >
      {children}
    </Link>
  );
}

/**
 * Every change to a flag, its definition and its configuration in every
 * environment, newest first and a page at a time; an entry opens in a
 * drawer with what it changed, and one that changed a configuration
 * offers to revert the configuration to the version it made.
 */
export function FlagHistory({ project, flag, entry, onCloseEntry, nameOf }: FlagHistoryProps) {
  const { t } = useLingui();
  const place = useEnvironmentPlace(project);
  // The cursor of the page shown, and those of the pages before it, so
  // "Previous" goes back one page at a time.
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]);
  const cursor = cursors.at(-1);
  const page = useQuery(flagHistoryQuery(project, flag.id, cursor));

  if (page.isError) {
    return <QueryError error={page.error} onRetry={() => void page.refetch()} />;
  }
  const next = page.data?.next_cursor ?? undefined;
  return (
    <div className="flex flex-col gap-4">
      {page.data?.items.length === 0 && cursor === undefined ? (
        <EmptyState
          icon={<HistoryIcon />}
          title={<Trans>Nothing has changed on this flag yet.</Trans>}
        />
      ) : (
        <>
          <AuditEntriesTable
            caption={t`History of the flag`}
            entries={page.data?.items ?? []}
            loading={page.isPending}
            EntryLink={HistoryEntryLink}
            place={place}
          />
          <nav aria-label={t`Pages of the history`} className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              disabled={cursors.length === 1 || page.isFetching}
              onClick={() => setCursors(cursors.slice(0, -1))}
            >
              <Trans>Previous</Trans>
            </Button>
            <Button
              variant="outline"
              disabled={next === undefined || page.isFetching}
              onClick={() => setCursors([...cursors, next])}
            >
              <Trans>Next</Trans>
            </Button>
          </nav>
        </>
      )}
      {entry ? (
        <HistoryEntrySheet
          project={project}
          flag={flag}
          entryId={entry}
          onClose={onCloseEntry}
          nameOf={nameOf}
        />
      ) : null}
    </div>
  );
}

function HistoryEntrySheet({
  project,
  flag,
  entryId,
  onClose,
  nameOf,
}: {
  project: string;
  flag: Flag;
  entryId: string;
  onClose: () => void;
  nameOf: (member: string) => string;
}) {
  const { t } = useLingui();
  const entry = useQuery(projectAuditEntryQuery(project, entryId));
  const environments = useQuery(environmentsQuery(project));
  const environment = environments.data?.find((env) => env.id === entry.data?.environment_id);
  const current = flag.environments.find(
    (configuration) => configuration.environment === environment?.key,
  );

  return (
    <AuditEntrySheet
      onClose={onClose}
      entry={entry.data}
      pending={entry.isPending}
      error={entry.error}
      onRetry={() => void entry.refetch()}
      place={environment ? { label: t`Environment`, value: environment.name } : undefined}
      changes={entry.data ? <EntryChanges entry={entry.data} nameOf={nameOf} /> : undefined}
      actions={
        entry.data && environment && current ? (
          <RevertAction
            project={project}
            flag={flag}
            entry={entry.data}
            environment={environment}
            current={current}
          />
        ) : undefined
      }
    />
  );
}

/** What a history entry changed, item by item, when it changed a configuration or the definition. */
function EntryChanges({
  entry,
  nameOf,
}: {
  entry: AuditEntry;
  nameOf: (member: string) => string;
}) {
  const configuration = configurationSides(entry);
  if (configuration?.before && configuration.after) {
    return (
      <ConfigurationDiff changes={diffConfigurations(configuration.before, configuration.after)} />
    );
  }
  const definition = definitionSides(entry);
  if (definition?.before && definition.after) {
    return (
      <DefinitionDiff
        changes={diffDefinitions(definition.before, definition.after)}
        nameOf={nameOf}
      />
    );
  }
  return null;
}

/**
 * Writing again, whole, the configuration a history entry made, rule
 * identifiers and salts included, so a rollout's units stay where they
 * were. It is based on the version the page shows; a configuration
 * changed since is refused and read again, so confirming again is based
 * on the newer one.
 */
function RevertAction({
  project,
  flag,
  entry,
  environment,
  current,
}: {
  project: string;
  flag: Flag;
  entry: AuditEntry;
  environment: Environment;
  current: FlagConfiguration;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const answered = useConfigurationAnswer(project, flag.key);
  const [asking, setAsking] = useState(false);
  const version = revertVersion(entry, current.version);
  const now = current.version;
  const name = environment.name;

  const revert = useMutation({
    mutationFn: (to: number) =>
      unwrap(
        revertFlagConfiguration({
          path: { project, flag: flag.key, environment: environment.key },
          body: { version: to },
          headers: ifMatch(current.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: (configuration, to) => {
      answered(configuration);
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      setAsking(false);
      toast.success(t`The configuration in ${name} is back to version ${to}.`);
    },
    onError: (error) => {
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: flagQuery(project, flag.key).queryKey });
      }
    },
  });

  if (version === undefined) {
    return configurationSides(entry)?.after?.version === current.version ? (
      <p className="text-sm text-muted-foreground">
        <Trans>The configuration in {name} is at this version now.</Trans>
      </p>
    ) : null;
  }
  if (flag.archived_at !== null) {
    return (
      <p className="text-sm text-muted-foreground">
        <Trans>The flag is archived. Restore it to change its configurations.</Trans>
      </p>
    );
  }
  return (
    <>
      <Can permission="flag_configuration.revert" whenMissing="disable">
        <Button
          variant="outline"
          onClick={() => {
            revert.reset();
            setAsking(true);
          }}
        >
          <Undo2Icon aria-hidden />
          <Trans>Revert to this version</Trans>
        </Button>
      </Can>
      <ConfirmDialog
        open={asking}
        onOpenChange={setAsking}
        title={t`Revert to version ${version}`}
        confirmLabel={<Trans>Revert</Trans>}
        pending={revert.isPending}
        refusal={revert.error}
        onConfirm={() => revert.mutate(version)}
      >
        <p>
          <Trans>
            The configuration in {name}, now at version {now}, becomes again what version {version}{" "}
            was: on or off, its variations, overrides, prerequisites and rules, with the rules'
            identifiers and salts, so rollouts keep their units.
          </Trans>
        </p>
      </ConfirmDialog>
    </>
  );
}
