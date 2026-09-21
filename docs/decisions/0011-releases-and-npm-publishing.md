# ADR-0011: Release tags and npm publishing

**Status:** Accepted
**Date:** 2026-09-16
**Deciders:** maintainer

## Context

The TypeScript SDK covers the whole wire contract and is ready to be
installed by someone other than its author. Nothing has been released:
the repository has no tags, the npm name `tunna` is free (checked
2026-09-16), and the spec is `0.1.0-draft`, which its README defines as
"the contract is being written and nothing is stable".

This record decides how anything in this repository gets a version and
gets published, starting with the npm package, so the first release does
not set a precedent by accident.

Forces:

- **Only the maintainer runs releases** (project rule). A pushed tag may
  deploy, so tags are a human act.
- **One repository, several release lines.** The server, each SDK and the
  spec version independently (project notes). Tags must say which.
- **A draft spec.** Publishing `0.1.0` of a client for a contract that
  may still change would promise stability the spec disclaims.
- **Supply chain.** npm packages are a common attack surface. A publish
  should carry provenance, an attestation linking the artefact to the
  commit and workflow that built it, and there should be no long-lived
  publish token on a laptop or in a secret.
- **Small toolchain** (ADR-0010). No release framework for one package.

## Options considered

### Option A: Manual `npm publish` from the maintainer's machine

**Pros:** nothing to set up; 2FA on the account guards it.

**Cons:** no provenance; the build that ships is whatever was on the
machine, not the committed tree; a laptop is the weakest place to hold a
publish credential. Acceptable exactly once, to create the package on
the registry, which trusted publishing needs before it can be configured.

### Option B: CI publish on a tag with a long-lived npm token in a secret

**Pros:** builds from the committed tree; provenance possible.

**Cons:** the token is a standing credential that any workflow in the
repository, or anyone who can edit one, can exfiltrate. Rejected.

### Option C: CI stages on a tag with npm trusted publishing; the maintainer approves

The workflow authenticates to npm with a short-lived OpenID Connect token
GitHub mints for that run; npm is configured to trust that repository and
workflow file for the package. No token exists to leak. `npm stage
publish --provenance` builds and attests the version and parks it on the
registry unpublished; the maintainer approves it on npmjs.com with 2FA,
which is when it becomes installable. npm recommends exactly this and
marks direct publishing from CI as not recommended (seen on the
trusted-publisher form, 2026-09-16).

**Pros:** no standing credential; provenance for every publish; the
artefact is built from the tag by CI, the same steps every push already
runs; a human with 2FA is the last gate before anything is public, which
is the project's release rule made mechanical.

**Cons:** trusted publishing must be configured on npm after the package
exists, so one manual publish precedes it. A one-time step.

### Option D: A release framework (changesets, release-please, semantic-release)

**Pros:** changelogs, version bumps and tags automated.

**Cons:** a dependency and a convention for one package with one
maintainer, and it takes the tag out of the maintainer's hands, which the
project rule forbids. Rejected for now; revisit when a second SDK exists.

## Trade-off analysis

**Prereleases while the spec is draft.** The SDK publishes as
`0.1.0-alpha.N` under the npm dist-tag `alpha`. The first publish
showed a registry rule this record first got wrong: npm always has a
`latest` and sets it on the first publish whatever `--tag` says, and it
cannot be removed, so `npm install tunna` does resolve to the newest
alpha until `0.1.0` exists. The `alpha` tag is still the honest name to
install by, and the README says so; the version string itself carries
the warning.
Since `latest` cannot be removed and a stale `latest` is worse than a
current one, the maintainer moves it to the newest alpha by hand after
each approval, `npm dist-tag add tunna@<version> latest`, until `0.1.0`
takes it over for good; the workflow moves only `alpha`.
`0.1.0` is published when the spec drops `-draft`, and from then on the
SDK's version is its own and says nothing about the spec version, which
`SPEC_VERSION` carries.

**Tags name their release line.** `v1.2.3` is the server. An SDK is
`sdk/<language>/v1.2.3`, matching its directory; git allows the slashes.
The workflow triggers on its own line's pattern only, and refuses if the
version in `package.json` differs from the tag, so the tag is the single
statement of what was released.

**One manual publish, then never again.** Option A once, by the
maintainer, with 2FA, to create the package; then trusted publishing is
configured on npm for `.github/workflows/publish-typescript.yml`, and
Option C handles every publish after. The manual one still runs the same
`prepublishOnly` gate as CI.

**What goes in the tarball.** `dist/` only, plus the package README and a
copy of the MIT license in the package directory, since npm includes only
files under the package. No source, no tests, no spec.

## Decision

**Option C, bootstrapped by one Option A publish.**

### Versions and tags

| Line | Tag | Version rule |
|------|-----|--------------|
| Server | `v<semver>` | Its own; first release when the spec drops `-draft` |
| TypeScript SDK | `sdk/typescript/v<semver>` | `0.1.0-alpha.N` under dist-tag `alpha` while the spec is draft; `0.1.0` with the first stable spec; independent after |
| Spec | none; `spec/VERSION` | ADR in `spec/README.md` |

A release is: bump the version in `package.json` (`bun run bump` for the
next prerelease; it passes `--no-git-tag-version`, because the tag
`bun pm version` would otherwise create is `v<version>`, which is the
server's pattern and would start a server release), commit as
`sdk/typescript <version>`, tag `sdk/typescript/v<version>` on that
commit, push the tag, then approve the staged version on npmjs.com. The
maintainer does all five.

**`scripts/release.ts` (added 2026-09-21)** does the first four, for either
line: `bun scripts/release.ts sdk` or `bun scripts/release.ts server`. It
exists for its refusals, not for the typing it saves. Each is a mistake
that was made or nearly made by hand: it will not run unless the tree is
clean, on `main`, level with `origin/main`, and CI is green on the commit
(a red commit reached `main` on 2026-09-20 and was nearly tagged); it
builds the tag from the line's own prefix, so an SDK release cannot start
a server one; it refuses a tag that exists; and it pushes one tag by name,
never `--tags`. It computes the next prerelease from the newest tag on the
line, or takes an exact version. It asks for the version to be typed
before it creates anything, so it cannot run unattended, and `--dry-run`
prints every step and reports, rather than stops at, every precondition
that would have refused. It stays a tool the maintainer runs: approving
on npmjs.com and moving `latest` need a person and are printed at the end.
A Bun script rather than shell, so it is one file for Windows and Linux in
a language the SDK already uses; the cost is that a server release needs
Bun installed.

### The workflow

`.github/workflows/publish-typescript.yml`, on push of tags matching
`sdk/typescript/v*`:

1. Check out; set up Go, Bun and Node.
2. Verify the tag's version equals `package.json`'s; fail otherwise.
3. `bun install --frozen-lockfile`, `bun run check`, `bun test`,
   `bun run smoke` (which builds).
4. `npm stage publish --provenance --access public`, with `--tag alpha`
   when the version has a prerelease suffix, authenticated by OIDC
   (`id-token: write` permission, no secret). Needs a current npm; the
   workflow installs `npm@latest` since the one Node ships lags.
5. Create a GitHub release for the tag with generated notes, and point
   at the staged version in the run summary.
6. The maintainer approves the staged version on npmjs.com under the
   package's settings, with 2FA. Until then it is not installable.

### package.json

`publishConfig` with `access: public` (provenance is the workflow's flag, since a manual publish has no provider to attest);
`prepublishOnly` running check, test and build so a manual publish cannot
skip them; `files` stays `["dist"]`; a `LICENSE` copy in the package
directory.

### First publish

By the maintainer, from a clean checkout of the tagged commit, with
`npm login` and 2FA: `npm publish --tag alpha`. Then on npmjs.com, under
the package's settings, add a trusted publisher: repository
`tunnaio/tunna`, workflow `publish-typescript.yml`, direct publishing
left off. From the second version on, the tag stages and the approval
publishes.

## Consequences

Easier:

- A publish is a tag plus one approval; the artefact is built by CI from
  that commit, with provenance anyone can verify with `npm audit
  signatures`, and nothing becomes public without a person and 2FA.
- No publish credential exists anywhere after the first release.
- The same pattern serves the Rust SDK (crates.io supports trusted
  publishing too) and the server (a release workflow on `v*`).

Harder:

- Two tag namespaces to remember. The workflow's version check catches a
  tag on the wrong commit.
- Prerelease numbering is manual. Fine at this cadence.

To revisit:

- A changelog and a release framework (Option D) when a second SDK or a
  second maintainer makes manual bumps error-prone.
- Whether the server ships binaries through GitHub releases on `v*`,
  which needs its own workflow and a decision on which platforms.

## Action items

1. [x] Maintainer accepted this record, 2026-09-16.
2. [x] `package.json`: `publishConfig`, `prepublishOnly`, version
       `0.1.0-alpha.1`; `LICENSE` copied into the package directory, 2026-09-16.
3. [x] `.github/workflows/publish-typescript.yml`, 2026-09-16.
4. [x] First publish by the maintainer, 2026-09-16 (`tunna@0.1.0-alpha.1`);
       trusted publisher configured on npm the same day, staged-only.
5. [x] `sdk/typescript/README.md` install line; project notes, 2026-09-16.
