import js from "@eslint/js";
import pluginLingui from "eslint-plugin-lingui";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: [
      "**/dist/**",
      "**/node_modules/**",
      "**/playwright-report/**",
      "**/test-results/**",
      // Generated from api/management.yaml, the route files and the catalogs.
      "packages/api/src/generated/**",
      "apps/console/src/routeTree.gen.ts",
      "apps/console/src/locales/**",
      "apps/console/public/theme-init.js",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2023,
      globals: { ...globals.browser, ...globals.node },
    },
    plugins: { "react-hooks": reactHooks, "react-refresh": reactRefresh },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "@typescript-eslint/consistent-type-imports": ["error", { prefer: "type-imports" }],
      // The console talks to the management API through the generated client only.
      "no-restricted-globals": [
        "error",
        { name: "fetch", message: "Use the generated client from @flagnetic/api." },
      ],
    },
  },
  {
    // Every string a member reads goes through Lingui, so a screen cannot
    // ship in English only. What is not prose (class names, keys, paths,
    // identifiers) is told apart by where it is written.
    files: ["apps/console/src/**/*.{ts,tsx}"],
    ignores: ["**/*.test.{ts,tsx}", "apps/console/src/test/**"],
    ...pluginLingui.configs["flat/recommended"],
    rules: {
      ...pluginLingui.configs["flat/recommended"].rules,
      "lingui/no-unlocalized-strings": [
        "error",
        {
          ignore: [
            // A single token without spaces that does not start with a
            // capital: a key, a path, a code, a CSS value.
            "^(?![A-Z])\\S+$",
            "^[A-Z0-9_-]+$",
            // The product's name is the same in every language.
            "^Flagnetic$",
            // An error class's name, and a CSS value written with var().
            "^[A-Z][A-Za-z]*Error$",
            "^var\\(",
          ],
          ignoreNames: [
            { regex: { pattern: "className", flags: "i" } },
            { regex: { pattern: "^[A-Z0-9_-]+$" } },
            "src",
            "href",
            "to",
            "type",
            "id",
            "role",
            "variant",
            "size",
            "side",
            "align",
            "position",
            "autoComplete",
            "displayName",
            "queryKey",
          ],
          ignoreFunctions: [
            "cva",
            "cn",
            "Error",
            "console.*",
            "*.querySelector",
            "*.getItem",
            "*.setItem",
            "*.removeItem",
            "*.addEventListener",
            "*.removeEventListener",
            "*.matchMedia",
            "*.startsWith",
            "*.endsWith",
            "*.includes",
            "createFileRoute",
            "redirect",
          ],
        },
      ],
    },
  },
  {
    files: ["**/*.mjs", "**/*.js"],
    languageOptions: { ecmaVersion: 2023, globals: { ...globals.node } },
  },
  {
    files: ["apps/console/src/routes/**/*.tsx"],
    rules: { "react-refresh/only-export-components": "off" },
  },
  {
    // The harness waits for the binary to answer /readyz before any client
    // exists, and the data layer is the one place that wraps fetch.
    files: ["e2e/support/*.ts", "apps/console/src/lib/http.ts"],
    rules: { "no-restricted-globals": "off" },
  },
);
