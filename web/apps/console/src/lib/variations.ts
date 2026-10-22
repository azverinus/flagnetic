import {
  type FlagType,
  type VariationInput,
  zDescription,
  zName,
  zVariationKey,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { violationMessage } from "./problem-messages";

/**
 * One variation as a form edits it: every field as typed, the value as
 * text until it is sent, and an identifier of the row's own so a row
 * keeps its place on the screen while the rows above it are removed.
 */
export type VariationDraft = {
  id: string;
  key: string;
  value: string;
  name: string;
  description: string;
};

/** A field of a variation row that can be wrong. */
export type VariationField = "key" | "value" | "name" | "description";

/** Something wrong with one row, at its index and field. */
export type VariationIssue = { path: [number, VariationField]; message: string };

let rows = 0;

function draft(key: string): VariationDraft {
  rows += 1;
  return { id: `variation-${rows}`, key, value: "", name: "", description: "" };
}

/**
 * The rows a flag of any type but boolean starts with: two, since a flag
 * serving one value only is rarely what is meant.
 */
export function startingVariations(): VariationDraft[] {
  return [draft("v1"), draft("v2")];
}

/** A row to add below the others, keyed `vN` with the lowest N no row has. */
export function newVariation(current: readonly VariationDraft[]): VariationDraft {
  const taken = new Set(current.map((row) => row.key));
  let n = 1;
  while (taken.has(`v${n}`)) n += 1;
  return draft(`v${n}`);
}

type Parsed = { ok: true; value: unknown } | { ok: false };

/**
 * A row's value as the flag's type reads it: a string as typed, a finite
 * number, or any JSON but null, which no variation may serve.
 */
function parseValue(type: FlagType, raw: string): Parsed {
  switch (type) {
    case "string":
      return { ok: true, value: raw };
    case "number": {
      const value = raw.trim() === "" ? NaN : Number(raw);
      return Number.isFinite(value) ? { ok: true, value } : { ok: false };
    }
    case "json":
      try {
        const value: unknown = JSON.parse(raw);
        return value === null ? { ok: false } : { ok: true, value };
      } catch {
        return { ok: false };
      }
    case "boolean":
      return { ok: false };
  }
}

/**
 * The variations a flag is created or edited with, or none for a boolean
 * flag, whose variations are always `true` and `false`. A blank name or
 * description is left out, so the server names the variation by its key.
 * The rows must have no issue.
 */
export function variationInputs(
  type: FlagType,
  rows: readonly VariationDraft[],
): VariationInput[] | undefined {
  if (type === "boolean") return undefined;
  return rows.map((row) => {
    const parsed = parseValue(type, row.value);
    const name = row.name.trim();
    const description = row.description.trim();
    return {
      key: row.key.trim(),
      value: parsed.ok ? parsed.value : row.value,
      ...(name ? { name } : {}),
      ...(description ? { description } : {}),
    };
  });
}

/**
 * What the server would refuse in the rows, checked by the generated
 * schemas' bounds before asking it: keys by the variation key rule and
 * unique (the later of two rows is the one to fix), values of the flag's
 * type, names and descriptions within their lengths.
 */
export function variationIssues(
  i18n: I18n,
  type: FlagType,
  rows: readonly VariationDraft[],
): VariationIssue[] {
  if (type === "boolean") return [];
  const issues: VariationIssue[] = [];
  const seen = new Set<string>();
  rows.forEach((row, index) => {
    const key = row.key.trim();
    if (!zVariationKey.safeParse(key).success) {
      issues.push({
        path: [index, "key"],
        message: i18n._(msg`Letters, digits, ., - and _, at most 64.`),
      });
    } else if (seen.has(key)) {
      issues.push({ path: [index, "key"], message: i18n._(msg`Another variation has this key.`) });
    }
    seen.add(key);
    if (!parseValue(type, row.value).ok) {
      issues.push({
        path: [index, "value"],
        message:
          type === "number"
            ? i18n._(msg`Enter a number.`)
            : i18n._(msg`Enter JSON other than null.`),
      });
    }
    const bounds = [
      ["name", zName.maxLength],
      ["description", zDescription.maxLength],
    ] as const;
    for (const [field, max] of bounds) {
      if (max !== null && row[field].trim().length > max) {
        issues.push({
          path: [index, field],
          message: violationMessage(i18n, {
            pointer: `/variations/${index}/${field}`,
            code: "too_long",
            detail: "",
            params: { max },
          }),
        });
      }
    }
  });
  return issues;
}
