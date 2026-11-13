import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type ChangeEvent, useState } from "react";

import { ImportChanges, ImportRefusal, ImportWarnings } from "@/components/import-project";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/errors";
import {
  createFromDocument,
  documentIdentity,
  documentMediaType,
  dryRunNewProject,
  type HeldDocument,
} from "@/lib/project-import";
import { projectsQuery } from "@/lib/queries";

/**
 * Creates a project from a project document, for an instance admin: the
 * file, the key and name (the document's unless changed), a dry run of
 * what the new project would hold, then the creation, after which the
 * new project is opened. A key the instance already has is explained
 * beside the key.
 */
export function CreateFromDocument() {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [document, setDocument] = useState<HeldDocument | undefined>();
  const [unreadable, setUnreadable] = useState<string | undefined>();
  const [key, setKey] = useState("");
  const [name, setName] = useState("");

  const identity = { key: key.trim(), name: name.trim() };
  const dryRun = useMutation({
    mutationFn: (held: HeldDocument) => dryRunNewProject(held, identity),
    meta: { toast: false },
  });
  const create = useMutation({
    mutationFn: (held: HeldDocument) => createFromDocument(held, identity),
    meta: { toast: false },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey });
      await navigate({ to: "/projects/$project", params: { project: result.project.key } });
    },
  });

  const choose = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    const mediaType = documentMediaType(file.name);
    dryRun.reset();
    create.reset();
    if (mediaType === undefined) {
      setDocument(undefined);
      setUnreadable(t`Choose a .json, .yaml or .yml file.`);
      return;
    }
    setUnreadable(undefined);
    const held = { name: file.name, mediaType, text: await file.text() };
    const defaults = documentIdentity(held);
    setDocument(held);
    setKey(defaults.key);
    setName(defaults.name);
  };
  // What a dry run said holds for the key and name it was run with.
  const edited = () => {
    dryRun.reset();
    create.reset();
  };

  const refusal = create.error ?? dryRun.error;
  const taken = refusal instanceof ApiError && refusal.code === "already_exists";
  const result = dryRun.data?.result;
  const createdKey = result?.project.key ?? "";
  const busy = dryRun.isPending || create.isPending;

  return (
    <div className="flex w-full max-w-2xl flex-col gap-5">
      <Field
        label={t`Project document`}
        hint={t`A JSON or YAML file, such as an export of a project of this or another instance.`}
        error={unreadable}
      >
        <Input type="file" accept=".json,.yaml,.yml" onChange={(event) => void choose(event)} />
      </Field>
      {document ? (
        <>
          <Field
            label={t`Key`}
            hint={t`The document's own unless you change it. It never changes afterwards.`}
            error={taken ? t`This instance already has a project with that key.` : undefined}
          >
            <Input
              autoComplete="off"
              value={key}
              onChange={(event) => {
                setKey(event.target.value);
                edited();
              }}
            />
          </Field>
          <Field label={t`Name`}>
            <Input
              autoComplete="off"
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                edited();
              }}
            />
          </Field>
        </>
      ) : null}
      {refusal && !taken ? <ImportRefusal error={refusal} /> : null}
      {result ? (
        <div className="flex flex-col gap-4">
          <p className="text-sm font-medium">
            <Trans>
              Creates the project <code className="font-mono">{createdKey}</code> holding:
            </Trans>
          </p>
          <ImportChanges result={result} />
          <ImportWarnings result={result} />
        </div>
      ) : null}
      <div className="flex flex-wrap justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          disabled={document === undefined || busy}
          onClick={() => document && dryRun.mutate(document)}
        >
          <Trans>Check what it would create</Trans>
        </Button>
        <Button
          type="button"
          disabled={document === undefined || result === undefined || busy}
          onClick={() => document && create.mutate(document)}
        >
          <Trans>Create from this document</Trans>
        </Button>
      </div>
    </div>
  );
}
