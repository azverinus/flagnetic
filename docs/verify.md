---
title: "Verifying a release"
description: "Checking the image, the chart and each SDK package's attestation, downloading the SBOM, and what protects Go modules."
---

# Verifying a release

Every release is built by the release workflow of this repository, and most of what it publishes carries a statement, signed by that workflow's identity, of what it was built from. This page shows how to check each artifact before you run it. Repository: `azverinus/flagnetic`; workflow: `.github/workflows/release.yml`.

What each artifact carries:

| Artifact | Registry | Attestation | How to check |
| --- | --- | --- | --- |
| Server image | GHCR | build provenance (GitHub attestation), BuildKit SLSA provenance and an SBOM | [Image](#the-image) |
| Helm chart | GHCR (OCI) | build provenance (GitHub attestation) | [Chart](#the-chart) |
| `flagnetic` (JavaScript) | npm | npm provenance, from trusted publishing | [npm](#npm) |
| `flagnetic` (Python) | PyPI | PEP 740 attestations, from trusted publishing | [PyPI](#pypi) |
| `flagnetic`, `flagnetic-openfeature` (Ruby) | RubyGems | sigstore attestation pushed with the gem | [RubyGems](#rubygems) |
| `flagnetic`, `flagnetic_flutter` (Dart) | pub.dev | none the registry can hold; published by `.github/workflows/publish-dart.yml` through automated publishing | [pub.dev](#pubdev) |
| Go modules | the module proxy, from tags | none; the checksum database pins the content | [Go modules](#go-modules) |

There is no separate cosign signature: the attestations are signed through Sigstore with the workflow's OIDC identity, and `cosign` can check them too.

## The image

The image is `ghcr.io/azverinus/flagnetic`, for `linux/amd64` and `linux/arm64`. Resolve the tag you will run to a digest, then verify the digest:

```bash
docker buildx imagetools inspect ghcr.io/azverinus/flagnetic:<version> --format '{{.Manifest.Digest}}'
gh attestation verify oci://ghcr.io/azverinus/flagnetic@sha256:<digest> --repo azverinus/flagnetic
```

The command passes only if the digest was attested by a workflow run in `azverinus/flagnetic`. Add `--signer-workflow azverinus/flagnetic/.github/workflows/release.yml` to require the release workflow itself.

Running the image by digest (`image: ghcr.io/azverinus/flagnetic@sha256:<digest>`) makes what you verified exactly what runs; a tag can move.

With `cosign` (2.4 or later) instead of the GitHub CLI; the attestation is stored next to the image as a Sigstore bundle, which `--new-bundle-format` reads:

```bash
cosign verify-attestation \
  --new-bundle-format \
  --type https://slsa.dev/provenance/v1 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/azverinus/flagnetic/\.github/workflows/release\.yml@' \
  ghcr.io/azverinus/flagnetic@sha256:<digest>
```

### The SBOM

The build attaches an SBOM and BuildKit's provenance to each platform's image in the index. Download them for one platform with:

```bash
docker buildx imagetools inspect ghcr.io/azverinus/flagnetic:<version> --format '{{json (index .SBOM "linux/amd64").SPDX}}' > flagnetic.spdx.json
docker buildx imagetools inspect ghcr.io/azverinus/flagnetic:<version> --format '{{json (index .Provenance "linux/amd64").SLSA}}' > flagnetic.provenance.json
```

The SBOM is SPDX JSON; feed it to a scanner such as `grype sbom:./flagnetic.spdx.json`.

## The chart

The chart is published to `oci://ghcr.io/azverinus/charts/flagnetic` at the same version as the server, with a build attestation:

```bash
helm pull oci://ghcr.io/azverinus/charts/flagnetic --version <version>
gh attestation verify oci://ghcr.io/azverinus/charts/flagnetic:<version> --repo azverinus/flagnetic
```

The `cosign` form is the one above, with the chart's reference in place of the image's. A `helm pull` of a digest-pinned reference, `oci://ghcr.io/azverinus/charts/flagnetic@sha256:<digest>`, is immune to a moved tag.

## npm

The JavaScript SDK is published from the release workflow through npm's trusted publishing, so each version it publishes carries a provenance statement naming the repository, the workflow and the commit, and npm shows it on the package page. The first version is published by hand before npm can trust the workflow, so it has none. To check from a project that depends on it:

```bash
npm audit signatures
```

It verifies the registry signatures and the provenance attestations of every installed package that has them, `flagnetic` included.

## PyPI

The Python SDK is published through PyPI's trusted publishing with attestations (PEP 740), so each file has a provenance object tying it to this repository and workflow. PyPI shows it on the file's page. To fetch a file and its attestation from PyPI and check them, name the file:

```bash
pip install pypi-attestations
pypi-attestations verify pypi --repository https://github.com/azverinus/flagnetic pypi:flagnetic-<version>.tar.gz
```

## RubyGems

The gems `flagnetic` and `flagnetic-openfeature` are pushed by the release workflow through trusted publishing, each with a sigstore attestation that RubyGems stores. RubyGems shows the attestation on the version's page at rubygems.org, with the repository and workflow it names; check there that they are `azverinus/flagnetic` and `release.yml`. RubyGems checks the attestation against the file and the gem's trusted publisher before it accepts the push, so a gem it lists with an attestation is the file that workflow built.

## pub.dev

pub.dev has no place to store an attestation. `flagnetic` and `flagnetic_flutter` are published by `.github/workflows/publish-dart.yml` through automated publishing, with no stored credential: pub.dev accepts a publish of these packages only from a GitHub Actions run in this repository started on a release tag. The first version of each is published by hand, before pub.dev can trust the workflow. pub.dev also stores a hash of every archive and `dart pub get` checks it against `pubspec.lock`; commit the lock file.

## Go modules

The Go SDK, its OpenFeature provider and the evaluator are Go modules published by tag. A registry-side attestation does not exist for them. What protects them is the checksum database: the first time any machine fetches a module version, `sum.golang.org` records its hash in a public, append-only log, and every later `go get` or `go mod download` on any machine fails if the content differs. Keep it on (it is the default; do not set `GOSUMDB=off`, and do not list these modules in `GONOSUMDB` or `GOPRIVATE`) and commit `go.sum`, which pins the same hashes for your build. `go mod verify` checks that the modules in the local cache have not changed since they were downloaded.
