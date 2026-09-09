# tunna wire contract

**Spec version:** see [`VERSION`](VERSION). This document is a draft. All names,
shapes and defaults below were confirmed by the maintainer on 2026-09-09;
where one follows from an accepted decision record, the record is cited.

## 1. Conventions

- **Transport:** HTTP/1.1 and HTTP/2. TLS is expected in deployment; the
  server does not require it and must not assume it.
- **Encoding of text:** UTF-8 everywhere. JSON bodies are UTF-8 without a
  byte-order mark.
- **Timestamps on the wire:** integer Unix seconds, UTC. One
  integer parses the same in every language and canonicalizes trivially. JSON
  response fields that carry a time are also Unix seconds.
- **Identifiers:** opaque strings. Clients never parse them.
- **Percent-encoding:** RFC 3986 unreserved set (`A-Z a-z 0-9 - . _ ~`) is
  left as is; every other byte of the UTF-8 encoding becomes `%XX` with
  uppercase hex. Encode once. Path segments are encoded individually and
  joined with `/`. Query names and values are encoded individually and joined
  with `=` and `&`. [ADR-0003]
- **Canonical form is built from decoded values** on both sides, so a client
  that sends `%7E` and one that sends `~` produce the same canonical string.
  [ADR-0003]
- **Bucket names:** 3 to 63 characters, lowercase `a-z`, digits,
  and `-`; must begin and end with a letter or digit. Not a DNS requirement
  here, but it keeps names safe in every SDK, every shell and every URL.
- **Object keys:** 1 to 1024 bytes of UTF-8. Any Unicode scalar
  value except U+0000. May contain `/`, which has no meaning to the server
  except as the default listing delimiter. Keys are never paths on disk.
- **Reserved path prefix:** `/-/` is the control plane. A bucket
  name cannot begin with `-`, so `/-/...` never collides with `/{bucket}/...`.

## 2. Request processing order [ADR-0004]

Every request passes through six stages. The first stage that fails answers.

1. Syntax
2. Authentication
3. Authorization
4. Semantic validation
5. Body
6. State

Each error code in [`errors.json`](errors.json) belongs to exactly one stage.
A request with faults at several stages receives the earliest stage's error.

## 3. Authentication [ADR-0003]

### 3.1 API keys

An API key is a public **key id** and a **secret**. The server generates both
and returns the secret once. Key ids are safe to log; secrets and signatures
are not.

### 3.2 Canonical request

Lines joined with `\n`, no trailing newline:

```
TUNNA1
<METHOD>
<canonical path>
<canonical query>
<signed header names>
<signed header block>
<time>
```

| Line | Content |
|------|---------|
| `TUNNA1` | The scheme identifier. Bumped if the layout ever changes. |
| `<METHOD>` | Uppercase HTTP method. |
| `<canonical path>` | The request path, each segment percent-encoded per section 1, leading `/`. The path `/` is `/`. |
| `<canonical query>` | Parameters sorted by encoded name, then by encoded value; each as `name=value` (value may be empty, `=` always present); joined with `&`. The signature parameter itself is excluded. Empty string if no parameters remain. |
| `<signed header names>` | Lowercase header names the client chose to sign, sorted, joined with `;`. May be empty. `host` is never required and is discouraged. |
| `<signed header block>` | For each signed header in the same order: `name:value` with the value trimmed of leading and trailing whitespace and internal runs of whitespace collapsed to one space; entries joined with `\n`. Empty string if no headers are signed. |
| `<time>` | Header form: the request timestamp (Unix seconds). Presigned form: the expiry (Unix seconds). |

**Signature:** `hex(HMAC-SHA256(secret, canonical request))`, lowercase hex.

Not signed: the `Host` header, the body. Body integrity comes from a content
checksum header the client may include among the signed headers (section 8).

### 3.3 Header form

```
X-Tunna-Date: <unix seconds>
Authorization: TUNNA1-HMAC-SHA256 key=<key id>, headers=<h1;h2>, sig=<hex>
```

`X-Tunna-Date` is not listed among the signed headers; its value is the last
line of the canonical request, so it is covered by the signature already.
Listing it anyway is allowed and harmless.

The server accepts `X-Tunna-Date` within a skew window of the server's clock,
default 15 minutes either way, configurable. Outside the window the error is
`clock_skew` and the response includes the server's time.

### 3.4 Presigned form

Query parameters:

| Parameter | Value |
|-----------|-------|
| `x-tunna-key` | key id |
| `x-tunna-expires` | absolute expiry, Unix seconds |
| `x-tunna-headers` | signed header names, `;`-joined, may be empty |
| `x-tunna-sig` | the signature |

All four are part of the canonical query except `x-tunna-sig`, which is
appended as the last parameter of the URL after the canonical query. The maximum
lifetime (expiry minus now at signing) is server configuration, default 7
days. The key is looked up when the URL is used; a disabled or deleted key
fails every URL it signed. A request carrying both header-form and
presigned-form credentials is `malformed_request`.

### 3.5 Anonymous routes

Health and version, and reads on buckets marked public, need no credentials.
A request that presents credentials to an anonymous route is still verified
and fails if they are bad.

## 4. Control plane

All under `/-/`. A path that exists but not for the request's method answers
`method_not_allowed` with an `Allow` header; a path that matches nothing
answers `unknown_route`. Both use the JSON error body from section 9.

| Method and path | Purpose | Auth |
|-----------------|---------|------|
| `GET /-/health` | Liveness. `200 {"status":"ok"}` | anonymous |
| `GET /-/version` | Server version and spec version | anonymous |
| `GET /-/buckets` | List buckets visible to the key | key |
| `PUT /-/buckets/{bucket}` | Create a bucket | key |
| `GET /-/buckets/{bucket}` | Bucket metadata and settings | key |
| `DELETE /-/buckets/{bucket}` | Delete an empty bucket | key |
| `POST /-/uploads` | Initiate an upload session (section 6) | key |
| `GET /-/uploads/{id}` | Session state: part size, received parts | key or presigned |
| `PUT /-/uploads/{id}/parts/{n}` | Upload part `n` | key or presigned |
| `POST /-/uploads/{id}/complete` | Complete the session | key or presigned |
| `DELETE /-/uploads/{id}` | Abort the session | key or presigned |
| `/-/keys...` | Key management | decided with the authorization model |

## 5. Objects

| Method and path | Purpose |
|-----------------|---------|
| `PUT /{bucket}/{key}` | Store a small object in one request. Size cap is server configuration. |
| `GET /{bucket}/{key}` | Fetch. Supports `Range`. |
| `HEAD /{bucket}/{key}` | Metadata only. |
| `DELETE /{bucket}/{key}` | Delete. |
| `GET /{bucket}?list&prefix=&delimiter=&after=&limit=` | List keys. Ordered by key. `delimiter` groups common prefixes. |

Response headers on GET and HEAD: `Content-Length`, `Content-Type`, `ETag`,
`Last-Modified`, the checksum header (section 8), and any user metadata
headers (prefix `X-Tunna-Meta-`).

## 6. Uploads [ADR-0001]

Numbered parts of a fixed per-session size, written at offset into one file.
No assembly step.

### 6.1 Initiate

`POST /-/uploads` with a JSON body: bucket, key, `part_size` (bytes),
optional `content_type`, optional `size` (total, if known), optional user
metadata. Returns the session id, the part size, and the expiry.

Part size limits are server configuration, with defaults of
minimum 5 MiB and maximum 100 MiB. The maximum exists because proxies cap
bodies.

### 6.2 Part

`PUT /-/uploads/{id}/parts/{n}`, `n` from 1. The body is exactly `part_size`
bytes unless `n` is the final part, in which case it is between 1 and
`part_size` bytes. The server does not know which part is final until
complete, so the rule enforced on arrival is: length is `part_size`, or less
than `part_size`. A part shorter than `part_size` that is later found not to
be the last is rejected at complete with `part_size_mismatch`.

The server writes the body at offset `(n-1) * part_size`, syncs it, records
`n` as received, and returns the part's checksum. Re-sending a part replaces
it. Parts may arrive in any order and concurrently.

Clients should send `Expect: 100-continue` so that a rejection before stage
5 costs no upload bandwidth.

### 6.3 Query

`GET /-/uploads/{id}` returns the session: bucket, key, part size, expiry, and
the sorted list of received part numbers. Resume is: fetch this, send what is
missing.

### 6.4 Complete

`POST /-/uploads/{id}/complete` with a JSON body: `parts` (the total count),
and, if the checksum scheme requires it, the per-part checksums. The server
checks that parts 1 through `parts` are all received, that every part before
the last has length `part_size`, records the object, and drops the session,
in one transaction. Returns the object's metadata as HEAD would.

### 6.5 Abort and expiry

`DELETE /-/uploads/{id}` releases the file and the session. Sessions that
are neither completed nor aborted expire after a server-configured time,
default 24 hours, and are collected.

## 7. Listing

`GET /{bucket}?list` returns objects ordered by key, as a JSON array of
metadata records plus `common_prefixes` when a delimiter is given, plus
`next` when the page is full. Pagination is by `after=<last key seen>`.
`limit` defaults to 1000 and is capped by the server.

## 8. Checksums

Decided in the checksum ADR (follow-up to ADR-0001 and ADR-0003). Until then:
the header name, the algorithm, and whether complete requires per-part values
are all open. The signing vectors that include a checksum header use the
placeholder name `x-tunna-checksum` and will be regenerated when the header
is fixed.

## 9. Errors

Body shape:

```json
{ "error": { "code": "object_not_found", "message": "human readable", "details": {} } }
```

`code` is from [`errors.json`](errors.json). `message` is for humans and may
change without a spec bump. `details` carries structured fields named in the
error table entry, such as `server_time` on `clock_skew`.

Every 401 response carries `WWW-Authenticate: TUNNA1-HMAC-SHA256`, as HTTP
requires.

## 10. CORS

Rules carried from platform facts:

- A response that echoes an origin in `Access-Control-Allow-Origin` carries
  `Vary: Origin`.
- An allowed-origin pattern's wildcard stands for exactly one DNS label,
  anchored, never crossing a dot.
- CORS configuration is per bucket. Its shape is decided with the bucket
  settings.

## 11. Limits

| Limit | Default | Configurable |
|-------|---------|--------------|
| Single-request object size | 100 MiB | yes |
| Part size minimum | 5 MiB | yes |
| Part size maximum | 100 MiB | yes |
| Parts per session | 10 000 | yes |
| Clock skew window | 15 minutes | yes |
| Presign maximum lifetime | 7 days | yes |
| Upload session expiry | 24 hours | yes |
| List page maximum | 1000 | yes |
| Key length | 1024 bytes | no |
| User metadata total | 8 KiB | yes |

Every configurable value is logged at startup.

## Open items in this document

- Checksum scheme (section 8).
- Authorization model: what a key may do, per bucket; public buckets; key
  management endpoints.
- Bucket settings shape (CORS, public flag).
- Whether small-object PUT returns the same metadata shape as complete.
