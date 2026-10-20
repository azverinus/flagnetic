# Contributing to Flagnetic

Thanks for your interest. Flagnetic is in early development: the codebase is being assembled in large pieces. Issues, bug reports and discussion are welcome now; pull requests are welcome too, but for anything beyond a small fix please open an issue first so we can agree on the shape before you invest time.

Every change, the maintainers' included, reaches `main` through a pull request.

## Development setup

You need:

- Go (the version in `go.mod`; `go` downloads the pinned toolchain itself)
- Docker, for the PostgreSQL that tests start through testcontainers
- [Task](https://taskfile.dev)
- [jq](https://jqlang.org), which the check scripts use to read `go.work` and `go.mod`

```bash
task check   # lint, tests and vulnerability scan: what CI runs
```

Set `DATABASE_URL` to an existing PostgreSQL to skip testcontainers.

## How the code is organised

Read [ARCHITECTURE.md](ARCHITECTURE.md). In short: domain rules in `internal/domain` as plain Go, use cases in `internal/app`, adapters in `internal/infra`, dependencies pointing inwards and enforced by the linter.

The repository holds several Go modules: the server at the root, and the modules published for other programs to import (the evaluator and the Go SDKs), each with its own `go.mod` and version. The committed `go.work` ties them together, so a change across modules builds and tests in one checkout, and `task check` runs every check once per module. A new module joins with its `use` line in `go.work`: CI reads the list from there and checks the module on the oldest and the newest supported Go release.

## Tests

Every change comes with tests at the seam that shows the behaviour:

- a domain rule: a unit test in its domain package;
- a repository or a use case: a test against a real PostgreSQL (never a fake repository);
- an API behaviour: an acceptance test through the generated client;
- an evaluation change: a fixture in `conformance/fixtures` first, then the code in every evaluator.

A test package that needs PostgreSQL calls `testkit.Main` from its `TestMain`: it gets one schema of its own with the migrations applied. CI runs the tests on the oldest and the newest supported PostgreSQL and checks a coverage floor per layer (`.testcoverage.yml`); `task check` runs the same check locally.

## Commits

- **Conventional Commits.** `type(scope): summary` in the imperative, under 72 characters. Types: `feat`, `fix`, `perf`, `deps`, `refactor`, `test`, `docs`, `build`, `ci`, `chore`, `revert`. Scopes are the contexts (`flags`, `segments`, `experiments`, `delivery`, `events`, `governance`, `access`, `workspace`, `integrations`), the SDKs (`sdk-go`, `sdk-go-openfeature`, `sdk-js`, `sdk-dart`, `sdk-ruby`, `sdk-python`), and `api`, `eval`, `conformance`, `console`, `platform`, `release`, `ci`, `docs`.
- **Sign-off (DCO).** Every commit carries a `Signed-off-by:` line matching its author: `git commit -s`. By signing off you certify the [Developer Certificate of Origin](https://developercertificate.org/): that you wrote the change or otherwise have the right to submit it under the project's license. CI rejects commits without it.
- Explain *why* in the body when the subject cannot. Keep commits self-contained; each one should build and pass tests.

## Pull requests

- Keep one concern per pull request. Large work is split into a **stack** of small pull requests, each based on the previous one; the maintainers use [git-spice](https://abhinav.github.io/git-spice/) (`gs branch create`, `gs stack submit`), but any tool or plain branches work.
- Pull requests are **squash-merged**: the title becomes the single commit on `main`, so it must be a Conventional Commit subject (CI checks it). Mark breaking changes with `!` (`feat(api)!: ...`) and explain them in the description.
- `task check` passes locally; CI must be green. The check to watch is **`ci-ok`**: it needs every job of the `ci` workflow and fails when any of them failed or was cancelled, while a job that was skipped does not hold it. A new job in `ci.yml` goes into the `needs` of `ci-ok` too; `task lint` and CI fail otherwise. The pull request title, sign-off and CodeQL checks run in their own workflows beside it.
- Update `docs/` when users would notice the change. Do not edit `CHANGELOG.md`: it is generated.
- API changes start in `api/*.yaml`, with the regenerated code committed alongside.

## Releases

[release-please](https://github.com/googleapis/release-please) keeps one release pull request open per package, with its next version and changelog computed from the commits on `main` that touch it. Merging one tags the release and publishes it: the server image to `ghcr.io/azverinus/flagnetic`, each SDK to its registry. Until 1.0.0 a new feature or a breaking change bumps the minor version, a fix the patch version. A change to `conformance/` alone releases nothing: the release note goes with the evaluator or SDK change the fixture demands.

### Go modules

The evaluator (`eval`), the Go SDK (`sdks/go`, with `flagnetictest` inside it) and the OpenFeature provider (`sdks/go/openfeature`) are separate Go modules with their own versions and changelogs. A Go module is published by its tag alone, so merging a release pull request is the release: the tags are `eval/vX.Y.Z`, `sdks/go/vX.Y.Z` and `sdks/go/openfeature/vX.Y.Z`. Use the scopes `eval`, `sdk-go`, `sdk-go-openfeature` and `conformance` for commits to them.

Each module requires the released version of what it imports, because that is what `go get` gives a consumer; the committed `go.work` only makes a change across modules build in one checkout. So an SDK change that needs a new evaluator lands with it, but releases in order:

1. The evaluator's release pull request is merged and `eval` is tagged.
2. Dependabot proposes the bump of the SDK's requirement to that tag; merging it is a commit to the SDK, which updates the SDK's release pull request.
3. The SDK's release pull request is merged; Dependabot then bumps the provider's requirement, and the provider releases the same way.

Before a module's first release its dependents require it at `v0.0.0`, which only the workspace resolves (`go.work` replaces it with the module's directory). CI's `released requirements` job builds each module outside the workspace against what it requires; it is skipped while a direct requirement is `v0.0.0`, and fails when that would hide a broken release: when the module is released while it still requires `v0.0.0`, or when a requirement's module is tagged but not yet bumped, so merge the bump promptly after a tag.

The first release of all three follows the order above, with one difference: Dependabot cannot resolve a `v0.0.0` requirement outside the workspace, so the first bump of each dependent is made by hand, in its directory, with `go get github.com/azverinus/flagnetic/eval@v0.1.0` (and the SDK's tag for the provider) and `.github/scripts/check-tidy.sh <dir> -w`. Every later bump is Dependabot's.

## Licensing

Flagnetic is licensed under [Apache-2.0](LICENSE). Contributions are accepted under the same license (inbound = outbound), certified by the sign-off above. There is no CLA.

## Code of conduct

Participation is governed by our [Code of Conduct](CODE_OF_CONDUCT.md).
