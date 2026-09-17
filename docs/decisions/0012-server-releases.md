# ADR-0012: Server releases

**Status:** Accepted
**Date:** 2026-09-17
**Deciders:** maintainer

## Context

The server implements the whole draft contract, passes 108 conformance
cases, has a container image that CI builds and starts on every push,
and is running on the maintainer's own host. ADR-0011 reserved the `v*`
tag line for it and deferred what a server release produces. The SDK is
already on npm as `0.1.0-alpha.1`; the server having no release at all
while its client does is the odd state, not the other way round.

Forces:

- **Only the maintainer runs releases.** A tag is the act; the workflow
  does the rest and makes nothing public that the tag did not name.
- **Draft spec.** A prerelease version says so in its name, the way the
  SDK's does. `v0.1.0` waits for the spec to drop `-draft`.
- **Two things people want from a server release**: a binary to run on a
  machine, and an image to run on a platform. Both should be the exact
  commit the tag names, built by CI, with provenance.
- **Small toolchain.** No release framework unless it removes more than it
  adds.
- **The image already exists** (`Dockerfile`, tested in CI). A release
  publishes it; it does not build a second one.

## Options considered

### Option A: GitHub release with binaries, image to GHCR, plain workflow

A workflow on `v*`: a matrix `go build` for a handful of platforms with
the version baked in, checksums, a GitHub release with the binaries
attached and generated notes; `docker buildx` of the root Dockerfile for
amd64 and arm64, pushed to `ghcr.io/tunnaio/tunna` with the repository's
own token, tagged with the version. Build provenance attestations for
both, through GitHub's artifact attestations, so anyone can verify what
built them.

**Pros:** no new tool, no secret (GHCR accepts `GITHUB_TOKEN`), the
Dockerfile CI tests is the one published, provenance for free.

**Cons:** a longer workflow file than a GoReleaser config would be;
platform list and checksum step written by hand.

### Option B: GoReleaser

**Pros:** the standard tool; binaries, archives, checksums, Docker images,
changelog and release notes from one config file; well-trodden.

**Cons:** its own config language and Docker integration, a second
Dockerfile-like template for images rather than the one in the repo, and
a dependency on the tool's release cadence. It earns its keep at more
targets and more artefact types than this project has. Rejected for now;
the plain workflow can be replaced by it if the list grows.

### Option C: Docker Hub as well as GHCR

**Pros:** discoverability; people search Docker Hub.

**Cons:** a second registry account and a token in a secret, which
ADR-0011 avoided for npm. Deferred: GHCR first; a Docker Hub mirror is a
later addition with its own credential decision, and the `tunna` name
there is worth claiming when that happens.

## Decision

**Option A.**

### Versions and tags

`v<semver>` on the commit to release. While the spec is draft:
`v0.1.0-alpha.N`, prerelease in GitHub's sense. `v0.1.0` with the first
stable spec. The tag is the version; the workflow refuses a tag whose
name is not a semver.

### Artefacts

| Artefact | Where | Naming |
|----------|-------|--------|
| Binaries: linux amd64 and arm64, darwin amd64 and arm64, windows amd64 | Attached to the GitHub release | `tunna_<version>_<os>_<arch>` (`.exe` on windows), plus `checksums.txt` (SHA-256) |
| Image: linux amd64 and arm64 | `ghcr.io/tunnaio/tunna` | `<version>` always; `alpha` moved to the newest prerelease; `latest` only for a stable version |
| Provenance | GitHub attestations | One per binary and one for the image, verifiable with `gh attestation verify` |

The version inside the binary and the image is the tag without the `v`,
so `GET /-/version` reports what was released.

### The workflow

`.github/workflows/release.yml`, on push of `v*` tags:

1. Check out; set up Go.
2. Run the test suite once more on the tagged commit.
3. Build the five binaries with `CGO_ENABLED=0`, `-trimpath`, `-s -w`,
   `-X main.version=<version>`; write `checksums.txt`.
4. Build and push the image for two platforms with `docker/buildx`,
   logging in to GHCR with `GITHUB_TOKEN`, `VERSION` passed as the build
   argument the Dockerfile already takes.
5. Attest the binaries and the image.
6. Create the GitHub release, prerelease when the version has a suffix,
   generated notes, binaries and checksums attached, and the image
   reference in the notes.

Permissions: `contents: write`, `packages: write`, `id-token: write`,
`attestations: write`. No secret.

### After the first release

The GHCR package must be made public once, in the package's settings on
GitHub, since new packages default to private; the first workflow run
creates it. Dokploy can then pull `ghcr.io/tunnaio/tunna:<version>`
instead of building from source, which gives deploy-by-tag.

## Consequences

Easier:

- A server release is a tag. Binaries and image are built from that
  commit by CI with provenance; a person downloading either can verify
  the chain.
- Deploying a specific version to a platform is one field.

Harder:

- Five binaries and a two-platform image take a few minutes per release;
  acceptable at this cadence.
- The Go module is tagged by the same act, so `pkg.go.dev` will index the
  root package and `sig`; the importer-facing documentation gap from the
  project notes becomes visible with the first tag and is worth closing
  in the same release.

To revisit:

- GoReleaser when the platform list or artefact types grow.
- Docker Hub mirror.
- Homebrew tap or similar packaging, if anyone asks.

## Action items

1. [x] Maintainer accepted this record, 2026-09-17.
2. [x] `.github/workflows/release.yml`, 2026-09-17.
3. [x] `doc.go` for the root package written for an importer, 2026-09-17.
4. [x] `docs/deploy.md`: pulling the published image, 2026-09-17.
5. [ ] First tag by the maintainer, `v0.1.0-alpha.1`; GHCR package made
       public.
