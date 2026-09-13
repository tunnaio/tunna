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

"key" in the Auth column means credentials are required; a request without
any answers `unauthenticated`. "key or presigned" means either form. Until the
authorization model is decided, every enabled key may do everything.

### 4.1 Buckets

A bucket record on the wire:

```json
{ "name": "photos", "public": false, "created_at": 1788912000 }
```

| Request | Body | Response |
|---------|------|----------|
| `PUT /-/buckets/{bucket}` | Optional JSON `{"public": bool}`; absent body or absent field means `false` | `201` with the bucket record |
| `GET /-/buckets/{bucket}` | none | `200` with the bucket record |
| `GET /-/buckets` | none | `200 {"buckets": [record, ...]}` ordered by name; `[]` when none |
| `DELETE /-/buckets/{bucket}` | none | `204`, empty body |

Faults, in ladder order: an invalid name is `invalid_bucket_name` (stage 4,
checked before the store is consulted); a body that is not JSON or has a
field of the wrong type is `invalid_parameter` with `details.name` naming
the field (stage 4); a taken name on create is `bucket_exists` and an
unknown name on get or delete is `bucket_not_found` (stage 6). Deleting a
bucket that still holds objects or active uploads is `bucket_not_empty`.

## 5. Objects

| Method and path | Purpose |
|-----------------|---------|
| `PUT /{bucket}/{key}` | Store a small object in one request. Size cap is server configuration. |
| `GET /{bucket}/{key}` | Fetch. Supports `Range`. |
| `HEAD /{bucket}/{key}` | Metadata only. |
| `DELETE /{bucket}/{key}` | Delete. |
| `GET /{bucket}` | List objects (section 7). |

### 5.1 Single-request PUT

The body is the object. Request headers:

| Header | Meaning |
|--------|---------|
| `Content-Type` | Stored and served back. Default `application/octet-stream`. |
| `Content-Length` | Required; a chunked body without it is `malformed_request`. Above the single-request cap is `body_too_large` before any byte is read. |
| `X-Tunna-Checksum` | Optional, section 8. |
| `X-Tunna-Meta-<name>` | User metadata. Names are case-insensitive and stored lowercase; values are stored as sent. Total size cap is server configuration. |

Response: `201` with the object record as JSON (section 5.3) and the same
`ETag` and `X-Tunna-Checksum` headers a GET would carry. An existing key is
replaced; readers mid-download of the old bytes finish on the old bytes
(ADR-0007). The bucket must exist: `bucket_not_found` at stage 6.

### 5.2 GET and HEAD

Response headers: `Content-Length`, `Content-Type`, `ETag`, `Last-Modified`
(RFC 7231 format, from `created_at`), `X-Tunna-Checksum`,
`Accept-Ranges: bytes`, and one `X-Tunna-Meta-<name>` per metadata entry.
HEAD carries the same headers and no body.

`Range` with a single `bytes=` range is honoured with `206` and
`Content-Range`; an unsatisfiable range is `416`. Multiple ranges are not
supported and are served as a full `200`. `If-None-Match` against the ETag
answers `304`.

A read on a bucket marked public needs no credentials. A read on any other
bucket requires them; without, `unauthenticated`.

### 5.3 Object record

Returned by PUT and by list entries:

```json
{
  "bucket": "photos",
  "key": "2026/one.jpg",
  "size": 65536,
  "content_type": "image/jpeg",
  "checksum": "crc32c=4waSgw==",
  "created_at": 1788912000,
  "metadata": { "title": "Summer" }
}
```

`metadata` is present only when non-empty.

### 5.4 DELETE

`204`, empty body. Unknown key is `object_not_found`. The metadata row is
removed before the bytes are, so a crash between the two leaves an orphan
file and never a dangling row.

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

`GET /{bucket}` lists the bucket's objects in bytewise key order. Requires
credentials unless the bucket is public, the same rule as an object read.

| Parameter | Default | Meaning |
|-----------|---------|---------|
| `prefix` | empty | Only keys that start with this string. |
| `after` | empty | Only keys that sort strictly after this string; pass the last key of the previous page. |
| `limit` | 1000 | Page size, 1 to the server's maximum (default 1000); above it is clamped, below 1 or not an integer is `invalid_parameter` with `details.name` set to `limit`. |

Response `200`:

```json
{ "objects": [ record, ... ], "next": "2026/two.bin" }
```

`objects` is the page as object records (section 5.3), `[]` when empty.
`next` is present only when the page holds `limit` records, and equals the
last key in it; a client passes it as `after` to continue. A final page of
exactly `limit` records therefore yields one more, empty page.

An unknown bucket is `bucket_not_found`.

**Delimiter grouping** (folding `a/b/c` and `a/b/d` into a common prefix
`a/b/`) is not specified in this version. A `delimiter` parameter is
`invalid_parameter` until it is, so that a client cannot depend on the
absence of grouping by accident.

## 8. Checksums [ADR-0006]

One algorithm: **CRC32C**, CRC-32 with the Castagnoli polynomial. It is
composable, so an object uploaded in parts gets its whole-object value from
the per-part values at complete, with no second read. The vector file
`vectors/crc32c.json` is the authority on the algorithm, the wire form, and
the combine rule.

**Wire form:** the four-byte big-endian value, standard base64 with padding,
prefixed with the algorithm name: `crc32c=4waSgw==` is the checksum of the
ASCII string `123456789`.

**Header:** `X-Tunna-Checksum`.

| Where | Meaning |
|-------|---------|
| Request with a body (object PUT, part PUT) | Optional. When present it must be in the wire form, may be listed among the signed headers, and the body is rejected with `checksum_mismatch` if its CRC32C differs. Absent means the server computes the value while writing. |
| Part PUT response | The part's own checksum. |
| Complete request body | Optional `checksums`: an array of the per-part wire values in part order. When present, each is checked against what the server recorded; a mismatch is `checksum_mismatch` with `details.part`. |
| Object GET and HEAD response | The object's checksum, always. |

**ETag** is the object's checksum as a quoted string, `ETag: "crc32c=..."`.
It is strong: the same bytes produce the same value regardless of how they
were uploaded, single request or parts of any size.

A malformed header value, meaning anything that is not `crc32c=` followed by
eight base64 characters ending in `==`, is `invalid_parameter` with
`details.name` set to the header name, at stage 4.

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
