import js from "@eslint/js";
import pluginLingui from "eslint-plugin-lingui";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

// The class names a screen may not write, as regular expressions over a
// class-name string: a token starts the string or follows a space, and may
// carry variant prefixes (`md:`, `max-sm:`).
const classToken = (name) => `/(^|\\s)([a-z0-9[\\]-]+:)*(${name})(\\s|$)/`;
const screenClassRules = [
  {
    pattern: classToken("max-w-(?!full(\\s|$))[^\\s]+|mx-auto"),
    message:
      'A screen does not set its own width or centre itself: wrap it in <Page measure="full" | "form">, and put sections in <Card>.',
  },
  {
    pattern: classToken("p-6|px-6|pt-6"),
    message:
      "A screen does not set page padding: <Page> owns it (24 px, 16 px under 640 px). Sections get their padding from <Card>/<CardContent>.",
  },
  {
    pattern: classToken("uppercase"),
    message:
      "The console has no uppercase styling: write the label in sentence case, and use <PanelGroup label> or a card title for a group name.",
  },
];

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
    // A screen does not set its own width, centring, page padding or
    // casing: `Page` owns the measure and the padding, `Card` the section
    // surface, and the type scale has no uppercase. Each screen choosing its
    // own wrapper is what once left the content's left edge moving between
    // a list and the object it opens. `max-w-full` only stops a child from
    // outgrowing its parent, so it stays allowed. The kit (`components/ui/`)
    // is where these decisions live.
    files: ["apps/console/src/routes/**/*.{ts,tsx}", "apps/console/src/components/**/*.{ts,tsx}"],
    ignores: ["**/*.test.{ts,tsx}", "apps/console/src/components/ui/**"],
    rules: {
      "no-restricted-syntax": [
        "error",
        ...screenClassRules.flatMap(({ pattern, message }) => [
          { selector: `Literal[value=${pattern}]`, message },
          { selector: `TemplateElement[value.raw=${pattern}]`, message },
        ]),
      ],
    },
  },
  {
    // The signed-out pages are one centred card: tokens and type only.
    files: ["apps/console/src/components/signed-out-card.tsx"],
    rules: { "no-restricted-syntax": "off" },
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
