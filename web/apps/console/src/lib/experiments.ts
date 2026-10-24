import {
  type Experiment,
  type ExperimentPatch,
  type ExperimentStatus,
  type ExperimentWeight,
  type Flag,
  type ListExperimentsData,
  zExperimentStatus,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { dayOf, startOfDay } from "./flags";

/** The basis points of a whole: weights and traffic allocations are written in them. */
export const wholeBasisPoints = 10_000;

/** How many variations an experiment compares, at least and at most. */
export const minVariations = 2;
export const maxVariations = 50;

/** A percentage with at most two decimals, the point or a comma before them. */
const percentPattern = /^\d{1,3}(?:[.,]\d{1,2})?$/;

/**
 * A percentage as the member typed it, in basis points: 0 to 100 with at
 * most two decimals, since a basis point is the finest weight there is.
 * Anything else reads as nothing, for the form to point at.
 */
export function parsePercent(text: string): number | undefined {
  const trimmed = text.trim();
  if (!percentPattern.test(trimmed)) return undefined;
  const [whole, fraction = ""] = trimmed.split(/[.,]/) as [string, string?];
  const points = Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
  return points <= wholeBasisPoints ? points : undefined;
}

/** Basis points as a percentage with two decimals, in the member's language, without the sign. */
export function percentText(i18n: I18n, points: number): string {
  return new Intl.NumberFormat(i18n.locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
    useGrouping: false,
  }).format(points / 100);
}

/** Weights for so many variations, as even as basis points allow, the remainder on the first. */
export function evenWeights(count: number): number[] {
  const share = Math.floor(wholeBasisPoints / count);
  const remainder = wholeBasisPoints - share * count;
  return Array.from({ length: count }, (_, index) => share + (index < remainder ? 1 : 0));
}

/** One row of the weights input: a variation of the flag and its share, as typed. */
export type WeightDraft = { variation: string; weight: string };

export type WeightIssue = { index: number; field: "variation" | "weight"; message: string };

/** The sum of the weights, or nothing while any of them cannot be read. */
export function weightTotal(rows: readonly WeightDraft[]): number | undefined {
  let total = 0;
  for (const row of rows) {
    const points = parsePercent(row.weight);
    if (points === undefined) return undefined;
    total += points;
  }
  return total;
}

/**
 * What keeps a set of weights from being saved, by row and as a whole:
 * the same rules the server and the start judge them by, so a form never
 * sends weights that cannot start.
 */
export function weightIssues(
  i18n: I18n,
  rows: readonly WeightDraft[],
  variations: readonly string[],
): { rows: WeightIssue[]; total: string | undefined } {
  const issues: WeightIssue[] = [];
  const seen = new Set<string>();
  rows.forEach((row, index) => {
    if (row.variation === "") {
      issues.push({ index, field: "variation", message: i18n._(msg`Choose a variation.`) });
    } else if (seen.has(row.variation)) {
      issues.push({
        index,
        field: "variation",
        message: i18n._(msg`Another row has this variation.`),
      });
    } else if (!variations.includes(row.variation)) {
      issues.push({
        index,
        field: "variation",
        message: i18n._(msg`The flag has no such variation.`),
      });
    }
    seen.add(row.variation);
    if (parsePercent(row.weight) === undefined) {
      issues.push({
        index,
        field: "weight",
        message: i18n._(msg`Enter a percentage from 0 to 100, at most two decimals.`),
      });
    }
  });
  return { rows: issues, total: totalIssue(i18n, rows) };
}

function totalIssue(i18n: I18n, rows: readonly WeightDraft[]): string | undefined {
  if (rows.length < minVariations || rows.length > maxVariations) {
    return i18n._(msg`Compare ${minVariations} to ${maxVariations} variations.`);
  }
  const total = weightTotal(rows);
  if (total === undefined) return i18n._(msg`Enter every weight to see the total.`);
  if (total === wholeBasisPoints) return undefined;
  const sum = percentText(i18n, total);
  const whole = percentText(i18n, wholeBasisPoints);
  return i18n._(msg`The weights sum to ${sum} %; they must sum to ${whole} %.`);
}

/** The rows of the weights input for an experiment's weights. */
export function weightDrafts(i18n: I18n, weights: readonly ExperimentWeight[]): WeightDraft[] {
  return weights.map(({ variation, weight }) => ({ variation, weight: percentText(i18n, weight) }));
}

/** The weights the rows hold, once every row reads. */
export function weightsOf(rows: readonly WeightDraft[]): ExperimentWeight[] {
  return rows.map((row) => ({ variation: row.variation, weight: parsePercent(row.weight) ?? 0 }));
}

/** A field of the experiment form, as the API names it. */
export type ExperimentField =
  | "name"
  | "hypothesis"
  | "owner"
  | "tags"
  | "flag"
  | "unit_type"
  | "variations"
  | "traffic_allocation"
  | "expected_end_at";

const everyField: readonly ExperimentField[] = [
  "name",
  "hypothesis",
  "owner",
  "tags",
  "flag",
  "unit_type",
  "variations",
  "traffic_allocation",
  "expected_end_at",
];

/**
 * The fields an experiment's status no longer lets change: a draft
 * changes freely; a running experiment keeps what its comparison rests on,
 * its allocation only growing; a stopped one keeps all but its
 * description; an archived one keeps everything.
 */
export function lockedFields(status: ExperimentStatus): ReadonlySet<ExperimentField> {
  switch (status) {
    case "draft":
      return new Set();
    case "running":
      return new Set(["flag", "unit_type", "variations"]);
    case "stopped":
      return new Set(["flag", "unit_type", "variations", "traffic_allocation"]);
    case "archived":
      return new Set(everyField);
  }
}

/** Why the fields the status locks are locked, said once above them. */
export function lockReason(i18n: I18n, status: ExperimentStatus): string | undefined {
  switch (status) {
    case "draft":
      return undefined;
    case "running":
      return i18n._(
        msg`The experiment is running: its flag, unit type and weights stay as they started, so the comparison stays valid, and its traffic allocation can only grow, so no included unit ever leaves.`,
      );
    case "stopped":
      return i18n._(
        msg`The experiment is stopped: only its name, hypothesis, owner, tags and expected end date still change.`,
      );
    case "archived":
      return i18n._(msg`The experiment is archived: nothing about it changes any more.`);
  }
}

/** An experiment's status as a member reads it. */
export function experimentStatusName(i18n: I18n, status: ExperimentStatus): string {
  switch (status) {
    case "draft":
      return i18n._(msg`Draft`);
    case "running":
      return i18n._(msg`Running`);
    case "stopped":
      return i18n._(msg`Stopped`);
    case "archived":
      return i18n._(msg`Archived`);
  }
}

export const experimentStatuses: readonly ExperimentStatus[] = zExperimentStatus.options;

/**
 * The experiments list's filters as search parameters, as the API takes
 * them, so a filtered view and its page can be shared and survive a
 * reload: `flag` a flag's key, `owner` a member's identifier, `tag` one
 * tag matched exactly, `archived` lists archived experiments beside the
 * others, `cursor` the server's keyset cursor of the page shown.
 */
export type ExperimentsSearch = {
  status?: ExperimentStatus;
  flag?: string;
  owner?: string;
  tag?: string;
  archived?: true;
  cursor?: string;
};

function text(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;
}

/**
 * Reads the experiments list's search parameters, dropping anything the
 * server would refuse, so a mistyped address opens the list rather than
 * an error.
 */
export function validateExperimentsSearch(search: Record<string, unknown>): ExperimentsSearch {
  const out: ExperimentsSearch = {};
  for (const name of ["flag", "owner", "tag", "cursor"] as const) {
    const value = text(search[name]);
    if (value !== undefined) out[name] = value;
  }
  const status = zExperimentStatus.safeParse(search.status);
  if (status.success) out.status = status.data;
  if (search.archived === true || search.archived === "true") out.archived = true;
  return out;
}

/** Whether any filter is set; the page shown is not one. */
export function isExperimentsFiltered(search: ExperimentsSearch): boolean {
  return Object.entries(search).some(([name, value]) => value !== undefined && name !== "cursor");
}

/** The list request of a search. */
export function listExperimentsQueryOf(
  search: ExperimentsSearch,
): NonNullable<ListExperimentsData["query"]> {
  const { archived, ...rest } = search;
  return { ...rest, ...(archived ? { include_archived: true } : {}) };
}

/**
 * The environments whose rules serve an experiment, in the flag's order:
 * those a start puts it into and a stop rewrites.
 */
export function holdingEnvironments(flag: Flag, experiment: string): string[] {
  return flag.environments
    .filter((configuration) =>
      configuration.rules.some(
        (rule) => rule.serve.kind === "experiment" && rule.serve.experiment === experiment,
      ),
    )
    .map((configuration) => configuration.environment);
}

/** What the experiment form holds, as typed. */
export type ExperimentValues = {
  key: string;
  name: string;
  hypothesis: string;
  owner: string;
  tags: string[];
  flag: string;
  unit_type: string;
  variations: WeightDraft[];
  /** A percentage, as the weights are. */
  traffic_allocation: string;
  /** A day, YYYY-MM-DD, in the member's time zone. */
  expected_end_at: string;
};

/** The form's values for an experiment as saved. */
export function experimentValues(i18n: I18n, experiment: Experiment): ExperimentValues {
  return {
    key: experiment.key,
    name: experiment.name,
    hypothesis: experiment.hypothesis,
    owner: experiment.owner,
    tags: experiment.tags,
    flag: experiment.flag,
    unit_type: experiment.unit_type,
    variations: weightDrafts(i18n, experiment.variations),
    traffic_allocation: percentText(i18n, experiment.traffic_allocation),
    expected_end_at: experiment.expected_end_at ? dayOf(experiment.expected_end_at) : "",
  };
}

function sameWeights(a: readonly ExperimentWeight[], b: readonly ExperimentWeight[]): boolean {
  return (
    a.length === b.length &&
    a.every((weight, index) => {
      const other = b[index];
      return other?.variation === weight.variation && other.weight === weight.weight;
    })
  );
}

/**
 * The fields of the form that differ from the experiment it was filled
 * from, and only those: saving them onto a version someone else wrote
 * meanwhile keeps what they changed in every other field. An expected end
 * date left empty is never sent, since the API keeps a date once set.
 */
export function experimentPatchOf(base: Experiment, values: ExperimentValues): ExperimentPatch {
  const patch: ExperimentPatch = {};
  for (const field of ["name", "hypothesis", "owner", "flag", "unit_type"] as const) {
    const value = values[field].trim();
    if (value !== base[field]) patch[field] = value;
  }
  if (values.tags.join("\n") !== base.tags.join("\n")) patch.tags = values.tags;
  const weights = weightsOf(values.variations);
  if (!sameWeights(weights, base.variations)) patch.variations = weights;
  const allocation = parsePercent(values.traffic_allocation);
  if (allocation !== undefined && allocation !== base.traffic_allocation) {
    patch.traffic_allocation = allocation;
  }
  const day = values.expected_end_at;
  if (day !== "" && (base.expected_end_at === null || dayOf(base.expected_end_at) !== day)) {
    patch.expected_end_at = startOfDay(day);
  }
  return patch;
}
