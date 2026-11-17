# Contributing to Flagnetic

Thanks for your interest. Flagnetic is a developer preview: much of what it will hold is still to be built, in large pieces. Issues, bug reports and discussion are welcome now; pull requests are welcome too, but for anything beyond a small fix please open an issue first so we can agree on the shape before you invest time.

Every change, the maintainers' included, reaches `main` through a pull request.

## Development setup

You need:

- Go (the version in `go.mod`; `go` downloads the pinned toolchain itself)
- Docker, for the PostgreSQL that tests start through testcontainers
- [Task](https://taskfile.dev)
- [jq](https://jqlang.org), which the check scripts use to read `go.work` and `go.mod`
- Node.js 22 or newer and [pnpm](https://pnpm.io) (the version in `web/package.json`), for the console in `web/` and the JavaScript SDK in `sdks/js/` (`task sdk-js:check`)

```bash
task pr:check             # the quick CI checks that apply to what your branch changes
task pr:check -- --full   # the same with the server's tests and coverage floors
task check                # every CI check on the whole repository: complete but slow
```

`task pr:check` is what to run before pushing. It looks at the files changed since the merge base with `origin/main` and runs the checks CI runs for them that answer in minutes: tidy `go.mod` and current generated code in every Go module, build and lint, the tests of the Go modules beside the server, `govulncheck`, and the steps of the console and SDK jobs whose paths changed (`task pr:check -- --plan` lists them without running anything). The server's tests are left to CI, which runs them in parallel jobs: the tests of the changed packages and of the packages depending on them (with the race detector), the guard tests (those that parse the server's Go source, and those that read a file under a directory you changed), and the coverage floors. `task pr:check -- --full` runs that selection on your machine, for reproducing a failure of CI. CodeQL, the Docker build, the other PostgreSQL versions, the browser suites and the oldest Go release run in CI only.

CI checks what a change can break and nothing else: `.github/scripts/ci-plan.sh` maps the changed paths to the jobs of `ci.yml`, and `task pr:check` reads the same rules. A change to the documentation or to one SDK runs in minutes; a pull request to the server runs the tests its change can reach on the oldest supported PostgreSQL. A push to `main` that changes the server or the console runs everything, on the newest PostgreSQL too, and so does the nightly run. A check that must run for a new path gets its rule in `ci-plan.sh`. When several checkouts share a machine, the heavy steps wait for each other under one lock (`PR_CHECK_P` and `PR_CHECK_PARALLEL` set how many packages and tests run at once; the header of `.github/scripts/pr-check.sh` lists the settings), and the tests share one PostgreSQL container, `flagnetic-pr-check-postgres`, which `docker rm -f` removes.

Set `DATABASE_URL` to an existing PostgreSQL to skip testcontainers.

`task build` builds the console, then the binary that embeds it. `go build` alone needs no Node: a binary built without the console serves a placeholder page in its place, and says so in its log. `task web:dev` serves the console with live reload on port 5173, sending API requests to a binary running on `FLAGNETIC_URL` (default `http://localhost:8080`).

`task web:e2e` runs the console end to end: Playwright builds the binary with the console embedded, starts Dex and Keycloak (the OpenID Connect and SAML identity providers the specs sign in through), Mailpit and a throwaway PostgreSQL in Docker, starts it against them, bootstraps an instance admin through the CLI, seeds through the generated client, and drives Chromium (`pnpm --dir web e2e:install` downloads it once). The binary runs under a test clock: a spec moves the server's time by writing a duration into the offset file (`advanceClock` in `web/e2e/support/instance.ts`), which `serve` accepts only with `FLAGNETIC_TEST_HARNESS=playwright` and listening on loopback.

### Strings in the console

Every string a member reads goes through [Lingui](https://lingui.dev), with the English text as its ID: `<Trans>` in JSX, `` t`...` `` from `useLingui()` for an attribute, `` msg`...` `` for a string defined outside a component. The lint rule `lingui/no-unlocalized-strings` fails on a string written any other way. After adding or changing one, run `task web:extract` and commit the catalogs in `web/apps/console/src/locales`; CI fails when extraction would change them, and when a language lacks a translation. Russian strings take their terms from [`web/apps/console/src/locales/ru/GLOSSARY.md`](web/apps/console/src/locales/ru/GLOSSARY.md): one rendering per term, and the rules for a string; a test fails on the words it retires. Server errors are translated by their `code`, with the values they depend on read from the problem document and its violations' `params`, in `web/apps/console/src/lib/problem-messages.ts`.

### Adding a permission

A permission arrives with the first operation that needs it:

1. Add it to the catalog in `internal/domain/access/permission.go`, named `entity.verb`, with the token scope it falls under and, when it changes who has access to a project, as members only. Write it into the built-in roles in the same file: the owner holds every permission (a test fails otherwise), and the others hold what their name promises.
2. Name it in the operation's `x-permission` in `api/management.yaml` and run `task generate`, which regenerates the operation table and fails on a name the catalog does not hold.
3. Add its rows to the tests that hold the catalog and the roles row by row (`internal/domain/access/catalog_test.go`) and to the permission matrix of the management API (`internal/infra/in/http/management/permissions_test.go`), which calls one operation of each permission as every role and expects the refusal to name it; then to the tables in `docs/access.md`.
4. In the console, add it to `Permission` in `web/apps/console/src/lib/access-names.ts` with the words that complete "the permission to …", so a disabled action can say what it needs, and translate them.

### Deprecating an operation

From 1.0 a surface is only ever added to; a thing to remove is deprecated first and removed only in a major release (see [Compatibility and support](docs/compatibility.md#deprecation-and-removal)).

1. Annotate the operation in its `api/*.yaml` document: `deprecated: true`, `x-deprecated-since` with today's date (`YYYY-MM-DD`), and `x-sunset` with the earliest date a major release may remove it when you want to announce one. A deprecated operation without `x-deprecated-since` fails `task generate` and its guard test.
2. Run `task generate`: it writes the operation into the `deprecations.gen.go` of its API, and the server then answers `Deprecation` and `Sunset` headers for it with no further code.
3. Mark the operation in its description with what replaces it, and in `docs/`. For a surface without an operation (a field, a command, a flag, a variable, a metric) mark it deprecated in its documentation instead.
4. Commit it as `feat` (or `fix` when a defect is the reason) so the release notes list it; never `refactor` or `chore`.
5. Remove it only in a major release, as a breaking change (`feat(api)!:`).

## How the code is organised

Read [ARCHITECTURE.md](ARCHITECTURE.md). In short: domain rules in `internal/domain` as plain Go, use cases in `internal/app`, adapters in `internal/infra`, dependencies pointing inwards and enforced by the linter.

The repository holds several Go modules: the server at the root, and the modules published for other programs to import (the evaluator and the Go SDKs), each with its own `go.mod` and version. The committed `go.work` ties them together, so a change across modules builds and tests in one checkout, and `task check` and `task pr:check` run their Go checks once per module. A new module joins with its `use` line in `go.work`: CI reads the list from there and checks the module on the oldest and the newest supported Go release.

The Python SDK in `sdks/python` is built and checked with [uv](https://docs.astral.sh/uv/) (it fetches the Python it needs): `uv sync`, then `uv run ruff check .`, `uv run ruff format --check .`, `uv run mypy` and `uv run pytest` in that directory, which CI runs on the oldest and the newest supported Python. Its regular expression engine takes case folding and Unicode classes from Go's tables; after a Go release that changes them, regenerate with `go run ./sdks/python/scripts/_unicode_tables > sdks/python/src/flagnetic/_eval/_re2/unicode_tables.py`, which CI compares.

### Migrations

From 1.0 a replica of 1.x runs correctly against the schema of 1.(x+1), because a rolling update migrates before the new replicas start while the old ones still serve. So every migration is expand/contract:

- It may add (a table, a nullable column or one with a default, an index built `CONCURRENTLY` under `-- +goose NO TRANSACTION`), and it never renames or drops anything the previous minor reads or writes, nor tightens a constraint its writes could violate (`NOT NULL` without a default, a new unique or check constraint without `NOT VALID`).
- To replace a column, one minor adds the new one and writes both; the next reads only the new one; the minor after drops the old one. A column or table the code stops using is dropped one minor after the code stops using it, never in the same release.
- [squawk](https://github.com/sbdchd/squawk) lints every migration file a pull request adds (`.github/scripts/lint-migrations.sh`, settings in `.squawk.toml`, for PostgreSQL 16); migrations already released are not linted. CI runs it, and `task pr:check` runs it when the migrations change and reports it as not run when squawk is missing (`.github/scripts/install-squawk.sh <dir>` installs the pinned, checksum-verified release).
- A deliberate exception is a squawk ignore comment on the line before the statement, with the reason on the line before that, for example `-- The column was never read by any release.` followed by `-- squawk-ignore ban-drop-column`. The lint fails an ignore that has no reason.

## Tests

Every change comes with tests at the seam that shows the behaviour:

- a domain rule: a unit test in its domain package;
- a repository or a use case: a test against a real PostgreSQL (never a fake repository);
- an API behaviour: an acceptance test through the generated client;
- an evaluation change: a fixture in `conformance/fixtures` first, then the code in every evaluator.

A test package that needs PostgreSQL calls `testkit.Main` from its `TestMain`: it gets one schema of its own with the migrations applied. After its tests, and before a test's own database is dropped, the test kit reads every audit entry written there and fails if one holds a secret value the tests created or received: a token value, a password or any other secret. The test kit's helpers and its API client record them; a test that obtains a secret another way records it with `testkit.RecordSecret`. CI runs the tests on the oldest and the newest supported PostgreSQL and checks a coverage floor per layer (`.testcoverage.yml`); `task pr:check` runs the same check locally for the packages you changed.

## Commits

- **Conventional Commits.** `type(scope): summary` in the imperative, under 72 characters. Types: `feat`, `fix`, `perf`, `deps`, `refactor`, `test`, `docs`, `build`, `ci`, `chore`, `revert`. Scopes are the contexts (`flags`, `experiments`, `delivery`, `events`, `governance`, `access`, `workspace`, `integrations`), the SDKs (`sdk-go`, `sdk-go-openfeature`, `sdk-js`, `sdk-dart`, `sdk-flutter`, `sdk-ruby`, `sdk-ruby-openfeature`, `sdk-python`, or `sdks` for a change to several of them), and `api`, `eval`, `conformance`, `console`, `platform`, `release`, `ci`, `docs`.
- **Sign-off (DCO).** Every commit carries a `Signed-off-by:` line matching its author: `git commit -s`. By signing off you certify the [Developer Certificate of Origin](https://developercertificate.org/): that you wrote the change or otherwise have the right to submit it under the project's license. CI rejects commits without it.
- Explain *why* in the body when the subject cannot. Keep commits self-contained; each one should build and pass tests.

## Pull requests

- Keep one concern per pull request. Large work is split into a **stack** of small pull requests, each based on the previous one; the maintainers use [git-spice](https://abhinav.github.io/git-spice/) (`gs branch create`, `gs stack submit`), but any tool or plain branches work.
- Pull requests are **squash-merged**: the title becomes the single commit on `main`, so it must be a Conventional Commit subject (CI checks it). Mark breaking changes with `!` (`feat(api)!: ...`) and explain them in the description.
- A fix that applies to several SDKs is one pull request with the scope `sdks`, not one per SDK: the checks of every SDK it touches run for it, and release-please lists it in the changelog of each.
- `task pr:check` passes locally; CI must be green. The check to watch is **`ci-ok`**: it needs every job of the `ci` workflow and fails when any of them failed or was cancelled, while a job that was skipped does not hold it. A new job in `ci.yml` goes into the `needs` of `ci-ok` too; `task lint` and CI fail otherwise. The pull request title, sign-off and CodeQL checks run in their own workflows beside it.
- Update `docs/` when users would notice the change. Do not edit `CHANGELOG.md`: it is generated.
- API changes start in `api/*.yaml`, with the regenerated code committed alongside.

## Releases

[release-please](https://github.com/googleapis/release-please) keeps one release pull request open per package, with its next version and changelog computed from the commits on `main` that touch it. Merging one tags the release and publishes it: the server image to `ghcr.io/azverinus/flagnetic`, each SDK to its registry. The release pull request of an SDK runs that SDK's checks; the server's runs every check of CI, as the last point before the image is published. Every release pull request edits `.release-please-manifest.json`, so after one is merged the others conflict until release-please rewrites them, which it does on that push to `main`. Until 1.0.0 a new feature or a breaking change bumps the minor version, a fix the patch version. A change to `conformance/` alone releases nothing: the release note goes with the evaluator or SDK change the fixture demands.

When a server release is cut, its migrations are pinned: before merging the server's release pull request, append every migration the release ships that `internal/infra/out/postgres/migrations/released.sum` does not list yet (`sha256sum NNNNN_*.sql >> released.sum` for each new file, in order, in a pull request of its own). Until then the test that a released migration never changes does not guard them. After the tag, `git diff <tag> -- internal/infra/out/postgres/migrations` shows only added files, and every file of the tag is listed.

### Go modules

The evaluator (`eval`), the Go SDK (`sdks/go`, with `flagnetictest` inside it) and the OpenFeature provider (`sdks/go-openfeature`) are separate Go modules with their own versions and changelogs. A Go module is published by its tag alone, so merging a release pull request is the release: the tags are `eval/vX.Y.Z`, `sdks/go/vX.Y.Z` and `sdks/go-openfeature/vX.Y.Z`. Use the scopes `eval`, `sdk-go`, `sdk-go-openfeature` and `conformance` for commits to them.

Each module requires the released version of what it imports, because that is what `go get` gives a consumer; the committed `go.work` only makes a change across modules build in one checkout. So an SDK change that needs a new evaluator lands with it, but releases in order:

1. The evaluator's release pull request is merged and `eval` is tagged.
2. Dependabot proposes the bump of the SDK's requirement to that tag; merging it is a commit to the SDK, which updates the SDK's release pull request.
3. The SDK's release pull request is merged; Dependabot then bumps the provider's requirement, and the provider releases the same way.

Before a module's first release its dependents require it at `v0.0.0`, which only the workspace resolves (`go.work` replaces it with the module's directory). CI's `released requirements` job builds each module outside the workspace against what it requires; it is skipped while a direct requirement is `v0.0.0`, and fails when that would hide a broken release: when the module is released while it still requires `v0.0.0`, or when a requirement's module is tagged but not yet bumped, so merge the bump promptly after a tag.

The first release of all three follows the order above, with one difference: Dependabot cannot resolve a `v0.0.0` requirement outside the workspace, so the first bump of each dependent is made by hand, in its directory, with `go get github.com/azverinus/flagnetic/eval@v0.1.0` (and the SDK's tag for the provider) and `.github/scripts/check-tidy.sh <dir> -w`. Every later bump is Dependabot's.

### The JavaScript SDK

The npm package `flagnetic` in `sdks/js` is its own release-please component, tagged `sdks/js/vX.Y.Z`; use the scope `sdk-js`. release-please keeps `package.json` and `VERSION` in `src/version.ts` at the release's version. When its release pull request is merged, the `npm (flagnetic)` job of `release.yml` checks out the tag, checks that the version is the tag's, builds and checks the package, keeps the tarball as the artifact `flagnetic-<version>` for 30 days and publishes that tarball to npm through [trusted publishing](https://docs.npmjs.com/trusted-publishers): npm accepts the workflow's OIDC token, so no npm token is stored, and attaches a provenance attestation. The job runs in the GitHub environment `npm`; give it the protection rules a publish should have.

npm lets a package trust a workflow only once the package exists, so the first version is published by hand:

1. Merge the first release pull request of `sdks/js`. While `flagnetic` is not on npm, the job stops at `npm publish --dry-run` and says so in its summary.
2. Download the artifact `flagnetic-<version>` of that run and check its contents (`tar tzf flagnetic-<version>.tgz`).
3. Publish it from an npm account with two-factor authentication: `npm publish flagnetic-<version>.tgz --access public --provenance=false`. A provenance attestation can only be made in CI, so this one version has none.
4. When the next release pull request of `sdks/js` is ready to merge, add a trusted publisher in the package's settings on npmjs.com: GitHub Actions, organization or user `azverinus`, repository `flagnetic`, workflow filename `release.yml`, environment `npm`. A new trusted publisher must publish within two days or it expires and has to be deleted and added again, so merge that pull request within those two days; its release is the first the workflow publishes.
5. Once it has, set the package's publishing access to require two-factor authentication and disallow tokens, so that only the workflow publishes.

### The Python SDK

The PyPI package `flagnetic` in `sdks/python` is its own release-please component, tagged `sdks/python/vX.Y.Z`; use the scope `sdk-python`. release-please keeps `__version__` in `src/flagnetic/_version.py` at the release's version, and `pyproject.toml` reads the package's version from it, so `uv.lock` holds no version a release would leave stale. When its release pull request is merged, the `python wheel and sdist` job of `release.yml` checks out the tag, checks that the version is the tag's, builds and checks the wheel and the sdist and keeps them as the artifact `flagnetic-python-<version>` for 30 days; the `PyPI (flagnetic)` job then publishes them through [trusted publishing](https://docs.pypi.org/trusted-publishers/): PyPI accepts the workflow's OIDC token, so no PyPI token is stored, and each file carries an attestation. The job runs in the GitHub environment `pypi`; give it the protection rules a publish should have.

PyPI accepts a publisher for a project that does not exist yet, so the first version is published by the workflow too. Before merging the first release pull request of `sdks/python`:

1. Create the GitHub environment `pypi` with its protection rules.
2. On pypi.org, from an account with two-factor authentication, add a pending publisher (Your account → Publishing): PyPI project name `flagnetic`, owner `azverinus`, repository `flagnetic`, workflow name `release.yml`, environment name `pypi`. A pending publisher does not reserve the name: whoever uploads `flagnetic` first owns it, so merge the release pull request soon after.
3. Merge the release pull request. The first upload creates the project and turns the pending publisher into its trusted publisher.
4. Once it has, check the project's publishers on pypi.org and that no API token is scoped to it.

### The Ruby SDK

The gems `flagnetic` in `sdks/ruby` and `flagnetic-openfeature` in `sdks/ruby-openfeature` are release-please components of their own, tagged `sdks/ruby/vX.Y.Z` and `sdks/ruby-openfeature/vX.Y.Z`; use the scopes `sdk-ruby` and `sdk-ruby-openfeature`. release-please keeps `VERSION` in `lib/flagnetic/version.rb` and `lib/flagnetic/openfeature/version.rb` at the release's version, and each gemspec reads its version from there. When a release pull request is merged, the `ruby gem` job of `release.yml` checks out the tag, checks that the version is the tag's, builds the gem with `gem build --strict`, compares the files it packs with the files the repository tracks and keeps it as the artifact `flagnetic-ruby-<version>` or `flagnetic-openfeature-ruby-<version>` for 30 days; the `RubyGems` job then pushes it through [trusted publishing](https://guides.rubygems.org/trusted-publishing/): RubyGems accepts the workflow's OIDC token, so no RubyGems key is stored. Both jobs run in the GitHub environment `rubygems`; give it the protection rules a publish should have.

The provider requires one minor of `flagnetic` (`~> 0.1.0`) and is tested against the released one as well as the working tree, so a change across both gems releases in order:

1. The release pull request of `sdks/ruby` is merged and `flagnetic` is published.
2. When that release is a new minor, Dependabot proposes the bump of the provider's requirement in `flagnetic-openfeature.gemspec`; merging it is a commit to the provider, which updates the provider's release pull request.
3. The release pull request of `sdks/ruby-openfeature` is merged. When both are merged at once, the workflow publishes `flagnetic` first and holds the provider back if that fails.

RubyGems accepts a publisher for a gem that does not exist yet, so the first versions are published by the workflow too. Before merging the first release pull request of each gem:

1. Create the GitHub environment `rubygems` with its protection rules.
2. On rubygems.org, from an account with multi-factor authentication, add a pending trusted publisher for each gem on the [pending trusted publishers page](https://rubygems.org/profile/oidc/pending_trusted_publishers): gem name `flagnetic`, then `flagnetic-openfeature`; repository owner `azverinus`, repository name `flagnetic`, workflow filename `release.yml`, environment `rubygems`. Until a gem's first push nobody owns its name, so merge the release pull requests soon after, `flagnetic` first.
3. Merge the release pull request. The first push creates the gem and turns the pending publisher into its trusted publisher.
4. Once it has, check the gem's trusted publishers on rubygems.org and that no API key with push rights is scoped to it; both gemspecs require multi-factor authentication for any push made by hand.

### The Dart and Flutter SDK

The pub.dev packages `flagnetic` in `sdks/dart` and `flagnetic_flutter` in `sdks/flutter` are release-please components of their own, tagged `sdks/dart/vX.Y.Z` and `sdks/flutter/vX.Y.Z`; use the scopes `sdk-dart` and `sdk-flutter`. release-please keeps `version` in `pubspec.yaml` and, for `flagnetic`, `sdkVersion` in `lib/src/client/options.dart` at the release's version. `flagnetic_flutter` requires the released `flagnetic` by version and resolves the working tree only through `pubspec_overrides.yaml`, which is never published, so release `flagnetic` first, let Dependabot raise the requirement, then release `flagnetic_flutter`.

pub.dev accepts a publish from GitHub Actions only for a run started by a tag push or by `workflow_dispatch` on the tag, and the tags release-please creates start no workflow. So when a release pull request is merged, the `pub.dev (dispatch)` job of `release.yml` dispatches `publish-dart.yml` on the tag. That workflow checks that the version is the tag's, runs `dart pub publish --dry-run` and pana at full score, and publishes through [automated publishing](https://dart.dev/tools/pub/automated-publishing): pub.dev accepts the job's OIDC token, so no pub.dev credential is stored. The publishing job runs in the GitHub environment `pub.dev`; give it the protection rules a publish should have. To publish a tag again, run `publish-dart.yml` by hand on that tag.

pub.dev lets a package be published automatically only once it exists, so the first version of each package is published by hand:

1. Merge the first release pull request of `sdks/dart`. While `flagnetic` is not on pub.dev, `publish-dart.yml` stops after its checks, says so in its summary and keeps the package's files as the artifact `flagnetic-<version>` for 30 days.
2. Download the artifact, unpack it (`tar xzf flagnetic-<version>.tar.gz`) and check its contents.
3. In the unpacked directory, from a Google account with two-factor authentication, run `dart pub publish`.
4. On pub.dev, in the package's Admin tab, enable automated publishing from GitHub Actions: repository `azverinus/flagnetic`, tag pattern `sdks/dart/v{{version}}`, publishing from `workflow_dispatch` events enabled, environment `pub.dev` required. Move the package to a verified publisher if you have one.
5. Once Dependabot has raised `flagnetic_flutter`'s requirement and `flagnetic_flutter`'s first release pull request is merged, do the same for `flagnetic_flutter` (artifact `flagnetic_flutter-<version>`, tag pattern `sdks/flutter/v{{version}}`), publishing with `flutter pub publish`.

## Licensing

Flagnetic is licensed under [Apache-2.0](LICENSE). Contributions are accepted under the same license (inbound = outbound), certified by the sign-off above. There is no CLA.

## Code of conduct

Participation is governed by our [Code of Conduct](CODE_OF_CONDUCT.md).
