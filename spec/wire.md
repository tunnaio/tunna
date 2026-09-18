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

One thing runs before stage 1: a CORS preflight (section 10.2), which is
answered from configuration and never reaches the ladder.

## 3. Authentication [ADR-0003]

### 3.1 API keys

An API key is a public **key id** and a **secret**. The server generates both
and returns the secret once. Key ids are safe to log; secrets and signatures
are not.

A disabled key answers exactly as a key that does not exist: `unknown_key`,
in both the header and the presigned form. The answer does not reveal whether
an id was ever issued.

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

### 3.6 Authorization [ADR-0008]

Stage 3 decides what an authenticated key may do. Two kinds of key:

- **admin**: everything, including every route under `/-/keys` and bucket
  create, patch and delete.
- **scoped**: a map from bucket name to `read` or `write`; `write`
  includes `read`; the entry `*` applies to every bucket. Scoped keys
  cannot manage keys or create and delete buckets.

| Requires | Routes |
|----------|--------|
| nothing | health, version, and reads and listing on a public bucket |
| `read` on the bucket | object GET and HEAD, object listing, bucket GET |
| `write` on the bucket | object PUT and DELETE; upload initiate, part, query, complete, abort (checked against the session's bucket) |
| any key | bucket list, filtered to the buckets the key can read; admin sees all |
| admin | bucket create, patch and delete; every `/-/keys` route |

A key without the level answers `forbidden` before the bucket or object is
looked up, so the response does not depend on whether the resource exists.
Upload routes are the exception: the session is loaded first to learn its
bucket, so an unknown session is `upload_not_found` for everyone and a
known one is `forbidden` for a key without `write` on its bucket. The
vector file `vectors/authorization.json` is the authority on the rule.

A presigned URL carries its signing key's scope, which the signature has
already narrowed to one method and one path.

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
| `/-/keys...` | Key management (section 4.2) | admin |

"key" in the Auth column means credentials are required; a request without
any answers `unauthenticated`. "key or presigned" means either form. What a
key may do is section 3.6.

### 4.1 Buckets

A bucket record on the wire:

```json
{ "name": "photos", "public": false, "created_at": 1788912000 }
```

| Request | Body | Response |
|---------|------|----------|
| `PUT /-/buckets/{bucket}` | Optional JSON `{"public": bool}`; absent body or absent field means `false` | `201` with the bucket record |
| `GET /-/buckets/{bucket}` | none | `200` with the bucket record |
| `PATCH /-/buckets/{bucket}` | JSON `{"public": bool}`; the field is required | `200` with the updated bucket record |
| `GET /-/buckets` | none | `200 {"buckets": [record, ...]}` ordered by name; `[]` when none |
| `DELETE /-/buckets/{bucket}` | none | `204`, empty body |

Faults, in ladder order: an invalid name is `invalid_bucket_name` (stage 4,
checked before the store is consulted); a body that is not JSON or has a
field of the wrong type is `invalid_parameter` with `details.name` naming
the field (stage 4), and so is a patch body without `public`; a taken name on
create is `bucket_exists` and an unknown name on get, patch or delete is
`bucket_not_found` (stage 6). Deleting a
bucket that still holds objects or active uploads is `bucket_not_empty`.

### 4.2 Keys [ADR-0008]

A key record on the wire:

```json
{ "id": "tk_1f2e3d4c5b6a7988", "name": "web-prod", "admin": false,
  "scopes": { "photos": "write", "public-site": "read" },
  "disabled": false, "created_at": 1788912000 }
```

`secret` appears in exactly two responses, create and rotate, and nowhere
else. `scopes` is omitted when `admin` is true.

| Request | Body | Response |
|---------|------|----------|
| `POST /-/keys` | `{"name", "admin", "scopes"}`; `name` required, 1 to 128 bytes; `admin` defaults false; `scopes` required unless admin, and forbidden with it | `201` with the record plus `secret` |
| `GET /-/keys` | none | `200 {"keys": [record, ...]}` ordered by id |
| `GET /-/keys/{id}` | none | `200` record |
| `PATCH /-/keys/{id}` | any of `{"name", "admin", "scopes", "disabled"}` | `200` updated record. Absent fields are left alone; the merged record must satisfy the rules above, so making a scoped key admin sends `"scopes": {}` in the same patch |
| `POST /-/keys/{id}/rotate` | none | `200` record plus a new `secret`; the old one stops verifying at once |
| `DELETE /-/keys/{id}` | none | `204` |

Every route requires an admin key; a scoped key answers `forbidden`. An
unknown id is `key_not_found`. A scope value other than `read` or `write`,
a scope key that is not a valid bucket name or `*`, or `admin: true`
together with `scopes` is `invalid_parameter` with `details.name`. The
server does not prevent an admin from disabling, rotating or deleting its
own key or the last admin; the bootstrap variable
(`docs/configuration.md`) is the recovery path.

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
No assembly step. Every route below requires credentials, header form or
presigned; the presigned form is how a browser uploads parts directly.

### 6.1 Initiate

`POST /-/uploads` with a JSON body:

```json
{ "bucket": "photos", "key": "2026/big.bin", "part_size": 5242880,
  "content_type": "application/octet-stream", "metadata": { "title": "x" } }
```

`bucket`, `key` and `part_size` are required. `content_type` defaults to
`application/octet-stream`. `metadata` is optional and has the same rules
as the `X-Tunna-Meta-` headers on a single-request PUT. Faults in ladder
order: a missing or mistyped field is `invalid_parameter` with
`details.name`; a bad key is `invalid_key`; a part size outside the
server's limits is `invalid_part_size` with `details.min` and `details.max`
(defaults 5 MiB and 100 MiB; the maximum exists because proxies cap
bodies); an unknown bucket is `bucket_not_found`.

Response `201`:

```json
{ "id": "up_...", "bucket": "photos", "key": "2026/big.bin", "part_size": 5242880,
  "content_type": "application/octet-stream", "created_at": 1788912000, "expires_at": 1788998400 }
```

The id is opaque. A session that is neither completed nor aborted expires
at `expires_at` (server configuration, default 24 hours after initiate).

### 6.2 Part

`PUT /-/uploads/{id}/parts/{n}`, `n` from 1 to the server's maximum
(default 10 000); outside that is `invalid_part_number` with `details.max`.
An unknown id is `upload_not_found`. `Content-Length` is required; absent is
`malformed_request` and zero is `invalid_parameter` naming `Content-Length`,
since a part is never empty. A body longer than `part_size` is
`body_too_large` before any byte is read. A body
shorter than `part_size` is accepted on arrival, since only complete knows
which part is last. `X-Tunna-Checksum` is optional and, when present, the
body is rejected with `checksum_mismatch` on a mismatch (section 8).

The server writes the body at offset `(n-1) * part_size`, syncs it, and
records the part's length and checksum. Re-sending a part replaces it. Parts
may arrive in any order and concurrently. Clients should send
`Expect: 100-continue` so a rejection before stage 5 costs no bandwidth.

Response `200`, with `X-Tunna-Checksum` carrying the same value:

```json
{ "part": 3, "size": 1024, "checksum": "crc32c=..." }
```

### 6.3 Query

`GET /-/uploads/{id}` returns the session, with the received part numbers
sorted ascending. Resume is: fetch this, send what is missing.

```json
{ "id": "up_...", "bucket": "photos", "key": "2026/big.bin", "part_size": 5242880,
  "content_type": "application/octet-stream", "created_at": 1788912000,
  "expires_at": 1788998400, "parts": [1, 3] }
```

### 6.4 Complete

`POST /-/uploads/{id}/complete` with a JSON body:

```json
{ "parts": 3, "checksums": ["crc32c=...", "crc32c=...", "crc32c=..."] }
```

`parts` is the total count, at least 1; `checksums` is optional and, when
present, must have exactly `parts` entries. The server checks, in order:
every part 1..`parts` was received, else `upload_incomplete` with
`details.missing` listing the absent numbers; every part before the last has
length `part_size`, else `part_size_mismatch` with `details.part`; every
supplied checksum equals the recorded one, else `checksum_mismatch` with
`details.part`. Then, in one transaction, it records the object with the
combined checksum (ADR-0006) and drops the session. An existing object at
the key is replaced, as with PUT.

Response `201` with the object record (section 5.3) and the `ETag` and
`X-Tunna-Checksum` headers, exactly as a single-request PUT answers.
After complete the session id is gone: query and part answer
`upload_not_found`.

### 6.5 Abort and expiry

`DELETE /-/uploads/{id}` releases the file and the session, `204`. An
unknown id is `upload_not_found`. Expired sessions answer
`session_not_active` on part, query and complete until a sweep removes them,
after which they are `upload_not_found`. A bucket with an active session
cannot be deleted: `bucket_not_empty`.

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

## 10. CORS [ADR-0009]

CORS is one server-wide allowlist of origins (`docs/configuration.md`,
`TUNNA_CORS_ORIGINS`). It is not a security boundary: every request
carries an explicit signature or presigned query, and a browser attaches
nothing on its own, so an origin list restricts nothing a non-browser
client could not already do. It exists to let the right pages through and
to keep caches honest. When the list is empty, no CORS header is ever
written. The bucket record has no CORS field; one is reserved for a later
per-bucket narrowing and its absence means the server-wide list.

### 10.1 Origins

An entry is `*`, an exact origin `scheme://host[:port]`, or a pattern
`scheme://*.domain[:port]` whose `*` stands for exactly one DNS label.
Scheme is `http` or `https`. Entries are normalised at startup: lowercase,
and a default port (`:80` for http, `:443` for https) dropped, because
that is how browsers send `Origin`. Anything else, a path, userinfo, a
query, a wildcard elsewhere than the leftmost label, is a startup error.

A request origin matches an exact entry byte for byte after the host is
compared case-insensitively, and matches a pattern when the scheme and port
agree, the host ends with `.` plus the pattern's domain, and exactly one
label precedes that suffix. `https://*.example.com` matches
`https://app.example.com` and not `https://example.com`,
`https://a.b.example.com` or `https://evil-example.com`. The literal
origin `null` matches only `*`. If the list holds `*`, every origin
matches and the answer is the literal `*`.
[`vectors/cors.json`](vectors/cors.json) is the definition.

### 10.2 Preflight (stage 0)

A request is a preflight when the method is `OPTIONS` and both `Origin`
and `Access-Control-Request-Method` are present. It is answered before
stage 1 of section 2, from configuration alone, so it needs no credentials,
consults no store, and does not depend on whether the path is routed.

| Origin | Response |
|--------|----------|
| matches | `204`, empty body, with the headers below |
| does not match, or CORS is off | `204`, empty body, no CORS headers, no `Vary` |

| Header | Value |
|--------|-------|
| `Access-Control-Allow-Origin` | the request's `Origin`, or `*` |
| `Access-Control-Allow-Methods` | `GET, HEAD, PUT, POST, PATCH, DELETE`, the union of every route, regardless of path |
| `Access-Control-Allow-Headers` | the request's `Access-Control-Request-Headers`, echoed verbatim; absent when the request had none |
| `Access-Control-Max-Age` | `86400` |
| `Vary` | `Origin, Access-Control-Request-Method, Access-Control-Request-Headers` |

The header list is echoed rather than `*` because `*` is defined not to
cover `Authorization`, which every signed request carries. An `OPTIONS`
that is not a preflight falls through to the router, which has no `OPTIONS`
route: `method_not_allowed` on a known path, `unknown_route` on an unknown
one, with the CORS response headers of 10.3 when the origin matches.

### 10.3 Responses

Every response to a request carrying an `Origin` that matches, on every
route and including error responses:

| Header | Value |
|--------|-------|
| `Access-Control-Allow-Origin` | the request's `Origin`, or `*` |
| `Access-Control-Expose-Headers` | `*`; valid because the server never uses credentialed mode, and it exposes `ETag`, `X-Tunna-Checksum`, `Content-Range` and every `X-Tunna-Meta-*` without a list to maintain |
| `Vary` | `Origin`; omitted when the value is `*`, which is the same for everyone |

A request with no `Origin`, an origin that does not match, or CORS off gets
the same response with none of these headers. A non-matching origin is not
an error: the request may be a valid non-browser call, and the browser
refuses on its own.

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

- Whether small-object PUT returns the same metadata shape as complete.
