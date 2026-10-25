import {
  type FlagType,
  type ListFlagsData,
  type Violation,
  zFlagCreate,
  zFlagType,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { z } from "zod";

import { ApiError } from "./errors";
import { violationMessage } from "./problem-messages";

/** What a flag's configuration in the list's environment is filtered by. */
export type FlagStateFilter = "on" | "off" | "rules" | "rollout" | "experiment";

export const flagStateFilters: readonly FlagStateFilter[] = [
  "on",
  "off",
  "rules",
  "rollout",
  "experiment",
];

/** How the flags list is ordered: by key, by name, or the latest change to a definition first. */
export type FlagsSort = "key" | "name" | "updated";

export const flagsSorts: readonly FlagsSort[] = ["key", "name", "updated"];

/**
 * The flags list's filters as search parameters, so a filtered view and
 * its page can be shared and survive a reload. `environment` is the
 * environment `state` is judged in, by key; `tag` is one tag matched
 * exactly; `owner` a member's identifier; `archived` lists the archived
 * flags instead of the others; `differs_between` two environment keys,
 * comma separated, listing the flags whose configurations differ between
 * them; `cursor` is the server's keyset cursor of the page shown.
 */
export type FlagsSearch = {
  environment?: string;
  q?: string;
  type?: FlagType;
  tag?: string;
  owner?: string;
  state?: FlagStateFilter;
  archived?: true;
  differs_between?: string;
  sort?: FlagsSort;
  cursor?: string;
};

/** Every type a flag can have, in the order a member chooses among them. */
export const flagTypes: readonly FlagType[] = zFlagType.options;

function text(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;
}

/** An environment's key, as the server writes every key but a flag's. */
const environmentKey = /^[a-z][a-z0-9_-]{0,63}$/;

function oneOf<T extends string>(options: readonly T[], value: unknown): T | undefined {
  return options.find((option) => option === value);
}

/** Two different environment keys, comma separated, as the address holds a comparison. */
function environmentPair(value: unknown): [string, string] | undefined {
  const parts = text(value)?.split(",");
  if (parts?.length !== 2) return undefined;
  const [a, b] = parts as [string, string];
  return environmentKey.test(a) && environmentKey.test(b) && a !== b ? [a, b] : undefined;
}

/**
 * Reads the flags list's search parameters, dropping anything the server
 * would refuse, so a mistyped address opens the list rather than an error.
 */
export function validateFlagsSearch(search: Record<string, unknown>): FlagsSearch {
  const out: FlagsSearch = {};
  for (const name of ["environment", "q", "tag", "owner", "cursor"] as const) {
    const value = text(search[name]);
    if (value !== undefined) out[name] = value;
  }
  const type = zFlagType.safeParse(search.type);
  if (type.success) out.type = type.data;
  const state = oneOf(flagStateFilters, search.state);
  if (state) out.state = state;
  if (search.archived === true || search.archived === "true") out.archived = true;
  const pair = environmentPair(search.differs_between);
  if (pair) out.differs_between = pair.join(",");
  const sort = oneOf(flagsSorts, search.sort);
  if (sort) out.sort = sort;
  return out;
}

/**
 * Whether any filter is set. The environment alone filters nothing, and
 * the order and the page choose how the flags are shown, so none of them
 * counts.
 */
export function isFlagsFiltered(search: FlagsSearch): boolean {
  return Object.entries(search).some(
    ([name, value]) =>
      value !== undefined &&
      !["cursor", "environment", "sort"].includes(name) &&
      (name !== "state" || search.environment !== undefined),
  );
}

/**
 * The list request of a search: the state only with the environment it is
 * judged in, which the server requires, and the comparison as the pair of
 * keys it takes.
 */
export function listFlagsQueryOf(search: FlagsSearch): NonNullable<ListFlagsData["query"]> {
  const { state, differs_between, ...rest } = search;
  const pair = environmentPair(differs_between);
  return {
    ...rest,
    ...(state && search.environment ? { state } : {}),
    ...(pair ? { differs_between: pair } : {}),
  };
}

/** The two environments a comparison in the address names. */
export function comparedEnvironments(search: FlagsSearch): [string, string] | undefined {
  return environmentPair(search.differs_between);
}

/**
 * The environment the flags list shows, among the project's environment
 * keys in their order: the one the address names, else the one this
 * browser showed last for the project, else the first.
 */
export function resolveEnvironment(
  keys: readonly string[],
  asked: string | undefined,
  remembered: string | null,
): string | undefined {
  if (asked !== undefined && keys.includes(asked)) return asked;
  if (remembered !== null && keys.includes(remembered)) return remembered;
  return keys[0];
}

const variationField = /^\/variations\/\d+\/(key|value)$/;

/**
 * The sentence for a violation of a flag write. A flag's key and its
 * variations' keys follow rules of their own, wider than the keys of
 * other entities, and a variation's value is of the flag's type, so those
 * violations read differently here; every other reads as anywhere else.
 */
export function flagViolationMessage(i18n: I18n, violation: Violation): string {
  const field = variationField.exec(violation.pointer)?.[1];
  if (violation.code === "invalid_key" && violation.pointer === "/key") {
    return i18n._(msg`Start with a letter; then letters, digits, ., - and _, at most 128 in all.`);
  }
  if (violation.code === "invalid_key" && field === "key") {
    return i18n._(msg`Letters, digits, ., - and _, at most 64.`);
  }
  if (violation.code === "duplicate" && field === "key") {
    return i18n._(msg`Another variation has this key.`);
  }
  if (violation.code === "invalid_type" && field === "value") {
    return i18n._(msg`Enter a value of the flag's type.`);
  }
  return violationMessage(i18n, violation);
}

/** What the refusal of a flag write says of the field at pointer, if anything. */
export function flagRefusalAt(i18n: I18n, refusal: unknown, pointer: string): string | undefined {
  if (!(refusal instanceof ApiError)) return undefined;
  const violation = refusal.violations.find((candidate) => candidate.pointer === pointer);
  return violation && flagViolationMessage(i18n, violation);
}

/** The server's rules for a flag's tags: how many, and how long each. */
const tagRules = zFlagCreate.shape.tags.unwrap();

/** A flag's tags, checked by the server's rules before it is asked. */
export function tagsSchema(i18n: I18n) {
  return z
    .array(z.string())
    .refine((tags) => tags.every((tag) => tagRules.element.safeParse(tag).success), {
      message: i18n._(msg`A tag has at most 64 characters.`),
    })
    .refine((tags) => tagRules.safeParse(tags).success, {
      message: i18n._(msg`A flag has at most 50 tags.`),
    });
}

/** The first instant of a day (YYYY-MM-DD) in the member's time zone. */
export function startOfDay(day: string): string {
  const [year, month, date] = day.split("-").map(Number) as [number, number, number];
  return new Date(year, month - 1, date).toISOString();
}

/** The day (YYYY-MM-DD) an instant falls on in the member's time zone. */
export function dayOf(instant: string): string {
  const date = new Date(instant);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

/** A flag type as a member reads it. */
export function flagTypeName(i18n: I18n, type: FlagType): string {
  switch (type) {
    case "boolean":
      return i18n._(msg`Boolean`);
    case "string":
      return i18n._(msg`String`);
    case "number":
      return i18n._(msg`Number`);
    case "json":
      return i18n._(msg`JSON`);
  }
}
