import type { Me } from "@flagnetic/api";
import type { I18n } from "@lingui/core";

import { activateLocale } from "@/i18n/i18n";
import {
  instanceDefaultLocale,
  isLocale,
  type Locale,
  localeStorageKey,
  resolveLocale,
  storedLocale,
} from "@/i18n/locale";

import { writeStored } from "./storage";

/**
 * The value a language picker gives the choice that follows the browser,
 * which names no language: the member records it as no locale (null).
 */
export const followBrowser = "browser";

/** The picker's value for a member's locale: the language, or following the browser. */
export function localeChoice(locale: string | null | undefined): string {
  return locale ?? followBrowser;
}

/** The member's locale a picker's value stands for: null follows the browser. */
export function chosenLocale(choice: string): Locale | null {
  return isLocale(choice) ? choice : null;
}

/**
 * The language the console renders in for the caller: the one the member
 * chose, else what the browser says. A member who follows the browser is
 * not held to what this browser remembers, which mirrors their choice; a
 * project token acts for no member, so for it what is remembered applies.
 */
export function memberLocale(me: Me): Locale {
  const member = me.member;
  return resolveLocale({
    member: member?.locale,
    stored: member ? null : storedLocale(),
    browser: navigator.languages,
    instanceDefault: instanceDefaultLocale(),
  });
}

/**
 * Mirrors the member's choice into this browser, so the sign-in page, which
 * no session reaches, shows it too; a member who follows the browser leaves
 * nothing remembered, so the sign-in page follows the browser as they do.
 * A project token acts for no member and changes nothing remembered. It
 * writes only when what is remembered differs.
 */
function rememberMemberLocale(me: Me): void {
  if (!me.member) return;
  const chosen = me.member.locale ?? null;
  if (storedLocale() !== chosen) writeStored(localeStorageKey, chosen);
}

/**
 * Renders the console in the caller's language from now on, remembering it
 * in this browser and loading it only when it changes.
 */
export async function followMemberLocale(i18n: I18n, me: Me): Promise<void> {
  rememberMemberLocale(me);
  const locale = memberLocale(me);
  if (i18n.locale !== locale) await activateLocale(locale);
}
