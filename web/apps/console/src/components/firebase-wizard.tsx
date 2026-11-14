import type { FirebaseImportResult } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Plural, Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRightIcon, TriangleAlertIcon } from "lucide-react";
import { type ChangeEvent, type ReactNode, useState } from "react";

import { ImportChanges, ImportRefusal, ImportWarnings } from "@/components/import-project";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { describeCondition, describeServe } from "@/lib/conditions";
import { ApiError } from "@/lib/errors";
import {
  applyFirebase,
  conditionMappings,
  type FirebaseSession,
  type FirebaseStep,
  type FirebaseTarget,
  dryRunFirebase,
  firebaseSteps,
  loadFirebaseSession,
  parameterRows,
  parseTemplate,
  pruneTemplate,
  saveFirebaseSession,
  setIncluded,
  type Template,
} from "@/lib/firebase-import";
import { flagTypeName } from "@/lib/flags";
import { formatNumber } from "@/i18n/format";
import { protectedEnvironmentsTouched } from "@/lib/project-import";
import { environmentsQuery, unitTypesQuery } from "@/lib/queries";

const stepNames: Record<FirebaseStep, ReturnType<typeof msg>> = {
  upload: msg`Upload`,
  parameters: msg`Parameters`,
  target: msg`Environment`,
  preview: msg`Preview`,
};

/** The unit type percent rollouts bucket on unless the member chooses another. */
const defaultUnitType = "anonymous";

/**
 * Imports a Firebase Remote Config template step by step: upload it,
 * choose the parameters that become flags while reading how each condition
 * maps, choose the environment and the unit type for percent rollouts,
 * read a dry run, apply it, and land on the flags it created or updated.
 * The step, the template and the choices survive a reload; a template
 * the browser could not keep returns the flow to the upload.
 */
export function FirebaseWizard({ project }: { project: string }) {
  const [session, setSession] = useState<FirebaseSession>(() => loadFirebaseSession(project));
  // Names a template chosen, so a dry run of an earlier file is never taken for this one.
  const [generation, setGeneration] = useState(0);
  const [applied, setApplied] = useState<{
    result: FirebaseImportResult;
    environment: string;
  }>();
  const environments = useQuery(environmentsQuery(project));
  const unitTypes = useQuery(unitTypesQuery(project));

  const move = (next: FirebaseSession) => {
    setSession(next);
    saveFirebaseSession(project, next);
  };

  const read = session.text === undefined ? undefined : parseTemplate(session.text);
  const template = read !== undefined && "template" in read ? read.template : undefined;
  const excluded = session.excluded ?? [];
  const target: FirebaseTarget | undefined = (() => {
    // A stored choice is kept only while the project still has it: an environment or unit
    // type deleted since would otherwise be sent to the server and shown as no option.
    const environmentKeys = (environments.data ?? []).map((environment) => environment.key);
    const environment =
      session.environment !== undefined && environmentKeys.includes(session.environment)
        ? session.environment
        : environmentKeys[0];
    const unitTypeKeys = (unitTypes.data ?? []).map((unitType) => unitType.key);
    const unitType =
      session.unitType !== undefined && unitTypeKeys.includes(session.unitType)
        ? session.unitType
        : unitTypeKeys.includes(defaultUnitType)
          ? defaultUnitType
          : unitTypeKeys[0];
    return environment === undefined || unitType === undefined
      ? undefined
      : { environment, unitType };
  })();

  if (applied) {
    return (
      <Summary
        project={project}
        result={applied.result}
        environment={applied.environment}
        onAgain={() => {
          setApplied(undefined);
          setGeneration((value) => value + 1);
        }}
      />
    );
  }

  const current = template === undefined ? "upload" : session.step;
  return (
    <div className="flex flex-col gap-4">
      <StepList current={current} />
      {current === "upload" || template === undefined ? (
        <Upload
          fileName={session.fileName}
          onChosen={(fileName, text) => {
            setGeneration((value) => value + 1);
            move({ step: "upload", fileName, text });
          }}
          onContinue={() => move({ ...session, step: "parameters" })}
          canContinue={template !== undefined}
        />
      ) : environments.isError || unitTypes.isError ? (
        <ImportRefusal error={environments.error ?? unitTypes.error} />
      ) : environments.isPending || unitTypes.isPending ? (
        <p role="status" className="text-sm text-muted-foreground">
          <Trans>Loading the project's environments and unit types…</Trans>
        </p>
      ) : target === undefined ? (
        <p role="alert" className="text-sm text-destructive">
          <Trans>
            The project needs an environment and a unit type before a template can be imported.
          </Trans>
        </p>
      ) : current === "parameters" ? (
        <Parameters
          project={project}
          template={template}
          target={target}
          excluded={excluded}
          generation={generation}
          onToggle={(key, included) =>
            move({ ...session, excluded: setIncluded(excluded, key, included) })
          }
          onBack={() => move({ ...session, step: "upload" })}
          onContinue={() => move({ ...session, step: "target" })}
        />
      ) : current === "target" ? (
        <Target
          project={project}
          target={target}
          onChange={(next) =>
            move({ ...session, environment: next.environment, unitType: next.unitType })
          }
          onBack={() => move({ ...session, step: "parameters" })}
          onContinue={() =>
            move({
              ...session,
              step: "preview",
              environment: target.environment,
              unitType: target.unitType,
            })
          }
        />
      ) : (
        <Preview
          project={project}
          template={pruneTemplate(template, excluded)}
          target={target}
          generation={generation}
          fileName={session.fileName ?? ""}
          onBack={() => move({ ...session, step: "target" })}
          onApplied={(result) => {
            setApplied({ result, environment: target.environment });
            move({ step: "upload" });
          }}
        />
      )}
    </div>
  );
}

function StepList({ current }: { current: FirebaseStep }) {
  const { i18n } = useLingui();
  return (
    <ol aria-label={i18n._(msg`Steps`)} className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
      {firebaseSteps.map((step, index) => (
        <li
          key={step}
          aria-current={step === current ? "step" : undefined}
          className={step === current ? "font-semibold" : "text-muted-foreground"}
        >
          {index + 1}. {i18n._(stepNames[step])}
        </li>
      ))}
    </ol>
  );
}

function Upload({
  fileName,
  canContinue,
  onChosen,
  onContinue,
}: {
  fileName: string | undefined;
  canContinue: boolean;
  onChosen: (fileName: string, text: string) => void;
  onContinue: () => void;
}) {
  const { t } = useLingui();
  const [unreadable, setUnreadable] = useState<string | undefined>();

  const choose = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    let text: string;
    try {
      text = await file.text();
    } catch {
      setUnreadable(t`The browser could not read the file.`);
      return;
    }
    const read = parseTemplate(text);
    if ("error" in read) {
      setUnreadable(
        read.error === "not_json"
          ? t`The file is not JSON. Firebase writes the template as JSON.`
          : t`The file is not a Remote Config template: it has no parameters.`,
      );
      return;
    }
    setUnreadable(undefined);
    onChosen(file.name, text);
  };

  return (
    <div className="flex flex-col gap-4">
      <Field
        label={t`Remote Config template`}
        hint={t`The template as "firebase remoteconfig:get" or the Remote Config REST API returns it, a JSON file.`}
        error={unreadable}
      >
        <Input
          type="file"
          accept=".json,application/json"
          onChange={(event) => void choose(event)}
        />
      </Field>
      {fileName !== undefined && unreadable === undefined ? (
        <p className="text-sm">
          <Trans>
            Chosen: <code className="font-mono">{fileName}</code>
          </Trans>
        </p>
      ) : null}
      <div>
        <Button disabled={!canContinue || unreadable !== undefined} onClick={onContinue}>
          <Trans>Continue</Trans>
        </Button>
      </div>
    </div>
  );
}

function Parameters({
  project,
  template,
  target,
  excluded,
  generation,
  onToggle,
  onBack,
  onContinue,
}: {
  project: string;
  template: Template;
  target: FirebaseTarget;
  excluded: readonly string[];
  generation: number;
  onToggle: (key: string, included: boolean) => void;
  onBack: () => void;
  onContinue: () => void;
}) {
  const { i18n, t } = useLingui();
  // The whole template is read, so unticking a parameter never hides how a condition maps.
  // A refusal does not hold the member here: leaving out the parameter at fault is how it is
  // fixed, and the preview's dry run reads only the parameters still included.
  const mapping = useQuery({
    queryKey: [
      "firebase-dry-run",
      project,
      "mapping",
      generation,
      target.environment,
      target.unitType,
    ],
    queryFn: () => dryRunFirebase(project, template, target),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const rows = parameterRows(template, mapping.data?.result.flags, excluded);
  const included = rows.filter((row) => row.included).length;

  return (
    <div className="flex flex-col gap-6">
      <section aria-labelledby="firebase-parameters" className="flex flex-col gap-2">
        <h3 id="firebase-parameters" className="text-sm font-semibold">
          <Trans>Parameters as flags</Trans>
        </h3>
        <p className="text-sm text-muted-foreground">
          <Trans>
            Each parameter you include becomes the flag with its key, typed by its value type, with
            its group as a tag. A parameter you leave out is not imported.
          </Trans>
        </p>
        <div className="overflow-x-auto rounded-md border">
          <table className="w-full text-sm">
            <caption className="sr-only">
              <Trans>The parameters of the template</Trans>
            </caption>
            <thead>
              <tr className="border-b text-left text-muted-foreground">
                <th scope="col" className="p-3 font-medium">
                  <Trans>Include</Trans>
                </th>
                <th scope="col" className="p-3 font-medium">
                  <Trans>Flag key</Trans>
                </th>
                <th scope="col" className="p-3 font-medium">
                  <Trans>Type</Trans>
                </th>
                <th scope="col" className="p-3 font-medium">
                  <Trans>Tags</Trans>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const key = row.key;
                return (
                  <tr key={row.key} className="border-b last:border-b-0">
                    <td className="p-3">
                      <input
                        type="checkbox"
                        aria-label={t`Include ${key}`}
                        checked={row.included}
                        onChange={(event) => onToggle(row.key, event.target.checked)}
                        className="size-4 accent-primary"
                      />
                    </td>
                    <td className="p-3">
                      <code className="font-mono text-xs">{row.key}</code>
                    </td>
                    <td className="p-3">{flagTypeName(i18n, row.type)}</td>
                    <td className="p-3">
                      {row.tags.length === 0 ? (
                        <span className="text-muted-foreground">—</span>
                      ) : (
                        row.tags.join(", ")
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <section aria-labelledby="firebase-conditions" className="flex flex-col gap-2">
        <h3 id="firebase-conditions" className="text-sm font-semibold">
          <Trans>Conditions</Trans>
        </h3>
        <p className="text-sm text-muted-foreground">
          <Trans>
            Each Firebase condition becomes one rule per alternative, in the template's order. A
            condition the server cannot reproduce is skipped, and said so.
          </Trans>
        </p>
        {mapping.isPending || mapping.isFetching ? (
          <p role="status" className="text-sm text-muted-foreground">
            <Trans>Reading how the conditions map…</Trans>
          </p>
        ) : mapping.isError ? (
          <ImportRefusal error={mapping.error} />
        ) : (
          <ConditionList template={template} result={mapping.data.result} />
        )}
      </section>

      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={onBack}>
          <Trans>Back</Trans>
        </Button>
        <Button
          disabled={included === 0 || mapping.isPending || mapping.isFetching}
          onClick={onContinue}
        >
          <Plural
            value={included}
            one="Continue with # parameter"
            other="Continue with # parameters"
          />
        </Button>
      </div>
    </div>
  );
}

function ConditionList({ template, result }: { template: Template; result: FirebaseImportResult }) {
  const { i18n, t } = useLingui();
  const mappings = conditionMappings(template, result);
  if (mappings.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        <Trans>The template has no conditions.</Trans>
      </p>
    );
  }
  const skipped = mappings.filter((mapping) => mapping.skippedFor.length > 0);
  return (
    <div className="flex flex-col gap-3">
      {skipped.length > 0 ? (
        <section aria-label={t`Conditions that are skipped`} className="flex flex-col gap-1">
          <h4 className="flex items-center gap-2 text-sm font-medium">
            <TriangleAlertIcon aria-hidden className="size-4 text-destructive" />
            <Trans>Conditions that are skipped</Trans>
          </h4>
          <ul className="flex flex-col gap-1 text-sm">
            {skipped.map((mapping) => {
              const parameters = mapping.skippedFor.join(", ");
              const name = mapping.name;
              return (
                <li key={mapping.name}>
                  <Trans>
                    <code className="font-mono">{name}</code> is skipped for {parameters}.
                  </Trans>
                </li>
              );
            })}
          </ul>
        </section>
      ) : null}
      <ul aria-label={t`Condition mapping`} className="flex flex-col gap-2">
        {mappings.map((mapping) => {
          const usedBy = mapping.parameters.join(", ");
          return (
            <li key={mapping.name} className="flex flex-col gap-1.5 rounded-md border p-3 text-sm">
              <div className="flex flex-wrap items-baseline gap-2">
                <span className="font-medium">{mapping.name}</span>
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all">
                  {mapping.expression}
                </code>
              </div>
              {mapping.rules.length === 0 ? (
                <p className="text-muted-foreground">
                  {mapping.parameters.length === 0 ? (
                    <Trans>No parameter uses it.</Trans>
                  ) : (
                    <Trans>It becomes no rule.</Trans>
                  )}
                </p>
              ) : (
                <ul className="flex flex-col gap-1">
                  {mapping.rules.map((rule, index) => {
                    const rollout = rule.rollout;
                    const percent = formatNumber(i18n, (rollout?.percentage ?? 0) / 100);
                    const unitType = rollout?.unitType ?? "";
                    const parts = [
                      ...rule.conditions.map((condition) => describeCondition(i18n, condition)),
                      ...(rollout === undefined
                        ? []
                        : [i18n._(msg`${percent}% of ${unitType} units`)]),
                    ];
                    return (
                      <li key={index} className="flex items-start gap-2">
                        <ArrowRightIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
                        <span>{parts.join(i18n._(msg` and `)) || i18n._(msg`every context`)}</span>
                      </li>
                    );
                  })}
                </ul>
              )}
              {mapping.parameters.length > 0 ? (
                <p className="text-xs text-muted-foreground">
                  <Trans>Used by {usedBy}</Trans>
                </p>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Target({
  project,
  target,
  onChange,
  onBack,
  onContinue,
}: {
  project: string;
  target: FirebaseTarget;
  onChange: (target: FirebaseTarget) => void;
  onBack: () => void;
  onContinue: () => void;
}) {
  const { t } = useLingui();
  const environments = useQuery(environmentsQuery(project));
  const unitTypes = useQuery(unitTypesQuery(project));
  return (
    <div className="flex flex-col gap-4">
      <Field
        label={t`Environment`}
        hint={t`The flags are on here, serving what the template says. Every other environment gets them off, serving the default.`}
        className="w-72"
      >
        <Select
          value={target.environment}
          onValueChange={(environment) => onChange({ ...target, environment })}
        >
          {(environments.data ?? []).map((environment) => (
            <SelectItem key={environment.key} value={environment.key}>
              {environment.name}
              {environment.protected ? ` (${t`protected`})` : ""}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field
        label={t`Unit type for percent rollouts`}
        hint={t`Firebase's percent conditions become rollouts that bucket this unit type.`}
        className="w-72"
      >
        <Select
          value={target.unitType}
          onValueChange={(unitType) => onChange({ ...target, unitType })}
        >
          {(unitTypes.data ?? []).map((unitType) => (
            <SelectItem key={unitType.key} value={unitType.key}>
              {unitType.key}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={onBack}>
          <Trans>Back</Trans>
        </Button>
        <Button onClick={onContinue}>
          <Trans>Preview the import</Trans>
        </Button>
      </div>
    </div>
  );
}

function Preview({
  project,
  template,
  target,
  generation,
  fileName,
  onBack,
  onApplied,
}: {
  project: string;
  template: Template;
  target: FirebaseTarget;
  generation: number;
  fileName: string;
  onBack: () => void;
  onApplied: (result: FirebaseImportResult) => void;
}) {
  const queryClient = useQueryClient();
  const dryRun = useQuery({
    queryKey: [
      "import-dry-run",
      project,
      "firebase",
      generation,
      target.environment,
      target.unitType,
      Object.keys(template.parameters ?? {}).join(),
      Object.values(template.parameterGroups ?? {})
        .flatMap((group) => Object.keys(group.parameters ?? {}))
        .join(),
    ],
    queryFn: () => dryRunFirebase(project, template, target),
    // A dry run answers for the moment it ran: it is repeated only when asked for.
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const environments = useQuery(environmentsQuery(project));
  const apply = useMutation({
    mutationFn: (etag: string) => applyFirebase(project, template, target, etag),
    // The refusals are explained under the dry run, where the member decides what to do.
    meta: { toast: false },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({
        predicate: (query) => query.queryKey[0] !== "import-dry-run",
      });
      onApplied(result);
    },
  });

  let body: ReactNode;
  if (dryRun.isPending || dryRun.isFetching || environments.isPending) {
    body = (
      <p role="status" className="text-sm text-muted-foreground">
        <Trans>Checking what the import would change…</Trans>
      </p>
    );
  } else if (dryRun.isError) {
    body = <ImportRefusal error={dryRun.error} />;
  } else {
    const { result, etag } = dryRun.data;
    const touched = protectedEnvironmentsTouched(result, environments.data ?? []);
    const names = new Map((environments.data ?? []).map((env) => [env.key, env.name]));
    const touchedNames = touched.map((key) => names.get(key) ?? key).join(", ");
    const unchanged = result.flags.length === 0 || isNothing(result);
    const moved = apply.error instanceof ApiError && apply.error.status === 412;
    body = (
      <>
        <ImportChanges result={result} />
        <Flags result={result} preview />
        <Attributes result={result} preview />
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
        <ImportWarnings result={result} />
        {moved ? (
          <div
            role="alert"
            className="flex flex-col gap-3 rounded-md border border-destructive/50 bg-destructive/5 p-4 text-sm"
          >
            <p className="font-medium">
              <Trans>
                The project changed after this check, so nothing was imported. Check again to see
                what the import would change now.
              </Trans>
            </p>
            <div>
              <Button
                variant="outline"
                onClick={() => {
                  apply.reset();
                  void dryRun.refetch();
                }}
              >
                <Trans>Check again</Trans>
              </Button>
            </div>
          </div>
        ) : apply.isError ? (
          <ImportRefusal error={apply.error} />
        ) : null}
        <div>
          <Button disabled={unchanged || apply.isPending} onClick={() => apply.mutate(etag)}>
            <Trans>Apply</Trans>
          </Button>
        </div>
      </>
    );
  }

  const environment = target.environment;
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm">
        <Trans>
          Template: <code className="font-mono">{fileName}</code>, into{" "}
          <code className="font-mono">{environment}</code>
        </Trans>
      </p>
      {body}
      <div>
        <Button variant="outline" onClick={onBack} disabled={apply.isPending}>
          <Trans>Back</Trans>
        </Button>
      </div>
    </div>
  );
}

/** Whether the import would change nothing, which leaves nothing to apply. */
function isNothing(result: FirebaseImportResult): boolean {
  return Object.values(result.changes).every(
    (kind) => kind.created.length + kind.updated.length + kind.deleted.length === 0,
  );
}

/** The flags of the template as the project would store them, with each one's rules. */
function Flags({ result, preview }: { result: FirebaseImportResult; preview?: boolean }) {
  const { i18n, t } = useLingui();
  const created = new Set(result.changes.flags.created);
  if (result.flags.length === 0) return null;
  return (
    <section
      aria-label={preview ? t`Flags and rules` : t`Imported flags`}
      className="flex flex-col gap-2"
    >
      <h3 className="text-sm font-semibold">
        {preview ? <Trans>Flags and rules</Trans> : <Trans>Imported flags</Trans>}
      </h3>
      <ul className="flex flex-col gap-2">
        {result.flags.map((flag) => {
          const fallthrough = flag.fallthrough;
          return (
            <li key={flag.key} className="flex flex-col gap-1.5 rounded-md border p-3 text-sm">
              <div className="flex flex-wrap items-center gap-2">
                <code className="font-mono text-xs">{flag.key}</code>
                <span className="text-muted-foreground">
                  {flagTypeName(i18n, flag.type as Parameters<typeof flagTypeName>[1])}
                </span>
                <Badge variant="muted">{created.has(flag.key) ? t`Created` : t`Updated`}</Badge>
              </div>
              {flag.rules.length === 0 ? (
                <p className="text-muted-foreground">
                  <Trans>No rules; everyone gets {fallthrough}.</Trans>
                </p>
              ) : (
                <ol className="flex list-decimal flex-col gap-1 pl-5">
                  {flag.rules.map((rule, index) => (
                    <li key={rule.id ?? index}>
                      {rule.conditions.length === 0
                        ? i18n._(msg`Every context`)
                        : rule.conditions
                            .map((condition) => describeCondition(i18n, condition))
                            .join(i18n._(msg` and `))}
                      {": "}
                      {describeServe(i18n, rule.serve)}
                    </li>
                  ))}
                </ol>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function Attributes({ result, preview }: { result: FirebaseImportResult; preview?: boolean }) {
  const { t } = useLingui();
  if (result.attributes.length === 0) return null;
  return (
    <section
      aria-label={preview ? t`Attributes to declare` : t`Declared attributes`}
      className="flex flex-col gap-2"
    >
      <h3 className="text-sm font-semibold">
        {preview ? <Trans>Attributes to declare</Trans> : <Trans>Declared attributes</Trans>}
      </h3>
      <ul className="flex flex-wrap gap-2 text-sm">
        {result.attributes.map((attribute) => (
          <li key={attribute.key} className="rounded-md border px-2 py-1">
            <code className="font-mono text-xs">{attribute.key}</code>{" "}
            <span className="text-muted-foreground">{attribute.type}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** What the import did, with a link to every flag it created or updated. */
function Summary({
  project,
  result,
  environment,
  onAgain,
}: {
  project: string;
  result: FirebaseImportResult;
  environment: string;
  onAgain: () => void;
}) {
  const { t } = useLingui();
  const environments = useQuery(environmentsQuery(project));
  const name = environments.data?.find((env) => env.key === environment)?.name ?? environment;
  const created = new Set(result.changes.flags.created);
  const touched = new Set([
    ...result.changes.flags.created,
    ...result.changes.flags.updated.map((update) => update.key),
  ]);
  return (
    <div className="flex flex-col gap-4">
      <p role="status" className="text-sm font-medium">
        <Trans>Imported into {name}.</Trans>
      </p>
      <section aria-label={t`Flags`} className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">
          <Trans>Flags</Trans>
        </h3>
        <ul className="flex flex-col gap-1 text-sm">
          {result.flags
            .filter((flag) => touched.has(flag.key))
            .map((flag) => (
              <li key={flag.key} className="flex items-center gap-2">
                <Link
                  to="/projects/$project/flags/$flag"
                  params={{ project, flag: flag.key }}
                  search={{ environment }}
                  className="font-mono underline underline-offset-4"
                >
                  {flag.key}
                </Link>
                <Badge variant="muted">{created.has(flag.key) ? t`Created` : t`Updated`}</Badge>
              </li>
            ))}
        </ul>
        {touched.size === 0 ? (
          <p className="text-sm text-muted-foreground">
            <Trans>The project already matched the template, so no flag changed.</Trans>
          </p>
        ) : null}
      </section>
      <Attributes result={result} />
      <ImportWarnings result={result} />
      <div>
        <Button variant="outline" onClick={onAgain}>
          <Trans>Import another template</Trans>
        </Button>
      </div>
    </div>
  );
}
