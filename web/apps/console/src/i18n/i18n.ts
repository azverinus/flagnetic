import { i18n, type Messages } from "@lingui/core";

import type { Locale } from "./locale";

/**
 * Loads a catalog. Each is its own chunk, so a member downloads only the
 * language they read; the Vite plugin compiles the catalog on import.
 */
const catalogs: Record<Locale | "pseudo", () => Promise<{ messages: Messages }>> = {
  en: () => import("../locales/en/messages.po"),
  ru: () => import("../locales/ru/messages.po"),
  pseudo: () => import("../locales/pseudo/messages.po"),
};

/**
 * Renders the console in locale from now on: loads its catalog, activates
 * it and sets the document's language, so screen readers and the browser's
 * own controls follow.
 */
export async function activateLocale(locale: Locale | "pseudo"): Promise<void> {
  const { messages } = await catalogs[locale]();
  i18n.loadAndActivate({ locale, messages });
  document.documentElement.lang = locale === "pseudo" ? "en" : locale;
}

export { i18n };
