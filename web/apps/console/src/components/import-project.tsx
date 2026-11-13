import type { ImportKindChanges, ImportResult, ImportUpdate } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Plural, Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleAlertIcon, TriangleAlertIcon } from "lucide-react";
import { type ChangeEvent, type ReactNode, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/errors";
import { problemMessage, violationMessage } from "@/lib/problem-messages";
import {
  applyImport,
  type ChangeKind,
  changedKinds,
  documentMediaType,
  dryRunImport,
  type HeldDocument,
  type ImportSession,
  isUnchanged,
  loadImportSession,
  type PatchLine,
  patchLines,
  protectedEnvironmentsTouched,
  saveImportSession,
  warningMessage,
} from "@/lib/project-import";
import { environmentsQuery } from "@/lib/queries";

const kindNames: Record<ChangeKind, ReturnType<typeof msg>> = {
  environments: msg`Environments`,
  unit_types: msg`Unit types`,
  attributes: msg`Attributes`,
  segments: msg`Segments`,
  flags: msg`Flags`,
  experiments: msg`Experiments`,
};

/**
 * Imports a project document in merge mode: choose the file, read what a
 * dry run says it would create, update and delete, then apply exactly
 * that. The apply is sent only if the project is still as the dry run
 * found it. The step and the document survive a reload, and a document
 * the browser could not keep returns the flow to the upload.
 */
export function ImportProject({ project }: { project: string }) {
  const [session, setSession] = useState<ImportSession>(() => loadImportSession(project));
  // Names a document chosen, so a dry run of an earlier choice is never taken for this one.
  const [generation, setGeneration] = useState(0);
  const [applied, setApplied] = useState<ImportResult | undefined>();

  const move = (next: ImportSession) => {
    setSession(next);
    saveImportSession(project, next);
  };

  if (applied) {
    return <Applied result={applied} onAgain={() => setApplied(undefined)} />;
  }
  if (session.step === "review" && session.document) {
    return (
      <Review
        project={project}
        document={session.document}
        generation={generation}
        onBack={() => move({ step: "upload", document: session.document })}
        onApplied={(result) => {
          move({ step: "upload" });
          setApplied(result);
        }}
      />
    );
  }
  return (
    <Upload
      document={session.document}
      onChosen={(document) => {
        setGeneration((value) => value + 1);
        move({ step: "upload", document });
      }}
      onContinue={() => move({ step: "review", document: session.document })}
    />
  );
}

function Upload({
  document,
  onChosen,
  onContinue,
}: {
  document: HeldDocument | undefined;
  onChosen: (document: HeldDocument) => void;
  onContinue: () => void;
}) {
  const { t } = useLingui();
  const name = document?.name ?? "";
  const [unreadable, setUnreadable] = useState<string | undefined>();

  const choose = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    const mediaType = documentMediaType(file.name);
    if (mediaType === undefined) {
      setUnreadable(t`Choose a .json, .yaml or .yml file.`);
      return;
    }
    setUnreadable(undefined);
    onChosen({ name: file.name, mediaType, text: await file.text() });
  };

  return (
    <div className="flex flex-col gap-4">
      <Field
        label={t`Project document`}
        hint={t`A JSON or YAML file, such as an export of this or another project.`}
        error={unreadable}
      >
        <Input type="file" accept=".json,.yaml,.yml" onChange={(event) => void choose(event)} />
      </Field>
      {document ? (
        <p className="text-sm">
          <Trans>
            Chosen: <code className="font-mono">{name}</code>
          </Trans>
        </p>
      ) : null}
      <div>
        <Button disabled={document === undefined} onClick={onContinue}>
          <Trans>Check what it would change</Trans>
        </Button>
      </div>
    </div>
  );
}

function Review({
  project,
  document,
  generation,
  onBack,
  onApplied,
}: {
  project: string;
  document: HeldDocument;
  generation: number;
  onBack: () => void;
  onApplied: (result: ImportResult) => void;
}) {
  const queryClient = useQueryClient();
  const name = document.name;
  const dryRun = useQuery({
    queryKey: ["import-dry-run", project, generation],
    queryFn: () => dryRunImport(project, "merge", document),
    // A dry run answers for the moment it ran: it is repeated only when asked for.
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const environments = useQuery(environmentsQuery(project));
  const apply = useMutation({
    mutationFn: (etag: string) => applyImport(project, "merge", document, etag),
    // The refusals are explained under the dry run, where the member decides what to do.
    meta: { toast: false },
    onSuccess: async (result) => {
      // The dry run is left alone: it would run again against the project it just changed.
      await queryClient.invalidateQueries({
        predicate: (query) => query.queryKey[0] !== "import-dry-run",
      });
      onApplied(result);
    },
  });

  const again = () => {
    apply.reset();
    void dryRun.refetch();
  };

  let body: ReactNode;
  // The environments name the protected ones the import writes to, so Apply
  // waits for them rather than being offered before that notice can show.
  if (dryRun.isPending || dryRun.isFetching || environments.isPending) {
    body = (
      <p role="status" className="text-sm text-muted-foreground">
        <Trans>Checking what the import would change…</Trans>
      </p>
    );
  } else if (dryRun.isError) {
    body = <Refusal error={dryRun.error} />;
  } else {
    const { result, etag } = dryRun.data;
    const touched = protectedEnvironmentsTouched(result, environments.data ?? []);
    const names = new Map((environments.data ?? []).map((env) => [env.key, env.name]));
    const touchedNames = touched.map((key) => names.get(key) ?? key).join(", ");
    const unchanged = isUnchanged(result);
    const moved = apply.error instanceof ApiError && apply.error.status === 412;
    body = (
      <>
        <Changes result={result} />
        {touched.length > 0 ? (
          <p className="flex items-start gap-2 text-sm">
            <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
            <span>
              <Trans>
                This import writes to protected environments directly, without a change request:{" "}
                {touchedNames}.
              </Trans>
            </span>
          </p>
        ) : null}
        <Warnings result={result} />
        {moved ? (
          <div
            role="alert"
            className="flex flex-col gap-3 rounded-md border border-destructive/50 bg-destructive/5 p-4 text-sm"
          >
            <p className="flex items-start gap-2 font-medium">
              <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
              <Trans>
                The project changed after this check, so nothing was imported. Check again to see
                what the import would change now.
              </Trans>
            </p>
            <div>
              <Button variant="outline" onClick={again}>
                <Trans>Check again</Trans>
              </Button>
            </div>
          </div>
        ) : apply.isError ? (
          <Refusal error={apply.error} />
        ) : null}
        <div className="flex flex-wrap gap-2">
          <Button disabled={unchanged || apply.isPending} onClick={() => apply.mutate(etag)}>
            <Trans>Apply</Trans>
          </Button>
        </div>
      </>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm">
        <Trans>
          Document: <code className="font-mono">{name}</code>
        </Trans>
      </p>
      {body}
      <div>
        <Button variant="outline" onClick={onBack} disabled={apply.isPending}>
          <Trans>Choose another file</Trans>
        </Button>
      </div>
    </div>
  );
}

/** A refusal with the place in the document of each fault the server found. */
function Refusal({ error }: { error: unknown }) {
  const { i18n } = useLingui();
  const refusal = error instanceof ApiError ? error : new ApiError(0, {});
  const requestId = refusal.requestId;
  return (
    <div
      role="alert"
      className="flex flex-col gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm"
    >
      <p className="flex items-start gap-2">
        <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        <span>{problemMessage(i18n, refusal)}</span>
      </p>
      {refusal.violations.length > 0 ? (
        <ul aria-label={i18n._(msg`Faults in the document`)} className="flex flex-col gap-1 pl-6">
          {refusal.violations.map((violation, index) => (
            <li key={index}>
              <code className="font-mono text-xs">{violation.pointer || "/"}</code>{" "}
              {violationMessage(i18n, violation)}
            </li>
          ))}
        </ul>
      ) : null}
      {requestId ? (
        <p className="text-muted-foreground">
          <Trans>Request ID: {requestId}</Trans>
        </p>
      ) : null}
    </div>
  );
}

function Changes({ result }: { result: ImportResult }) {
  const { i18n } = useLingui();
  const kinds = changedKinds(result);
  if (kinds.length === 0) {
    return (
      <p className="text-sm font-medium">
        <Trans>Unchanged. The project already matches this document.</Trans>
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      {kinds.map(({ kind, changes }) => (
        <KindChanges key={kind} name={i18n._(kindNames[kind])} changes={changes} />
      ))}
    </div>
  );
}

function KindChanges({ name, changes }: { name: string; changes: ImportKindChanges }) {
  const { t } = useLingui();
  return (
    <section aria-label={name} className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">{name}</h3>
      <ul className="flex flex-col gap-2 text-sm">
        {changes.created.map((key) => (
          <li key={`created-${key}`} className="flex items-center gap-2">
            <Badge>{t`Created`}</Badge>
            <code className="font-mono">{key}</code>
          </li>
        ))}
        {changes.updated.map((update) => (
          <UpdatedEntry key={`updated-${update.key}`} update={update} />
        ))}
        {changes.deleted.map((key) => (
          <li key={`deleted-${key}`} className="flex items-center gap-2">
            <Badge variant="destructive">{t`Deleted`}</Badge>
            <code className="font-mono">{key}</code>
          </li>
        ))}
      </ul>
    </section>
  );
}

function UpdatedEntry({ update }: { update: ImportUpdate }) {
  const { t } = useLingui();
  const key = update.key;
  return (
    <li className="flex flex-col gap-1">
      <span className="flex items-center gap-2">
        <Badge variant="muted">{t`Updated`}</Badge>
        <code className="font-mono">{key}</code>
      </span>
      <ul
        aria-label={t`Changes to ${key}`}
        className="ml-4 flex flex-col gap-0.5 border-l pl-3 text-xs"
      >
        {patchLines(update).map((line, index) => (
          <li key={index} className="break-all">
            <PatchSentence line={line} />
          </li>
        ))}
      </ul>
    </li>
  );
}

function PatchSentence({ line }: { line: PatchLine }) {
  const path = <code className="font-mono">{line.path}</code>;
  const value = <code className="font-mono">{line.value}</code>;
  switch (line.op) {
    case "add":
      return (
        <Trans>
          Added {path}: {value}
        </Trans>
      );
    case "remove":
      return <Trans>Removed {path}</Trans>;
    case "replace":
      return (
        <Trans>
          Set {path} to {value}
        </Trans>
      );
    case "move": {
      const from = <code className="font-mono">{line.from}</code>;
      return (
        <Trans>
          Moved {from} to {path}
        </Trans>
      );
    }
    default: {
      const from = <code className="font-mono">{line.from}</code>;
      return (
        <Trans>
          Copied {from} to {path}
        </Trans>
      );
    }
  }
}

function Warnings({ result }: { result: ImportResult }) {
  const { i18n, t } = useLingui();
  const count = result.warnings.length;
  if (count === 0) return null;
  return (
    <section aria-label={t`Warnings`} className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">
        <Plural value={count} one="# warning" other="# warnings" />
      </h3>
      <ul className="flex flex-col gap-1.5 text-sm">
        {result.warnings.map((warning, index) => (
          <li key={index} className="flex flex-col gap-0.5">
            <span>{warningMessage(i18n, warning)}</span>
            <span className="text-xs text-muted-foreground">
              <code className="font-mono">{warning.code}</code>{" "}
              <code className="font-mono">{warning.pointer || "/"}</code>
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Applied({ result, onAgain }: { result: ImportResult; onAgain: () => void }) {
  return (
    <div className="flex flex-col gap-4">
      <p role="status" className="text-sm font-medium">
        <Trans>Imported.</Trans>
      </p>
      <Changes result={result} />
      <Warnings result={result} />
      <div>
        <Button variant="outline" onClick={onAgain}>
          <Trans>Import another document</Trans>
        </Button>
      </div>
    </div>
  );
}
