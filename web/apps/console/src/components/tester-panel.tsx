import {
  type Attribute,
  evaluate,
  type EvaluationContext,
  type EvaluationResult,
  type FlagConfigurationPut,
  type UnitType,
} from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation } from "@tanstack/react-query";
import { CircleAlertIcon, PlusIcon, Trash2Icon, XIcon } from "lucide-react";
import {
  type FormEvent,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";

import { ListValueInput, ScalarValueInput } from "@/components/condition-row";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectItem } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { formatNumber } from "@/i18n/format";
import type { AttributeTypes } from "@/lib/conditions";
import { ApiError, unwrap } from "@/lib/errors";
import { configurationViolationMessage } from "@/lib/flag-detail";
import { problemMessage } from "@/lib/problem-messages";
import type { RuleDraft } from "@/lib/rule-editor";
import {
  buildContext,
  type ContextDraft,
  contextDraftOf,
  contextLabel,
  type MatchedRule,
  matchedRuleOf,
  recentContexts,
  rememberContext,
} from "@/lib/tester";

export type TesterPanelProps = {
  project: string;
  flagKey: string;
  environment: { key: string; name: string };
  /** The saved configuration's rules, as the editor would show them. */
  savedRules: readonly RuleDraft[];
  /** The unsaved draft as a save would send it, with its cards; absent without changes. */
  draft: { configuration: FlagConfigurationPut; rules: readonly RuleDraft[] } | undefined;
  /** What the editor finds wrong in the draft, which the server would refuse too. */
  draftIssues: readonly string[];
  /** Shows the editor's marks on the draft, which the tester's message points to. */
  onDraftIssues: () => void;
  unitTypes: readonly UnitType[];
  attributes: readonly Attribute[];
  types: AttributeTypes;
  /** The card of the rule that served the last result, or none. */
  onMatch: (rule: string | undefined) => void;
  onClose: () => void;
};

type Outcome = { result: EvaluationResult; withDraft: boolean; rule: MatchedRule | undefined };

/** Rows to start from: the context evaluated last in the project, else one unit to fill in. */
function startingDraft(project: string, unitTypes: readonly UnitType[], types: AttributeTypes) {
  const recent = recentContexts(project)[0];
  return recent
    ? contextDraftOf(recent, types)
    : { units: [{ unitType: unitTypes[0]?.key ?? "", unitId: "" }], attributes: [] };
}

/**
 * Evaluating the flag for a context in one environment as the server
 * does, against the saved configuration or the unsaved draft, which is
 * never stored: what it serves, why, which rule decided, and for a
 * rollout the unit's bucket and the salt. It reads nothing a viewer may
 * not, so everyone who can open the flag can explain what a user gets.
 */
export function TesterPanel({
  project,
  flagKey,
  environment,
  savedRules,
  draft,
  draftIssues,
  onDraftIssues,
  unitTypes,
  attributes,
  types,
  onMatch,
  onClose,
}: TesterPanelProps) {
  const { i18n, t } = useLingui();
  const heading = useRef<HTMLHeadingElement>(null);
  const headingId = useId();
  const useDraftId = useId();
  const [rows, setRows] = useState<ContextDraft>(() => startingDraft(project, unitTypes, types));
  const [recent, setRecent] = useState(() => recentContexts(project));
  const [useDraft, setUseDraft] = useState(false);
  const [checked, setChecked] = useState(false);
  // Why the last evaluation was refused: the draft's mistakes, or the request's.
  const [refusal, setRefusal] = useState<{ draft: boolean; messages: string[] }>();
  const [outcome, setOutcome] = useState<Outcome>();
  const built = buildContext(i18n, rows, types);
  const issues = checked ? built.issues : new Map<string, string>();
  const name = environment.name;

  useEffect(() => heading.current?.focus(), []);

  const run = useMutation({
    mutationFn: ({ context, withDraft }: { context: EvaluationContext; withDraft: boolean }) =>
      unwrap(
        evaluate({
          path: { project, environment: environment.key },
          body: {
            context,
            flags: [flagKey],
            ...(withDraft && draft
              ? { draft: { flag: flagKey, configuration: draft.configuration } }
              : {}),
          },
        }),
      ),
    meta: { toast: false },
    onSuccess: (evaluation, { context, withDraft }) => {
      const result = evaluation.flags[flagKey];
      if (!result) return;
      const rule = matchedRuleOf(result.rule_id, withDraft && draft ? draft.rules : savedRules);
      setOutcome({ result, withDraft, rule });
      onMatch(rule?.key);
      rememberContext(project, context);
      setRecent(recentContexts(project));
    },
    onError: (error) => {
      setOutcome(undefined);
      onMatch(undefined);
      const ofDraft =
        error instanceof ApiError
          ? error.violations.filter((violation) => violation.pointer.startsWith("/draft/"))
          : [];
      if (ofDraft.length > 0) {
        setRefusal({
          draft: true,
          messages: ofDraft.map((violation) => configurationViolationMessage(i18n, violation)),
        });
        return;
      }
      setRefusal({
        draft: false,
        messages: [
          error instanceof ApiError
            ? problemMessage(i18n, error)
            : i18n._(msg`The request was refused.`),
        ],
      });
    },
  });

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    setRefusal(undefined);
    if (built.issues.size > 0) return;
    const withDraft = useDraft && draft !== undefined;
    if (withDraft && draftIssues.length > 0) {
      onDraftIssues();
      setOutcome(undefined);
      onMatch(undefined);
      setRefusal({ draft: true, messages: [...draftIssues] });
      return;
    }
    run.mutate({ context: built.context, withDraft });
  };

  const onKeyDown = (event: KeyboardEvent) => {
    // A list opened from the panel is drawn elsewhere in the page and
    // closes on its own Escape; only the panel's own keys close it.
    if (event.key !== "Escape" || !event.currentTarget.contains(event.target as Node)) return;
    event.stopPropagation();
    onClose();
  };

  const setUnit = (index: number, patch: Partial<ContextDraft["units"][number]>) =>
    setRows((current) => ({
      ...current,
      units: current.units.map((row, at) => (at === index ? { ...row, ...patch } : row)),
    }));
  const setAttribute = (index: number, patch: Partial<ContextDraft["attributes"][number]>) =>
    setRows((current) => ({
      ...current,
      attributes: current.attributes.map((row, at) => (at === index ? { ...row, ...patch } : row)),
    }));
  const sortedAttributes = [...attributes].sort((a, b) => (a.key < b.key ? -1 : 1));

  return (
    <aside
      aria-labelledby={headingId}
      onKeyDown={onKeyDown}
      className="fixed inset-x-0 bottom-0 z-30 flex max-h-[60dvh] flex-col gap-4 overflow-y-auto border-t bg-background p-4 shadow-lg lg:top-14 lg:left-auto lg:max-h-none lg:w-[26rem] lg:border-t-0 lg:border-l"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex flex-col gap-1">
          <h2
            ref={heading}
            id={headingId}
            tabIndex={-1}
            className="text-base font-semibold outline-none"
          >
            <Trans>Evaluate for context</Trans>
          </h2>
          <p className="text-sm text-muted-foreground">
            <Trans>In {name}, as the server evaluates the flag.</Trans>
          </p>
        </div>
        <Button type="button" variant="ghost" size="icon" onClick={onClose}>
          <XIcon aria-hidden />
          <span className="sr-only">
            <Trans>Close the tester</Trans>
          </span>
        </Button>
      </div>
      <form onSubmit={submit} noValidate className="flex flex-col gap-4">
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-sm font-medium">
            <Trans>Units</Trans>
          </legend>
          {rows.units.map((row, index) => {
            const n = index + 1;
            const issue = issues.get(`unit-${index}`);
            return (
              <div key={index} className="flex flex-col gap-1">
                <div className="flex items-center gap-2">
                  <Select
                    aria-label={t`Unit type (unit ${n})`}
                    value={row.unitType}
                    onValueChange={(unitType) => setUnit(index, { unitType })}
                    className="w-36 shrink-0"
                  >
                    {unitTypes.map((type) => (
                      <SelectItem key={type.key} value={type.key}>
                        {type.key}
                      </SelectItem>
                    ))}
                  </Select>
                  <Input
                    aria-label={t`Unit ID (unit ${n})`}
                    autoComplete="off"
                    value={row.unitId}
                    aria-invalid={issue ? true : undefined}
                    onChange={(event) => setUnit(index, { unitId: event.target.value })}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={t`Remove unit ${n}`}
                    onClick={() =>
                      setRows((current) => ({
                        ...current,
                        units: current.units.filter((_, at) => at !== index),
                      }))
                    }
                  >
                    <Trash2Icon aria-hidden />
                  </Button>
                </div>
                {issue ? <FieldIssue>{issue}</FieldIssue> : null}
              </div>
            );
          })}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="w-fit"
            disabled={unitTypes.length === 0}
            onClick={() =>
              setRows((current) => ({
                ...current,
                units: [...current.units, { unitType: unitTypes[0]?.key ?? "", unitId: "" }],
              }))
            }
          >
            <PlusIcon aria-hidden />
            <Trans>Add unit</Trans>
          </Button>
        </fieldset>
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-sm font-medium">
            <Trans>Attributes</Trans>
          </legend>
          {rows.attributes.map((row, index) => {
            const n = index + 1;
            const issue = issues.get(`attribute-${index}`);
            const type = types.get(row.attribute) ?? "string";
            return (
              <div key={index} className="flex flex-col gap-1">
                <div className="flex flex-wrap items-center gap-2">
                  <Select
                    aria-label={t`Attribute ${n}`}
                    value={row.attribute}
                    placeholder={t`Choose an attribute`}
                    onValueChange={(attribute) =>
                      setAttribute(index, { attribute, text: "", list: [] })
                    }
                    className="w-36 shrink-0"
                  >
                    {sortedAttributes.map((attribute) => (
                      <SelectItem key={attribute.key} value={attribute.key}>
                        {attribute.key}
                      </SelectItem>
                    ))}
                  </Select>
                  <div className="min-w-0 flex-1">
                    {type === "list" ? (
                      <ListValueInput
                        label={t`Value (attribute ${n})`}
                        type={type}
                        value={row.list}
                        onChange={(list) => setAttribute(index, { list })}
                        disabled={row.attribute === ""}
                        invalid={issue !== undefined}
                      />
                    ) : (
                      <ScalarValueInput
                        label={t`Value (attribute ${n})`}
                        type={type}
                        value={row.text}
                        onChange={(text) => setAttribute(index, { text })}
                        disabled={row.attribute === ""}
                        invalid={issue !== undefined}
                      />
                    )}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={t`Remove attribute ${n}`}
                    onClick={() =>
                      setRows((current) => ({
                        ...current,
                        attributes: current.attributes.filter((_, at) => at !== index),
                      }))
                    }
                  >
                    <Trash2Icon aria-hidden />
                  </Button>
                </div>
                {issue ? <FieldIssue>{issue}</FieldIssue> : null}
              </div>
            );
          })}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="w-fit"
            onClick={() =>
              setRows((current) => ({
                ...current,
                attributes: [...current.attributes, { attribute: "", text: "", list: [] }],
              }))
            }
          >
            <PlusIcon aria-hidden />
            <Trans>Add attribute</Trans>
          </Button>
        </fieldset>
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-2">
            <Switch
              id={useDraftId}
              checked={useDraft && draft !== undefined}
              disabled={draft === undefined}
              onCheckedChange={setUseDraft}
            />
            <Label htmlFor={useDraftId}>
              <Trans>Use unsaved draft</Trans>
            </Label>
          </div>
          {draft === undefined ? (
            <p className="text-xs text-muted-foreground">
              <Trans>The configuration has no unsaved changes.</Trans>
            </p>
          ) : null}
        </div>
        <Button type="submit" className="w-fit" disabled={run.isPending}>
          <Trans>Evaluate</Trans>
        </Button>
      </form>
      {refusal ? (
        <div role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          {refusal.draft ? (
            <div className="flex flex-col gap-1">
              <p>
                <Trans>The unsaved draft cannot be evaluated as it is:</Trans>
              </p>
              <ul className="flex list-disc flex-col gap-1 pl-4">
                {refusal.messages.map((message, index) => (
                  <li key={index}>{message}</li>
                ))}
              </ul>
            </div>
          ) : (
            <p>{refusal.messages.join(" ")}</p>
          )}
        </div>
      ) : null}
      {outcome ? <TesterResult outcome={outcome} /> : null}
      {recent.length > 0 ? (
        <section aria-label={t`Recent contexts`} className="flex flex-col gap-2">
          <h3 className="text-sm font-medium">
            <Trans>Recent contexts</Trans>
          </h3>
          <ul className="flex flex-col gap-1">
            {recent.map((context, index) => (
              <li key={index}>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-auto max-w-full justify-start py-1 text-left font-mono text-xs break-all whitespace-normal"
                  onClick={() => {
                    setRows(contextDraftOf(context, types));
                    setChecked(false);
                  }}
                >
                  {contextLabel(context) || t`Empty context`}
                </Button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </aside>
  );
}

function FieldIssue({ children }: { children: ReactNode }) {
  return <p className="text-xs text-destructive">{children}</p>;
}

/** A result's reason as a member reads it, with what it means. */
function useReason(reason: string): { label: string; meaning: string } {
  const { t } = useLingui();
  switch (reason) {
    case "off":
      return { label: t`Off`, meaning: t`The flag is off here: the off variation is served.` };
    case "override":
      return { label: t`Override`, meaning: t`The unit is pinned to a variation.` };
    case "rule":
      return {
        label: t`Matched rule`,
        meaning: t`The first rule whose conditions all hold served.`,
      };
    case "rollout":
      return {
        label: t`Rollout`,
        meaning: t`A rule's rollout served: the unit's bucket is below its percentage.`,
      };
    case "fallthrough":
      return {
        label: t`Fallthrough`,
        meaning: t`No rule matched: the fallthrough variation is served.`,
      };
    case "prerequisite_failed":
      return {
        label: t`Prerequisite failed`,
        meaning: t`A required flag does not serve the variation required: the off variation is served.`,
      };
    default:
      return { label: t`Error`, meaning: t`The flag could not be evaluated.` };
  }
}

/** One fact of the result, its term naming the value for assistive technology. */
function Fact({ term, children }: { term: ReactNode; children: ReactNode }) {
  const id = useId();
  return (
    <>
      <dt id={id} className="text-xs font-medium text-muted-foreground">
        {term}
      </dt>
      <dd aria-labelledby={id} className="min-w-0 break-words">
        {children}
      </dd>
    </>
  );
}

function TesterResult({ outcome }: { outcome: Outcome }) {
  const { i18n, t } = useLingui();
  const { result, withDraft, rule } = outcome;
  const reason = useReason(result.reason);
  const total = formatNumber(i18n, 10_000);
  const bucket = result.bucket === undefined ? undefined : formatNumber(i18n, result.bucket);
  const position = rule?.position;

  return (
    <section
      aria-label={t`Evaluation result`}
      className="flex flex-col gap-3 rounded-md border p-3 text-sm"
    >
      <p className="text-muted-foreground">
        {withDraft ? (
          <Trans>Evaluated with the unsaved draft.</Trans>
        ) : (
          <Trans>Evaluated with the saved configuration.</Trans>
        )}
      </p>
      {result.override_influenced ? (
        <p className="rounded-md border border-primary/40 bg-primary/5 p-2">
          <span className="font-medium">
            <Trans>Override.</Trans>
          </span>{" "}
          {result.reason === "override" ? (
            <Trans>An override serves this unit ID; rules are not consulted.</Trans>
          ) : (
            <Trans>An override of a required flag decided this result.</Trans>
          )}
        </p>
      ) : null}
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2">
        <Fact term={<Trans>Value</Trans>}>
          {result.value === undefined ? (
            <span className="text-muted-foreground">
              <Trans>No value</Trans>
            </span>
          ) : (
            <code className="font-mono text-xs">{JSON.stringify(result.value)}</code>
          )}
        </Fact>
        {result.variation !== undefined ? (
          <Fact term={<Trans>Variation</Trans>}>
            <code className="font-mono text-xs">{result.variation}</code>
          </Fact>
        ) : null}
        <Fact term={<Trans>Evaluation reason</Trans>}>
          <span className="flex flex-col">
            <span className="font-medium">{reason.label}</span>
            <span className="text-muted-foreground">{reason.meaning}</span>
          </span>
        </Fact>
        {result.rule_id !== undefined ? (
          <Fact term={<Trans>Rule</Trans>}>
            {position === undefined ? (
              <code className="font-mono text-xs">{result.rule_id}</code>
            ) : rule?.unsaved ? (
              <Trans>Rule {position}, new in the draft</Trans>
            ) : (
              <Trans>Rule {position}</Trans>
            )}
          </Fact>
        ) : null}
        {bucket !== undefined ? (
          <Fact term={<Trans>Bucket</Trans>}>
            <Trans>
              {bucket} of {total}
            </Trans>
          </Fact>
        ) : null}
        {result.salt ? (
          <Fact term={<Trans>Salt</Trans>}>
            <code className="font-mono text-xs break-all">{result.salt}</code>
          </Fact>
        ) : null}
        {result.prerequisite ? (
          <Fact term={<Trans>Prerequisite</Trans>}>
            <code className="font-mono text-xs">{result.prerequisite}</code>
          </Fact>
        ) : null}
        {result.error_code ? (
          <Fact term={<Trans>Error</Trans>}>
            <code className="font-mono text-xs">{result.error_code}</code>
          </Fact>
        ) : null}
      </dl>
    </section>
  );
}
