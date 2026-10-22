import {
  type Attribute,
  type AuditEntry,
  type Flag,
  type FlagConfiguration,
  type FlagListItem,
  getFlagConfiguration,
  putFlagConfiguration,
  regenerateRolloutSalt,
  type Segment,
  type UnitType,
} from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleAlertIcon } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { toast } from "sonner";

import { ConflictBanner } from "@/components/conflict-banner";
import { PrerequisitesSection } from "@/components/prerequisites-section";
import { RuleEditor } from "@/components/rule-editor";
import { ConfigurationDiff } from "@/components/structural-diff-view";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field } from "@/components/ui/field";
import { Select, SelectItem } from "@/components/ui/select";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { attributeOptions, type AttributeTypes } from "@/lib/conditions";
import {
  arrive,
  configurationDraft,
  type ConfigurationDraft,
  configurationPutOf,
  type EditorState,
  isDraftDirty,
  placeRefusal,
  prerequisiteIssues,
  reapply,
} from "@/lib/configuration-draft";
import { ApiError, unwrap } from "@/lib/errors";
import { configurationViolationMessage, entryOfVersion } from "@/lib/flag-detail";
import { ifMatch } from "@/lib/http";
import { problemMessage } from "@/lib/problem-messages";
import { flagEntriesQuery } from "@/lib/queries";
import { type RuleDraft, ruleIssues, withRule } from "@/lib/rule-editor";
import { diffConfigurations, fingerprint } from "@/lib/structural-diff";
import { useConfigurationAnswer } from "@/lib/use-configuration-answer";

/** What the project declares that a configuration names. */
export type Declarations = {
  attributes: readonly Attribute[];
  unitTypes: readonly UnitType[];
  segments: readonly Segment[];
  /** The project's flags that are not archived. */
  flags: readonly FlagListItem[];
};

export type ConfigurationEditorProps = {
  project: string;
  flag: Flag;
  /** The configuration as the server last answered it. */
  configuration: FlagConfiguration;
  environment: { id: string; name: string };
  declarations: Declarations;
  /** The overrides, written on their own, shown in their place in evaluation order. */
  overrides: ReactNode;
  /** Why the configuration cannot be edited here, when it cannot. */
  readOnly?: string;
};

/** A save met a newer version: the version the draft was based on, the newer one, and who saved it. */
type Conflict = {
  from: FlagConfiguration;
  fresh: FlagConfiguration;
  entry: AuditEntry | undefined;
};

/** A refusal of a save, by where the editor shows each of its violations. */
type Refusal = { at: Map<string, string>; rest: string[] };

/**
 * A flag's configuration in one environment as a draft, laid out in the
 * order evaluation reads it: the off variation, the overrides, the
 * prerequisites, the rules and the fallthrough. The draft remembers the
 * version it is based on; Save replaces the configuration under that
 * version, and a save that meets a newer one shows who saved it and what
 * they changed, and reapplies the draft onto it or discards it.
 */
export function ConfigurationEditor({
  project,
  flag,
  configuration,
  environment,
  declarations,
  overrides,
  readOnly,
}: ConfigurationEditorProps) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const name = environment.name;

  const types: AttributeTypes = useMemo(
    () =>
      new Map([
        ...declarations.unitTypes.map((type) => [type.key, "string"] as const),
        ...declarations.attributes.map((attribute) => [attribute.key, attribute.type] as const),
      ]),
    [declarations.attributes, declarations.unitTypes],
  );
  const attributes = useMemo(
    () => attributeOptions(declarations.attributes, declarations.unitTypes),
    [declarations.attributes, declarations.unitTypes],
  );
  const otherFlags = useMemo(
    () =>
      new Map(
        declarations.flags
          .filter((other) => other.key !== flag.key && other.archived_at === null)
          .map((other) => [other.key, other.variations.map((variation) => variation.key)]),
      ),
    [declarations.flags, flag.key],
  );

  const [state, setState] = useState<EditorState>(() => ({
    base: configuration,
    draft: configurationDraft(configuration, types),
  }));
  // A configuration the server answered since, read again or written on
  // its own (on and off, an override), reaches the draft once, as
  // `arrive` says, while the page renders.
  const [seen, setSeen] = useState(configuration);
  if (seen !== configuration) {
    setSeen(configuration);
    setState(arrive(state, configuration, types));
  }
  const [checked, setChecked] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>();
  const [conflict, setConflict] = useState<Conflict>();
  const [salting, setSalting] = useState<RuleDraft>();

  const { draft } = state;
  const dirty = readOnly === undefined && isDraftDirty(state, types);
  const setDraft = (change: (draft: ConfigurationDraft) => ConfigurationDraft) =>
    setState((current) => ({ ...current, draft: change(current.draft) }));

  const context = {
    types,
    segments: new Set(declarations.segments.map((segment) => segment.key)),
    flags: otherFlags,
    self: flag.key,
    variations: flag.variations.map((variation) => variation.key),
    unitTypes: new Set(declarations.unitTypes.map((type) => type.key)),
    saved: new Set(
      state.base.rules.flatMap((rule) =>
        rule.conditions.map((condition) => fingerprint(condition)),
      ),
    ),
  };
  const issues = new Map([
    ...ruleIssues(i18n, draft.rules, context),
    ...prerequisiteIssues(i18n, draft.prerequisites, context),
  ]);
  const errors = new Map<string, string>([
    ...(refusal?.at ?? new Map<string, string>()),
    ...(checked ? issues : new Map<string, string>()),
  ]);

  const answered = useConfigurationAnswer(project, flag.key);

  /**
   * Reads the newer version a write met, and who saved it, for the banner;
   * a failure to read it is explained where a refusal of Save is.
   */
  const meetConflict = async (from: FlagConfiguration) => {
    let fresh: FlagConfiguration;
    try {
      fresh = await unwrap(
        getFlagConfiguration({
          path: { project, flag: flag.key, environment: configuration.environment },
        }),
      );
    } catch (error) {
      const message =
        error instanceof ApiError
          ? problemMessage(i18n, error)
          : i18n._(msg`The request was refused.`);
      setRefusal({ at: new Map(), rest: [message] });
      return;
    }
    const entries = await queryClient
      .fetchQuery({
        ...flagEntriesQuery(project, flag.id, { environment: environment.id, limit: 20 }),
        staleTime: 0,
      })
      .catch(() => [] as AuditEntry[]);
    setConflict({ from, fresh, entry: entryOfVersion(entries, fresh.version) });
    answered(fresh);
  };

  const save = useMutation({
    mutationFn: ({ sent }: { sent: EditorState }) =>
      unwrap(
        putFlagConfiguration({
          path: { project, flag: flag.key, environment: configuration.environment },
          body: configurationPutOf(sent, types),
          headers: ifMatch(sent.base.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: (saved) => {
      setState({ base: saved, draft: configurationDraft(saved, types) });
      setChecked(false);
      setRefusal(undefined);
      answered(saved);
      toast.success(t`The configuration in ${name} is saved.`);
    },
    onError: async (error, { sent }) => {
      if (!(error instanceof ApiError)) return;
      if (error.code === "version_mismatch") {
        await meetConflict(sent.base);
        return;
      }
      if (error.violations.length > 0) {
        const placed = placeRefusal(error.violations, sent.draft);
        setRefusal({
          at: new Map(
            [...placed.at].map(([key, violation]) => [
              key,
              configurationViolationMessage(i18n, violation),
            ]),
          ),
          rest: placed.rest.map((violation) => configurationViolationMessage(i18n, violation)),
        });
        return;
      }
      setRefusal({ at: new Map(), rest: [problemMessage(i18n, error)] });
    },
  });

  const submit = () => {
    setChecked(true);
    setRefusal(undefined);
    if (issues.size > 0) return;
    setConflict(undefined);
    save.mutate({ sent: state });
  };

  const regenerate = useMutation({
    mutationFn: (rule: RuleDraft) =>
      unwrap(
        regenerateRolloutSalt({
          path: {
            project,
            flag: flag.key,
            environment: configuration.environment,
            rule: rule.id!,
          },
          headers: ifMatch(state.base.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: (next, rule) => {
      const salt = next.rules.find((saved) => saved.id === rule.id)?.serve.salt;
      setState((current) => ({
        base: next,
        draft: {
          ...current.draft,
          rules: withRule(current.draft.rules, rule.key, (card) => ({
            ...card,
            serve: { ...card.serve, ...(salt ? { salt } : {}) },
          })),
        },
      }));
      setSalting(undefined);
      answered(next);
      toast.success(t`The rollout has a new salt.`);
    },
    onError: async (error) => {
      if (error instanceof ApiError && error.code === "version_mismatch") {
        setSalting(undefined);
        await meetConflict(state.base);
      }
    },
  });

  const variationSelect = (
    field: "offVariation" | "fallthrough",
    pointer: "off_variation" | "fallthrough",
  ) => (
    <Select
      value={draft[field]}
      disabled={readOnly !== undefined}
      onValueChange={(value) => setDraft((current) => ({ ...current, [field]: value }))}
      className="sm:w-64"
      aria-invalid={errors.has(pointer) ? true : undefined}
    >
      {flag.variations.map((variation) => (
        <SelectItem key={variation.key} value={variation.key}>
          {variation.key}
        </SelectItem>
      ))}
    </Select>
  );

  const salted = new Set(
    state.base.rules.flatMap((rule) =>
      rule.serve.kind === "rollout" && rule.serve.salt ? [rule.id] : [],
    ),
  );

  return (
    <div className="flex flex-col gap-8">
      {conflict ? (
        <ConflictBanner
          subject="configuration"
          entry={conflict.entry}
          diff={<ConfigurationDiff changes={diffConfigurations(conflict.from, conflict.fresh)} />}
          actions={
            <>
              <Button
                type="button"
                onClick={() => {
                  setState(reapply(state, conflict.fresh, types));
                  setConflict(undefined);
                }}
              >
                <Trans>Reload and reapply my draft</Trans>
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setState({
                    base: conflict.fresh,
                    draft: configurationDraft(conflict.fresh, types),
                  });
                  setConflict(undefined);
                  setRefusal(undefined);
                  setChecked(false);
                }}
              >
                <Trans>Discard my draft</Trans>
              </Button>
            </>
          }
        />
      ) : null}
      <Field
        label={<Trans>Off variation</Trans>}
        hint={<Trans>Served while the flag is off, and when a prerequisite fails.</Trans>}
        error={errors.get("off_variation")}
      >
        {variationSelect("offVariation", "off_variation")}
      </Field>
      {overrides}
      <PrerequisitesSection
        prerequisites={draft.prerequisites}
        onChange={(prerequisites) => setDraft((current) => ({ ...current, prerequisites }))}
        flags={otherFlags}
        errors={errors}
        disabled={readOnly !== undefined}
      />
      <RuleEditor
        rules={draft.rules}
        onChange={(rules) => setDraft((current) => ({ ...current, rules }))}
        variations={flag.variations}
        unitTypes={declarations.unitTypes}
        attributes={attributes}
        segments={declarations.segments}
        flags={otherFlags}
        errors={errors}
        saltedRules={salted}
        onRegenerateSalt={readOnly === undefined ? setSalting : undefined}
        disabled={readOnly !== undefined}
      />
      <Field
        label={<Trans>Fallthrough variation</Trans>}
        hint={<Trans>Served while the flag is on and no rule matches.</Trans>}
        error={errors.get("fallthrough")}
      >
        {variationSelect("fallthrough", "fallthrough")}
      </Field>
      {checked && issues.size > 0 ? (
        <p role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <Trans>Fix what is marked above before saving.</Trans>
        </p>
      ) : null}
      {refusal && refusal.rest.length > 0 ? (
        <div role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <ul className="flex flex-col gap-1">
            {refusal.rest.map((message, index) => (
              <li key={index}>{message}</li>
            ))}
          </ul>
        </div>
      ) : null}
      <UnsavedChangesBar
        dirty={dirty}
        onDiscard={() => {
          setState({ base: configuration, draft: configurationDraft(configuration, types) });
          setConflict(undefined);
          setRefusal(undefined);
          setChecked(false);
        }}
        onSave={submit}
        pending={save.isPending}
      />
      <ConfirmDialog
        open={salting !== undefined}
        onOpenChange={(open) => !open && setSalting(undefined)}
        title={t`Regenerate the salt?`}
        confirmLabel={<Trans>Regenerate salt</Trans>}
        destructive
        pending={regenerate.isPending}
        refusal={regenerate.error}
        onConfirm={() => salting && regenerate.mutate(salting)}
      >
        <p>
          <Trans>
            Every unit is bucketed afresh: units now in the rollout may fall out of it and others
            come in. The new salt is saved at once, apart from the rest of the draft.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}
