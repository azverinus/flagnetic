import { readStored } from "@/lib/storage";

/** The languages a member can read the console in. */
export const locales = ["en", "ru"] as const;

export type Locale = (typeof locales)[number];

/** Where the console remembers the language for the screens before sign-in. */
export const localeStorageKey = "flagnetic.locale";

/** The language the console is written in, used when nothing else applies. */
export const sourceLocale: Locale = "en";

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && (locales as readonly string[]).includes(value);
}

/**
 * The offered language a BCP 47 tag asks for: the tag itself, or its primary
 * language (`ru-RU` reads Russian).
 */
export function matchLocale(tag: string): Locale | undefined {
  const lower = tag.toLowerCase();
  if (isLocale(lower)) return lower;
  const primary = lower.split("-")[0];
  return isLocale(primary) ? primary : undefined;
}

export type LocaleSources = {
  /** The language the member chose, null when they follow the browser. */
  member?: string | null;
  /** The language remembered in this browser. */
  stored?: string | null;
  /** The browser's languages, most preferred first. */
  browser?: readonly string[];
  /** The instance's default, as the server writes it into the page. */
  instanceDefault?: string | null;
};

/**
 * The language the console renders in: the member's choice, then the one
 * this browser remembers, then the first of the browser's languages the
 * console offers, then the instance's default, then English.
 */
export function resolveLocale(sources: LocaleSources): Locale {
  for (const candidate of [sources.member, sources.stored]) {
    if (candidate) {
      const match = matchLocale(candidate);
      if (match) return match;
    }
  }
  for (const tag of sources.browser ?? []) {
    const match = matchLocale(tag);
    if (match) return match;
  }
  const fallback = sources.instanceDefault ? matchLocale(sources.instanceDefault) : undefined;
  return fallback ?? sourceLocale;
}

/** The instance's default language, which the server writes into the page. */
export function instanceDefaultLocale(doc: Document = document): string | null {
  return (
    doc.querySelector<HTMLMetaElement>('meta[name="flagnetic-default-locale"]')?.content ?? null
  );
}

/** The language remembered in this browser, null when none or storage is denied. */
export function storedLocale(): string | null {
  return readStored(localeStorageKey);
}
