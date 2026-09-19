# Deploying

The server is one static binary with one data directory. The `Dockerfile`
at the repository root builds it from source into a distroless image that
runs as a non-root user, listens on 8000, and keeps everything under
`/data`.

```
docker build -t tunna --build-arg VERSION=$(git describe --always) .
docker run -p 8000:8000 -v tunna-data:/data \
  -e TUNNA_BOOTSTRAP_KEY=tk_admin:change-me tunna
```

## What the platform must provide

| Need | Why |
|------|-----|
| A persistent volume at `/data` | Objects and the SQLite database live there. A named volume inherits the image's ownership; a bind mount must be writable by uid 65532. Losing it loses everything. |
| Environment variables | `TUNNA_BOOTSTRAP_KEY` on first start; `TUNNA_CORS_ORIGINS` if browsers call the store directly. All variables: [`configuration.md`](configuration.md). |
| TLS termination in front | The server speaks plain HTTP. `Host` is not part of the signature (ADR-0003), so a reverse proxy that rewrites it is fine. |
| No request-body cap below the part size | Uploads send parts of up to `part_size` (default limits 5 MiB to 100 MiB) and single PUTs of up to 100 MiB. Cloudflare's free tier caps bodies at 100 MB, so keep parts under that when it is in front. |

## Configuration in a container

Everything is an environment variable; the image sets two defaults and
the platform's environment panel provides the rest.

| Variable | Image default | Set it when |
|----------|---------------|-------------|
| `TUNNA_ADDR` | none (binary default `:8000`) | The platform wants another port; change the port mapping to match. A platform that injects `PORT` needs nothing: it is honoured when `TUNNA_ADDR` is unset. |
| `TUNNA_DATA_DIR` | `/data` | The volume must be mounted elsewhere. |
| `TUNNA_BOOTSTRAP_KEY` | none | First start, to get an admin key in. Remove it once a managed admin key exists. |
| `TUNNA_CORS_ORIGINS` | none | A browser page calls the store directly. |

The first log line at startup shows the effective values, secrets
redacted; it is the way to confirm what the container received.

Health: `GET /-/health` answers `{"status":"ok"}` without credentials.
Logs go to stdout, one line per event; the effective configuration is the
first line, secrets redacted.

## Published images

Every server release (ADR-0012) pushes `ghcr.io/tunnaio/tunna:<version>`
for amd64 and arm64, with `alpha` pointing at the newest prerelease and
`latest` at the newest stable version. Pin the version in production:

```
docker run -p 8000:8000 -v tunna-data:/data \
  -e TUNNA_BOOTSTRAP_KEY=tk_admin:change-me ghcr.io/tunnaio/tunna:0.1.0-alpha.4
```

The image and each release binary carry a build provenance attestation:
`gh attestation verify oci://ghcr.io/tunnaio/tunna:<version> --owner tunnaio`
checks that it was built by this repository's release workflow from the
tagged commit.

## Dokploy, and similar

Create an application from the GitHub repository with the Dockerfile
build type, add a volume mounted at `/data`, set the environment
variables, and attach a domain with HTTPS. Traefik in front does not cap
body size by default. After the first start, create a managed admin key
through the API and remove `TUNNA_BOOTSTRAP_KEY` from the environment
([`configuration.md`](configuration.md), bootstrap section).

## Known limits at this stage

Pre-release; see the README. Two housekeeping jobs are not built yet:
expired upload sessions and orphaned blobs are not swept, so a session
that was never completed or aborted holds its file until then. Neither
affects correctness, only disk use.
