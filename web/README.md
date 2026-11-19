# The console

The console is a React single-page app in `apps/console`, built into the Flagnetic binary. This page is for whoever adds or changes a screen: how a screen is laid out, which primitive holds what, and the copy rules. Read it before writing a route; ESLint refuses the mistakes it names.

## The three zones

Every signed-in screen has the same three zones, left to right:

1. **The sidebar** names the screen: the project switcher, Overview, Flags, Segments, Experiments, Change requests, the activity screens, project settings, instance settings and the account menu. It shows what the member may open and nothing else. Navigation groups never appear in addresses.
2. **The context panel** (optional, 256 px) holds every choice that changes what the whole page shows. A screen with no such choice declares none and the page takes the full width. Under 1024 px the panel becomes a sheet opened from the header.
3. **The page** is the content, composed from the primitives below. It is the only zone a screen author writes.

## Where a second-level choice goes

| The choice                                                                          | Goes to                                                                                    |
| ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| Which screen of an area (settings, instance, personal)                              | Context panel, links (`PanelGroup`, `PanelLink`)                                           |
| Which object-wide dimension (a flag's or experiment's environment)                  | Context panel, rows with the state lamp (`PanelEnvironment`)                               |
| Which saved slice of a list (all, stale, archived; awaiting my decision, mine, all) | Context panel, a "Views" group                                                             |
| Narrowing a list by four or more facets                                             | Context panel, facets (`PanelFacet`, `PanelChecklist`, `PanelTextFacet`, `PanelDateFacet`) |
| Narrowing a list by up to three facets                                              | The list's `Toolbar`, inline, no labels above                                              |
| Search and sort                                                                     | Always the list's `Toolbar`                                                                |
| Two to four views of one card's content (a webhook's Settings and Deliveries)       | Tabs inside the page                                                                       |

Facets apply at once and write the same search parameter as before. A list's saved slices are _views_; the word never names an exposure.

## Primitives

All live in `apps/console/src/components/ui/`. A screen uses them and sets no width, centring, page padding or uppercase of its own.

**`Page`** owns the width and the padding. `measure="full"` fills the page up to 1600 px, for lists and objects; `measure="form"` keeps a 672 px reading measure, for forms and short notices. Both are left-aligned, so content starts at the same edge on every screen. Loading, error, not-found and forbidden states render through it too.

```tsx
<Page measure="full">
  <PageHeader title={t`Flags`} actions={<Button>…</Button>} />…
</Page>
```

**`PageHeader`**: title, optional description, actions, an optional "more" menu. At most one filled button; destructive actions are outlined or in the menu.

**`Card`, `CardHeader`, `CardContent`** are the only section surface. Do not draw your own bordered box. A list's card (`list`) keeps a minimum height so a short list does not shrink the page to a strip.

```tsx
<Card>
  <CardHeader title={t`Prerequisites`} description={t`Flags that must serve first.`} />
  <CardContent>…</CardContent>
</Card>
```

**`ContextPanel`** with `PanelGroup`, `PanelLink`, `PanelEnvironment`, `PanelFacet`, `PanelChecklist` and `PanelFacts` fills the second zone.

```tsx
<ContextPanel label={t`Flags`}>
  <PanelGroup label={t`Views`}>
    <PanelLink to="/projects/$projectKey/flags" params={{ projectKey }}>
      <Trans>All flags</Trans>
    </PanelLink>
  </PanelGroup>
</ContextPanel>
```

**`Toolbar`**: one row with search, sort, the button that brings the panel back while it is folded away, and a removable chip for each facet in force.

**`Table`**: the list's table, with tabular numbers.

**`Lamp`**: the state lamp, a filled dot when an environment serves the flag and a hollow one when it does not, always followed by the word. Use it where an environment's serving state is named and nowhere else; protection is a lock beside the name.

```tsx
<Lamp on={environment.on} />
```

**`SplitBar`**: one bar of segments in the series colours with a legend of name and share, for a rollout or an experiment's weights.

**`Badge`**: lifecycle words (Draft, Running, Archived) in the secondary role; a semantic colour only where the state asks for action.

**`Moment`** (`components/moment.tsx`): a date and time in the member's language. `relative` reads as how long ago up to seven days; the full time is always in `title`. `full` is the exact time in mono, for records such as the audit log.

**`FormPage`, `Field`, `SettingsSection`, `EmptyState`, `Details`** cover forms, settings blocks, an empty list and an inline "Details" disclosure.

A measure the kit does not offer is a reason to extend the kit, not to write a class in a route. `max-w-full` (a child not outgrowing its parent) is the one `max-w-` class allowed outside the kit.

## Tokens and type

Colours are tokens in `apps/console/src/styles.css`; use the token, never a hex value or a palette colour.

| Token                                             | Light                                      | Dark                                       | Use                                   |
| ------------------------------------------------- | ------------------------------------------ | ------------------------------------------ | ------------------------------------- |
| `--sidebar`                                       | `#f1f1f3`                                  | `#0f0f10`                                  | the window behind the inset page      |
| `--background`                                    | `#fafafa`                                  | `#141415`                                  | the inset page                        |
| `--card`, `--popover`                             | `#ffffff`                                  | `#19191b`                                  | sections, menus                       |
| `--raised` (`--muted`, `--secondary`, `--accent`) | `#f4f4f5`                                  | `#232325`                                  | table header, hovered row             |
| `--border`                                        | text at 9%                                 | text at 9%                                 | hairlines                             |
| `--input`                                         | text at 22%                                | text at 22%                                | field borders                         |
| `--foreground`                                    | `#171717`                                  | `#ededed`                                  | text                                  |
| `--foreground-secondary`                          | `#525252`                                  | `#a1a1a6`                                  | secondary text                        |
| `--muted-foreground`                              | `#6b6b6b`                                  | `#909096`                                  | meta text                             |
| `--primary`                                       | `#4f46e5`                                  | `#5b54ee`                                  | primary action, selection, focus ring |
| `--primary-ink`                                   | `#4338ca`                                  | `#a5a8ff`                                  | accent used as text                   |
| `--state-on`                                      | `#16a34a`                                  | `#4cc38a`                                  | serving state                         |
| `--state-risk`                                    | `#b45309`                                  | `#e2a336`                                  | a state that asks for attention       |
| `--destructive`                                   | `#cc2222`                                  | `#e85a5e`                                  | destructive actions                   |
| `--series-1` to `--series-4`                      | `#4f46e5`, `#d97706`, `#0d9488`, `#e11d48` | `#8b86f7`, `#f0b24b`, `#3dc4b3`, `#f0708a` | variations in bars and charts         |

State colours are always paired with a word or an icon. Both themes must pass axe.

Type is Geist and Geist Mono, bundled by the build (no request leaves the instance). Page title 24/28 at 600; card title 16/24 at 500; body and controls 14/20; labels 14 at 500; meta 12 to 13, muted; keys and identifiers 12.5 in mono. 600 is for page titles only. Radius is 8 for controls, 12 for cards and the page, 4 to 6 for tags and keycaps. Tables and counts use tabular numbers.

## Copy

- Every string a member reads is a Lingui message with its `ru-RU` translation, in the same change.
- A hint is one sentence of at most 120 characters. Longer content that matters goes under a `Details` disclosure; a hint that restates its label is deleted.
- A page description appears only where it adds to the title.
- Sentence case everywhere. No uppercase styling.
- An empty list shows its create action in the empty state only.
- Use the terms of `docs/glossary.md` verbatim: _context panel_, _state lamp_, _view_ (never for an exposure).

## Running and checking

```bash
task web:dev       # serve the console with live reload, proxying to a binary at FLAGNETIC_URL
task web:check     # typecheck, lint, format-check, unit tests and catalog check
task web:extract   # extract messages into the catalogs after changing a string
task web:e2e       # Playwright against the real binary and a throwaway PostgreSQL
```

While working, run one test file with `pnpm --dir web --filter @flagnetic/console exec vitest run <file>`. The accessibility sweep checks every routed screen in both themes for axe violations, sideways scroll at 768 px and 390 px, and overlapping controls.

## The lint rule

`web/eslint.config.js` fails a class-name string in a route or a screen component (anything outside `components/ui/`) that contains `max-w-` (except `max-w-full`), `mx-auto`, `p-6`, `px-6`, `pt-6` or `uppercase`, with or without a variant prefix. The message names the primitive to use. `apps/console/src/test/screen-classes.test.ts` holds a failing and a passing screen, so loosening the rule fails a test.
