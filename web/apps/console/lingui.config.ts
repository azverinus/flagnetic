import { defineConfig } from "@lingui/cli";
import { formatter } from "@lingui/format-po";

/**
 * The console's catalogs. Messages are identified by their English source
 * text, so a string is translated where it is written and English needs no
 * catalog entry of its own. `pseudo` wraps and stretches every message; a
 * unit test renders screens in it to prove that every visible string goes
 * through Lingui. It is never offered to a member.
 */
export default defineConfig({
  sourceLocale: "en",
  locales: ["en", "ru", "pseudo"],
  pseudoLocale: { locale: "pseudo", prepend: "⟦", append: "⟧" },
  fallbackLocales: { default: "en" },
  catalogs: [
    {
      path: "<rootDir>/src/locales/{locale}/messages",
      include: ["<rootDir>/src"],
      exclude: ["**/*.test.{ts,tsx}", "**/node_modules/**"],
    },
  ],
  // Line numbers would change the catalogs on every edit above a string;
  // the file names alone still say where a message is used.
  format: formatter({ lineNumbers: false }),
});
