import {
  type Experiment,
  type ExperimentStatus,
  type ExperimentStop,
  type Flag,
  zExperimentClone,
  zExperimentStop,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { ApiError } from "./errors";
import { parsePercent, percentText, weightDrafts, weightIssues } from "./experiments";
import { nameSchema } from "./forms";
import { problemMessage, violationMessage } from "./problem-messages";

/** A rule serving an experiment: the environment, its place there (from 1), what it says. */
export type HoldingRule = {
  environment: string;
  position: number;
  id: string;
  description: string;
};

/**
 * Every rule serving an experiment, in the flag's order of environments
 * and rules: what a stop rewrites to the winner or deletes.
 */
export function holdingRules(flag: Flag, experiment: string): HoldingRule[] {
  return flag.environments.flatMap((configuration) =>
    configuration.rules.flatMap((rule, index) =>
      rule.serve.kind === "experiment" && rule.serve.experiment === experiment
        ? [
            {
              environment: configuration.environment,
              position: index + 1,
              id: rule.id,
              description: rule.description,
            },
          ]
        : [],
    ),
  );
}

/**
 * What keeps a draft from starting, each said once and all at once, in
 * the order the server lists them: the flag archived, the unit type no
 * longer declared, the weights invalid against the flag's variations,
 * no hypothesis, no expected end date. The server judges again; this
 * lets the member fix everything before asking.
 */
export function startBlockers(
  i18n: I18n,
  experiment: Experiment,
  context: { flag: Flag; unitTypes: readonly { key: string }[] },
): string[] {
  const { flag, unitTypes } = context;
  const blockers: string[] = [];
  if (flag.archived_at !== null) {
    const key = flag.key;
    blockers.push(i18n._(msg`The flag ${key} is archived.`));
  }
  if (!unitTypes.some((unitType) => unitType.key === experiment.unit_type)) {
    const unitType = experiment.unit_type;
    blockers.push(i18n._(msg`The unit type ${unitType} is not declared.`));
  }
  const weights = weightIssues(
    i18n,
    weightDrafts(i18n, experiment.variations),
    flag.variations.map(({ key }) => key),
  );
  for (const message of new Set(weights.rows.map((issue) => issue.message))) {
    blockers.push(message);
  }
  if (weights.total !== undefined) blockers.push(weights.total);
  if (experiment.hypothesis.trim() === "") blockers.push(i18n._(msg`It has no hypothesis.`));
  if (experiment.expected_end_at === null) {
    blockers.push(i18n._(msg`It has no expected end date.`));
  }
  return blockers;
}

/**
 * What is wrong with a new traffic allocation for a running experiment:
 * it must read as a percentage and exceed the current one, since an
 * included unit never leaves and the same share changes nothing.
 */
export function growIssue(i18n: I18n, text: string, current: number): string | undefined {
  const points = parsePercent(text);
  if (points === undefined) {
    return i18n._(msg`Enter a percentage from 0 to 100, at most two decimals.`);
  }
  if (points <= current) {
    const from = percentText(i18n, current);
    return i18n._(msg`Enter more than ${from} %: the allocation only grows.`);
  }
  return undefined;
}

/** A stop's decision as the dialog holds it. */
export type StopDraft = { outcome: ExperimentStop["outcome"]; winner: string; note: string };

const noteRule = zExperimentStop.shape.note.unwrap();

/** The longest note a decision keeps. */
export const maxNoteLength = noteRule.maxLength ?? 0;

/** What a note is refused for, if anything. */
export function noteIssue(i18n: I18n, note: string): string | undefined {
  return noteRule.safeParse(note.trim()).success
    ? undefined
    : i18n._(msg`At most ${maxNoteLength} characters.`);
}

/** What keeps a stop from being sent: a winner chosen when one ships, a note within bounds. */
export function stopIssues(i18n: I18n, draft: StopDraft): { winner?: string; note?: string } {
  const issues: { winner?: string; note?: string } = {};
  if (draft.outcome === "winner" && draft.winner === "") {
    issues.winner = i18n._(msg`Choose the variation that won.`);
  }
  const note = noteIssue(i18n, draft.note);
  if (note !== undefined) issues.note = note;
  return issues;
}

/** The stop request of a decision: the winner only when one ships, the note only when written. */
export function stopBodyOf(draft: StopDraft): ExperimentStop {
  const body: ExperimentStop = { outcome: draft.outcome };
  if (draft.outcome === "winner") body.winner_variation = draft.winner;
  const note = draft.note.trim();
  if (note !== "") body.note = note;
  return body;
}

/** A clone as the dialog holds it; an empty name keeps the source's. */
export type CloneDraft = { key: string; name: string };

/**
 * What keeps a clone from being sent: a key the API takes and that is
 * not the source's own, and a name it takes when one is written.
 */
export function cloneIssues(
  i18n: I18n,
  draft: CloneDraft,
  source: string,
): { key?: string; name?: string } {
  const issues: { key?: string; name?: string } = {};
  const key = draft.key.trim();
  if (!zExperimentClone.shape.key.safeParse(key).success) {
    issues.key = i18n._(
      msg`Start with a letter; then letters, digits, ., - and _, at most 128 in all.`,
    );
  } else if (key === source) {
    issues.key = i18n._(msg`Choose a key no other experiment has.`);
  }
  if (draft.name.trim() !== "") {
    const name = nameSchema(i18n).safeParse(draft.name);
    if (!name.success) issues.name = name.error.issues[0]?.message ?? "";
  }
  return issues;
}

/** A transition or write the experiment's header offers. */
export type ExperimentAction = "start" | "grow" | "stop" | "note" | "archive" | "clone" | "delete";

/**
 * What an experiment's status lets a member do with it, in the order the
 * header shows them: a draft starts, is archived or deleted; a running one
 * grows its allocation or stops; a stopped one has its decision note
 * edited or is archived; any is cloned into a new draft.
 */
export function experimentActions(status: ExperimentStatus): ReadonlySet<ExperimentAction> {
  switch (status) {
    case "draft":
      return new Set(["start", "archive", "clone", "delete"]);
    case "running":
      return new Set(["grow", "stop", "clone"]);
    case "stopped":
      return new Set(["note", "archive", "clone"]);
    case "archived":
      return new Set(["clone"]);
  }
}

/** The field a start's violation concerns, as the form labels it. */
function startFieldName(i18n: I18n, pointer: string): string {
  if (pointer === "/hypothesis") return i18n._(msg`Hypothesis`);
  if (pointer === "/expected_end_at") return i18n._(msg`Expected end`);
  if (pointer === "/flag") return i18n._(msg`Flag`);
  if (pointer === "/unit_type") return i18n._(msg`Unit type`);
  return i18n._(msg`Weights`);
}

/**
 * A refusal of an experiment's write in the member's language: an
 * archived refusal names the experiment, since the shared sentence speaks
 * of a flag, and a start refused as not ready names each missing field.
 */
export function experimentRefusalMessage(i18n: I18n, error: ApiError): string {
  if (error.code === "archived") {
    return i18n._(msg`The experiment is archived: nothing about it changes any more.`);
  }
  const message = problemMessage(i18n, error);
  if (error.code !== "experiment_not_ready") return message;
  return [
    message,
    ...error.violations.map(
      (violation) =>
        `${startFieldName(i18n, violation.pointer)}: ${violationMessage(i18n, violation)}`,
    ),
  ].join(" ");
}
