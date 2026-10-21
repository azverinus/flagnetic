import { type ApiToken, type Me, type Scope, zScope } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/** Every scope, in the order the server lists them. */
const allScopes: readonly Scope[] = zScope.options;

/**
 * Whether a token carrying scope also reads: every scope but `read` lets it
 * change something, and nothing can be changed unseen.
 */
export function impliesRead(scope: Scope): boolean {
  return scope !== "read";
}

/**
 * The scopes a member may give their token, in the order the server lists
 * them: `instance:admin` only for an instance admin, since nobody else
 * holds it.
 */
export function offeredScopes({ instanceAdmin }: { instanceAdmin: boolean }): Scope[] {
  return allScopes.filter((scope) => instanceAdmin || scope !== "instance:admin");
}

/**
 * The scopes a project token may be given, in the order the server lists
 * them: never `instance:admin`, since a project token reaches its project
 * only.
 */
export const projectTokenScopes: readonly Scope[] = allScopes.filter(
  (scope) => scope !== "instance:admin",
);

/** The scopes chosen, with the `read` they imply, in the server's order. */
export function withImpliedRead(chosen: readonly Scope[]): Scope[] {
  const all = new Set<Scope>(chosen);
  if (chosen.some(impliesRead)) all.add("read");
  return allScopes.filter((scope) => all.has(scope));
}

/** What each scope lets a token do, in the member's language. */
export function scopeDescription(i18n: I18n, scope: Scope): string {
  switch (scope) {
    case "read":
      return i18n._(msg`Read everything you can read.`);
    case "flags:write":
      return i18n._(msg`Change flags, segments and declarations.`);
    case "experiments:write":
      return i18n._(msg`Change experiments.`);
    case "project:admin":
      return i18n._(msg`Administer projects: environments, members and tokens.`);
    case "instance:admin":
      return i18n._(msg`Administer the instance: projects, members and its settings.`);
  }
}

/** How far ahead a new token may expire. */
export type TokenPolicy = {
  /** The furthest ahead, in seconds; undefined when the instance sets no bound. */
  maxLifetimeSeconds?: number;
  /** Whether every token must expire. */
  expiryRequired: boolean;
};

/** The instance's policy for a new token, which `GET /me` answers every member. */
export function tokenPolicy(me: Me): TokenPolicy {
  return {
    maxLifetimeSeconds: me.max_token_lifetime_seconds ?? undefined,
    expiryRequired: me.require_token_expiry,
  };
}

/** The expiries offered when creating a token: a number of days ahead, or never. */
export type ExpiryChoice = "7" | "30" | "90" | "365" | "never";

const day = 24 * 60 * 60;
const presets = ["7", "30", "90", "365"] as const;

/** The presets the policy allows, the shortest first, then never unless every token must expire. */
export function expiryChoices(policy: TokenPolicy): ExpiryChoice[] {
  const max = policy.maxLifetimeSeconds;
  const within = presets.filter(
    (days, i) => max === undefined || i === 0 || Number(days) * day <= max,
  );
  return policy.expiryRequired ? [...within] : [...within, "never"];
}

/** The expiry a new token is offered first: thirty days, or the nearest the policy allows. */
export function defaultExpiry(choices: readonly ExpiryChoice[]): ExpiryChoice {
  return choices.includes("30") ? "30" : (choices[0] ?? "never");
}

/**
 * The instant a token created now with choice expires, or null for never.
 * A choice at the policy's bound is brought an hour inside it (a tenth of
 * it for a bound under ten hours), so a browser clock running ahead of
 * the server's does not get the token refused.
 */
export function expiresAt(choice: ExpiryChoice, now: Date, policy: TokenPolicy): string | null {
  if (choice === "never") return null;
  let seconds = Number(choice) * day;
  const max = policy.maxLifetimeSeconds;
  if (max !== undefined && seconds >= max) seconds = max - Math.min(60 * 60, max / 10);
  return new Date(now.getTime() + seconds * 1000).toISOString();
}

/** Where a token is in its life. */
export type TokenState = "active" | "expired" | "revoked";

export function tokenState(token: ApiToken, now: Date): TokenState {
  if (token.revoked_at) return "revoked";
  if (token.expires_at && new Date(token.expires_at) <= now) return "expired";
  return "active";
}
