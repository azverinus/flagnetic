# Agent guide

Instructions for AI coding agents (and a quick start for humans) working in this repository. Read `ARCHITECTURE.md` and `CONTRIBUTING.md` first.

## Project

Flagnetic: self-hosted, open-source feature flags and experiments. One Go binary with an embedded React console; PostgreSQL is the only dependency. Everything is free: there is no paid tier and no feature gate.

## Commands

```bash
task            # list tasks
task check      # what CI runs on every push: lint, tests, vulnerability scan
task lint       # golangci-lint in every module of go.work, including the layer rules
task test       # go test -race in every module (starts PostgreSQL through testcontainers unless DATABASE_URL is set)
task build      # bin/flagnetic
```

Requires Go (the toolchain in `go.mod` is fetched automatically), Docker for PostgreSQL in tests, [Task](https://taskfile.dev), and [jq](https://jqlang.org) for the check scripts.

## Rules

- **Layers.** `internal/domain` is plain Go: no `pgx`, no `net/http`, no I/O, no clock reads. `internal/app` holds one handler per use case and declares its ports as interfaces. Adapters live in `internal/infra`. `task lint` enforces the import rules; do not silence it.
- **Contracts first.** API changes start in `api/*.yaml`; regenerate (`task generate`) and commit the generated code. Once an SDK is released, the SDK protocol and ruleset format change only additively.
- **Evaluation changes start as a conformance fixture** in `conformance/fixtures`; every evaluator must pass it.
- **Tests go through the highest seam that shows the behaviour**: domain unit tests for rules, real PostgreSQL for repositories and handlers, the generated client for HTTP. Never fake a repository; never read tables in a test.
- **Exposure is an explicit call.** Evaluation never records one.
- **A client SDK key learns served values only**: values, variations and experiment blocks, never which rule, segment or reason finer than `targeting_match` produced them.
- **Comments state the reason**, in full, where the code is. Do not point at external documents, tickets or chat history.
- **Vocabulary**: use the terms in `docs/glossary.md` verbatim in identifiers, messages, docs and tests.
- **Commits and pull requests**: every change lands through a pull request, squash-merged, whose title is a Conventional Commit subject with a context or SDK scope (`feat(flags): ...`, `fix(sdk-js): ...`); every commit signed off (`git commit -s`). Split large work into a stack of small pull requests. English only. Never edit `CHANGELOG.md`; release-please generates it.

## Before you finish

Run `task check`. If you changed `api/`, run `task generate` and include the generated files. If you changed behaviour users see, update `docs/`.
