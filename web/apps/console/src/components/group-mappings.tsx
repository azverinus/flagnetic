import type { GroupMappings, Project, Role } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDownIcon, ArrowUpIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { type FormEvent, useState } from "react";

import { QueryError } from "@/components/query-error";
import { RoleField } from "@/components/role-field";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { FormActions, FormRefusal } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  bodyOf,
  checkRows,
  groupMappingsQuery,
  type MappingRow,
  type MappingTarget,
  mappingRows,
  moveRow,
  newRow,
  replaceGroupMappings,
  type RowErrors,
  runSummary,
  sameMappings,
} from "@/lib/group-mappings";
import { formRefusal } from "@/lib/problem-messages";
import { meQuery, projectsQuery, rolesQuery } from "@/lib/queries";

/**
 * What a provider's directory groups grant, in the order the provider
 * evaluates them: a role in a project, or instance admin. Saving replaces
 * the whole list and the server runs it at once for every member the
 * provider knows, so the page says how many grants that made and removed.
 */
export function GroupMappingsSection({ provider }: { provider: string }) {
  const mappings = useQuery(groupMappingsQuery(provider));
  const projects = useQuery(projectsQuery);
  const roles = useQuery(rolesQuery);
  const failed = [mappings, projects, roles].find(
    (read) => read.isError && read.data === undefined,
  );

  return (
    <section aria-labelledby="group-mappings" className="flex flex-col gap-4">
      <h2 id="group-mappings" className="text-base font-semibold">
        <Trans>Group mappings</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Members of a directory group hold the role in the project, or are instance admins, that
          the group maps to. A sign-in through the provider grants what the member's groups map to
          and removes what they no longer map to. A grant made this way can be removed only here.
        </Trans>
      </p>
      {failed ? (
        <QueryError error={failed.error} onRetry={() => void failed.refetch()} />
      ) : mappings.data === undefined || projects.data === undefined || roles.data === undefined ? (
        <Skeleton aria-busy className="h-24 w-full" />
      ) : (
        <GroupMappingsForm
          provider={provider}
          saved={mappings.data}
          projects={projects.data}
          roles={roles.data}
        />
      )}
    </section>
  );
}

function GroupMappingsForm({
  provider,
  saved,
  projects,
  roles,
}: {
  provider: string;
  saved: GroupMappings;
  projects: readonly Project[];
  roles: readonly Role[];
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const [rows, setRows] = useState<MappingRow[]>(() => mappingRows(saved.items));
  // What the form found wrong, once a save was tried.
  const [errors, setErrors] = useState<(RowErrors | undefined)[]>();
  const [summary, setSummary] = useState<string>();
  const dirty = !sameMappings(rows, saved.items);

  const save = useMutation({
    mutationFn: (body: GroupMappings) => replaceGroupMappings(provider, body),
    meta: { toast: false },
    onSuccess: (run, body) => {
      queryClient.setQueryData(groupMappingsQuery(provider).queryKey, body);
      // Members' grants changed under every project.
      void queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey });
      void queryClient.invalidateQueries({ queryKey: ["members"] });
      // A mapping to instance admin may have promoted or demoted the caller.
      void queryClient.invalidateQueries({ queryKey: meQuery.queryKey });
      setSummary(runSummary(i18n, run));
    },
  });

  const edit = (next: MappingRow[]) => {
    setRows(next);
    setErrors(undefined);
    setSummary(undefined);
    save.reset();
  };
  const change = (id: string, patch: Partial<MappingRow>) =>
    edit(rows.map((row) => (row.id === id ? { ...row, ...patch } : row)));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const found = checkRows(i18n, rows);
    if (found.some((row) => row !== undefined)) {
      setErrors(found);
      return;
    }
    save.mutate(bodyOf(rows));
  };

  const pointers = rows.flatMap((_, at) =>
    ["group", "project", "role", "instance_admin"].map((field) => `/items/${at}/${field}`),
  );

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4">
      {rows.length === 0 ? (
        <EmptyState
          title={<Trans>This provider's groups grant nothing yet.</Trans>}
          description={
            <Trans>
              Add a mapping to give the members of a directory group a role in a project, or make
              them instance admins.
            </Trans>
          }
        />
      ) : (
        <ol aria-label={t`Group mappings`} className="flex flex-col gap-3">
          {rows.map((row, index) => (
            <li key={row.id}>
              <MappingRowFields
                row={row}
                index={index}
                last={index === rows.length - 1}
                projects={projects}
                roles={roles}
                errors={errors?.[index]}
                refusal={save.error}
                onChange={(patch) => change(row.id, patch)}
                onMove={(by) => edit(moveRow(rows, index, by))}
                onRemove={() => edit(rows.filter((other) => other.id !== row.id))}
              />
            </li>
          ))}
        </ol>
      )}
      <div>
        <Button type="button" variant="outline" onClick={() => edit([...rows, newRow()])}>
          <PlusIcon aria-hidden />
          <Trans>Add a mapping</Trans>
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Saving runs the mappings at once for every member the provider knows. Removing a mapping
          takes the access it granted away from them now. The last active instance admin is never
          demoted this way.
        </Trans>
      </p>
      <FormRefusal refusal={formRefusal(save.error, pointers)} />
      {summary ? (
        <p role="status" className="rounded-md border bg-muted p-3 text-sm">
          {summary}
        </p>
      ) : null}
      <FormActions
        submitLabel={save.isPending ? <Trans>Saving…</Trans> : <Trans>Save mappings</Trans>}
        pending={save.isPending || !dirty}
        onCancel={dirty ? () => edit(mappingRows(saved.items)) : undefined}
      />
    </form>
  );
}

function MappingRowFields({
  row,
  index,
  last,
  projects,
  roles,
  errors,
  refusal,
  onChange,
  onMove,
  onRemove,
}: {
  row: MappingRow;
  index: number;
  last: boolean;
  projects: readonly Project[];
  roles: readonly Role[];
  errors: RowErrors | undefined;
  refusal: unknown;
  onChange: (patch: Partial<MappingRow>) => void;
  onMove: (by: -1 | 1) => void;
  onRemove: () => void;
}) {
  const { t } = useLingui();
  const number = index + 1;
  const at = `/items/${index}`;
  // A project deleted since the mapping was saved stays choosable as it is,
  // so the row shows what it holds.
  const known = projects.some((project) => project.key === row.project);
  return (
    <fieldset className="flex flex-col gap-3 rounded-md border p-3">
      <legend className="px-1 text-sm font-medium">
        <Trans>Mapping {number}</Trans>
      </legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field
          label={<Trans>Directory group</Trans>}
          hint={<Trans>Matched exactly as the provider claims it, case included.</Trans>}
          error={errors?.group}
          refusal={refusal}
          pointer={`${at}/group`}
        >
          <Input
            value={row.group}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => onChange({ group: event.target.value })}
          />
        </Field>
        <Field label={<Trans>Maps to</Trans>} refusal={refusal} pointer={`${at}/instance_admin`}>
          <Select
            value={row.target}
            onValueChange={(target) => onChange({ target: target as MappingTarget })}
          >
            <SelectItem value="role">{t`A role in a project`}</SelectItem>
            <SelectItem value="instance_admin">{t`Instance admin`}</SelectItem>
          </Select>
        </Field>
        {row.target === "role" ? (
          <>
            <Field
              label={<Trans>Project</Trans>}
              error={errors?.project}
              refusal={refusal}
              pointer={`${at}/project`}
            >
              <Select
                value={row.project}
                onValueChange={(project) => onChange({ project })}
                placeholder={t`Choose a project`}
              >
                {known || row.project === "" ? null : (
                  <SelectItem value={row.project}>{row.project}</SelectItem>
                )}
                {projects.map((project) => (
                  <SelectItem key={project.key} value={project.key}>
                    {project.name === project.key
                      ? project.key
                      : `${project.name} (${project.key})`}
                  </SelectItem>
                ))}
              </Select>
            </Field>
            <RoleField
              roles={roles}
              value={row.role}
              onChange={(role) => onChange({ role })}
              error={errors?.role}
              refusal={refusal}
              pointer={`${at}/role`}
            />
          </>
        ) : null}
      </div>
      <div className="flex flex-wrap justify-end gap-1">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={t`Move mapping ${number} up`}
          disabled={index === 0}
          onClick={() => onMove(-1)}
        >
          <ArrowUpIcon aria-hidden />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={t`Move mapping ${number} down`}
          disabled={last}
          onClick={() => onMove(1)}
        >
          <ArrowDownIcon aria-hidden />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={t`Remove mapping ${number}`}
          onClick={onRemove}
        >
          <Trash2Icon aria-hidden />
        </Button>
      </div>
    </fieldset>
  );
}
