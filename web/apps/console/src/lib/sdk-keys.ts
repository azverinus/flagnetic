import type { SdkKey, SdkKeyKind, SdkKeyStatus } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/** How many allowed origins a client key lists at most. */
export const maxAllowedOrigins = 50;

/**
 * How long a rotated key keeps working beside its replacement, as the
 * member picks it. None ends the old key at once; the others give the
 * services holding it time to move to the new one.
 */
export const overlapChoices = ["none", "1h", "24h", "7d"] as const;
export type OverlapChoice = (typeof overlapChoices)[number];

/** The overlap a rotation offers first: the server's own default of seven days. */
export const defaultOverlap: OverlapChoice = "7d";

const overlapSeconds: Record<OverlapChoice, number> = {
  none: 0,
  "1h": 3_600,
  "24h": 86_400,
  "7d": 604_800,
};

/** The `overlap_seconds` a rotation sends for a choice. */
export function overlapSecondsOf(choice: OverlapChoice): number {
  return overlapSeconds[choice];
}

/** The preset's name in the member's language. */
export function overlapName(i18n: I18n, choice: OverlapChoice): string {
  switch (choice) {
    case "none":
      return i18n._(msg`None, end the old key now`);
    case "1h":
      return i18n._(msg`1 hour`);
    case "24h":
      return i18n._(msg`24 hours`);
    case "7d":
      return i18n._(msg`7 days`);
  }
}

/** The kind's name in the member's language. */
export function kindName(i18n: I18n, kind: SdkKeyKind): string {
  return kind === "server" ? i18n._(msg`Server`) : i18n._(msg`Client`);
}

/** The status's name in the member's language. */
export function statusName(i18n: I18n, status: SdkKeyStatus): string {
  switch (status) {
    case "active":
      return i18n._(msg`Active`);
    case "rotating":
      return i18n._(msg`Rotating`);
    case "expired":
      return i18n._(msg`Expired`);
    case "revoked":
      return i18n._(msg`Revoked`);
  }
}

/**
 * Whether a key still works for an SDK: an expired or revoked key is
 * refused whatever is done to it.
 */
export function isInUse(key: Pick<SdkKey, "status">): boolean {
  return key.status === "active" || key.status === "rotating";
}

/**
 * Whether the key can be rotated. Only an active key is: a key already
 * rotated, expired or revoked is never brought back, so offering it would
 * only meet the server's refusal.
 */
export function canRotate(key: Pick<SdkKey, "status">): boolean {
  return key.status === "active";
}

/** Whether the key can still be renamed, restricted or revoked to any effect. */
export function canChange(key: Pick<SdkKey, "status">): boolean {
  return isInUse(key);
}

/**
 * An origin as a browser sends it in an `Origin` header, which is how
 * the server compares: `scheme://host[:port]` in lower case, the scheme's
 * default port dropped. Nothing else is an origin: no other scheme, no
 * path beyond a bare slash, query, fragment or credentials. Mirrors the
 * server's normalization, so the list on screen is the one it keeps and
 * a spelling that would be refused is caught before sending.
 */
export function normalizeOrigin(text: string): string | undefined {
  // Checked on the text itself: the URL parser forgives an empty port, an
  // empty query and the like, which the server refuses.
  const authority = /^[a-z][a-z0-9+.-]*:\/\/([^/?#@\s]+)\/?$/i.exec(text.trim())?.[1];
  if (authority === undefined || authority.endsWith(":")) return undefined;
  let url: URL;
  try {
    url = new URL(text.trim());
  } catch {
    return undefined;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
  // The parser takes port 0, which no browser sends and the server refuses.
  if (url.port === "0") return undefined;
  // The parser lowercases the host, brackets an IPv6 one and drops the
  // scheme's default port, so what is left is the origin itself.
  return `${url.protocol}//${url.host}`;
}

/**
 * The origins a list holds after one is added or removed: each valid
 * spelling replaced by its normalized form, repeats after normalizing
 * dropped (the first keeps its place), and a spelling that is no origin
 * kept as typed so the form can name it and the member remove it.
 */
export function editedOrigins(origins: readonly string[]): string[] {
  const out: string[] = [];
  for (const origin of origins) {
    const next = normalizeOrigin(origin) ?? origin;
    if (!out.includes(next)) out.push(next);
  }
  return out;
}

/** The origins of a list that are no origin, in order. */
export function invalidOrigins(origins: readonly string[]): string[] {
  return origins.filter((origin) => normalizeOrigin(origin) === undefined);
}
