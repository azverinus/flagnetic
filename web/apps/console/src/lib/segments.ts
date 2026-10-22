import type { Segment, SegmentCreate, SegmentPatch, Violation } from "@flagnetic/api";
import type { I18n } from "@lingui/core";

import {
  type AttributeTypes,
  type ConditionDraft,
  conditionDraft,
  conditionIssue,
  conditionOf,
} from "./conditions";
import { configurationViolationMessage } from "./flag-detail";
import { descriptionSchema, nameSchema } from "./forms";
import { violationMessage } from "./problem-messages";
import { fingerprint } from "./structural-diff";

/**
 * A segment as its editor edits it: the fields a member changes, its key
 * aside, which never changes once the segment is created.
 */
export type SegmentDraft = {
  name: string;
  description: string;
  private: boolean;
  conditions: ConditionDraft[];
};

export function segmentDraft(segment: Segment, types: AttributeTypes): SegmentDraft {
  return {
    name: segment.name,
    description: segment.description,
    private: segment.private,
    conditions: segment.conditions.map((condition) => conditionDraft(condition, types)),
  };
}

/**
 * What a save of the draft sends: only the fields changed from the
 * segment it was filled from, so that keeping one's edits over a newer
 * version changes there only what the member changed. Conditions are a
 * list the server replaces whole, so they are sent whole when any of
 * them changed.
 */
export function segmentPatchOf(
  base: Segment,
  draft: SegmentDraft,
  types: AttributeTypes,
): SegmentPatch {
  const patch: SegmentPatch = {};
  const name = draft.name.trim();
  const description = draft.description.trim();
  if (name !== base.name) patch.name = name;
  if (description !== base.description) patch.description = description;
  if (draft.private !== base.private) patch.private = draft.private;
  const conditions = draft.conditions.map((condition) => conditionOf(condition, types));
  if (fingerprint(conditions) !== fingerprint(base.conditions)) patch.conditions = conditions;
  return patch;
}

/**
 * What the draft's conditions still need before the server would take
 * them, by the conditions' keys. A condition the segment already has is
 * not checked again: the server took it, and a version such as "2.0" it
 * accepted must not block another edit.
 */
export function segmentIssues(
  i18n: I18n,
  base: Segment | undefined,
  draft: SegmentDraft,
  types: AttributeTypes,
): Map<string, string> {
  const saved = new Set(base?.conditions.map(fingerprint));
  // A segment's conditions name attributes only: no segment, no flag.
  const context = { types, segments: new Set<string>(), flags: new Map(), self: "" };
  const issues = new Map<string, string>();
  for (const condition of draft.conditions) {
    if (saved.has(fingerprint(conditionOf(condition, types)))) continue;
    const issue = conditionIssue(i18n, condition, context);
    if (issue) issues.set(condition.key, issue);
  }
  return issues;
}

/** What the draft's name and description still need, by the field's name, as the server says it. */
export function segmentFieldIssues(i18n: I18n, draft: SegmentDraft): Map<string, string> {
  const issues = new Map<string, string>();
  const name = nameSchema(i18n).safeParse(draft.name);
  if (!name.success) issues.set("name", name.error.issues[0]!.message);
  const description = descriptionSchema(i18n).safeParse(draft.description);
  if (!description.success) issues.set("description", description.error.issues[0]!.message);
  return issues;
}

/** The fields of a segment a refusal can point at by name. */
const segmentField = /^\/(key|name|description|private)$/;

/**
 * A refusal's violations by where the editor shows them: a field by its
 * name, a condition by its key. Pointers count places in the draft as it
 * was sent; the rest is shown above the actions.
 */
export function placeSegmentRefusal(
  violations: readonly Violation[],
  sent: SegmentDraft,
): { at: Map<string, Violation>; rest: Violation[] } {
  const at = new Map<string, Violation>();
  const rest: Violation[] = [];
  for (const violation of violations) {
    const key =
      segmentField.exec(violation.pointer)?.[1] ??
      sent.conditions[Number(/^\/conditions\/(\d+)(\/|$)/.exec(violation.pointer)?.[1] ?? NaN)]
        ?.key;
    if (key === undefined) rest.push(violation);
    else if (!at.has(key)) at.set(key, violation);
  }
  return { at, rest };
}

/**
 * The sentence for a violation of a segment write: a segment's key reads
 * as every other entity's, its conditions as a rule's do.
 */
export function segmentViolationMessage(i18n: I18n, violation: Violation): string {
  return violation.pointer === "/key"
    ? violationMessage(i18n, violation)
    : configurationViolationMessage(i18n, violation);
}

/** The segment a create page sends. */
export function segmentCreateOf(
  key: string,
  draft: SegmentDraft,
  types: AttributeTypes,
): SegmentCreate {
  const description = draft.description.trim();
  return {
    key: key.trim(),
    name: draft.name.trim(),
    ...(description ? { description } : {}),
    private: draft.private,
    conditions: draft.conditions.map((condition) => conditionOf(condition, types)),
  };
}

/**
 * The segments list's filters as search parameters, so a filtered view
 * can be shared and survives a reload. `q` is matched in the key or the
 * name; `private` keeps the private segments only, or the others only.
 * The server lists every segment of a project at once, so the console
 * filters what it read.
 */
export type SegmentsSearch = { q?: string; private?: boolean };

/** Reads the segments list's search parameters, dropping what it cannot read. */
export function validateSegmentsSearch(search: Record<string, unknown>): SegmentsSearch {
  const out: SegmentsSearch = {};
  if (typeof search.q === "string" && search.q.trim() !== "") out.q = search.q.trim();
  if (search.private === true || search.private === "true") out.private = true;
  if (search.private === false || search.private === "false") out.private = false;
  return out;
}

/** The segments the filters keep, in the order they were listed. */
export function filterSegments<T extends Segment>(
  segments: readonly T[],
  search: SegmentsSearch,
): T[] {
  const text = search.q?.toLowerCase();
  return segments.filter(
    (segment) =>
      (text === undefined ||
        segment.key.toLowerCase().includes(text) ||
        segment.name.toLowerCase().includes(text)) &&
      (search.private === undefined || segment.private === search.private),
  );
}
