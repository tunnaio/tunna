# ADR-0006: Checksums

**Status:** Accepted
**Date:** 2026-09-11
**Deciders:** maintainer

## Context

Three earlier decisions each left a checksum question open and pointed here:

- ADR-0001 chose fixed-size parts written at offset, in any order. A hash
  computed in arrival order is therefore not the object's hash, so a
  whole-object checksum is either composable across parts or costs a second
  read of the file.
- ADR-0003 chose not to sign request bodies, so body integrity comes from a
  content-checksum header the client may include among the signed headers.
  The header's name and format were left to this record.
- The wire contract (section 5) promises an `ETag` on every object, which
  clients use for conditional requests and change detection.

Forces:

- **Fast.** The checksum runs over every byte the server stores and serves.
  An algorithm that costs a noticeable fraction of disk bandwidth is on the
  hot path of the product goal. On a modern CPU, SHA-256 runs at roughly one
  to two gigabytes per second per core; CRC32C with hardware support runs at
  well over ten.
- **Composable, or not.** With parts arriving out of order, the server can
  finish a composable checksum at complete without reading anything. A
  non-composable one means reading the whole object back from disk at
  complete, or giving up the whole-object value for uploaded objects.
- **One scheme on the wire.** Every SDK implements the client side. Offering
  several algorithms multiplies the surface; offering one keeps the vector
  file small and the SDKs identical.
- **Integrity, not authentication.** The signed-header mechanism from
  ADR-0003 already binds the checksum to the caller. The checksum itself only
  has to detect corruption and truncation in transit and at rest. It does not
  have to resist an adversary who controls the bytes, because that adversary
  could sign any checksum they liked.
- **Clients may not know the checksum in advance.** A streaming upload from
  a source of unknown length cannot send a checksum header before the body.
  The scheme must work with and without a client-supplied value.

## Options considered

### Option A: CRC32C, composable, one algorithm

CRC-32 with the Castagnoli polynomial. Hardware-accelerated on every current
x86 and ARM CPU. Composable: given the CRC and length of two byte ranges, the
CRC of their concatenation is computed in constant time without the bytes.
Four bytes, base64 as `crc32c=<base64>` on the wire.

| Dimension | Assessment |
|-----------|------------|
| Throughput | Tens of gigabytes per second with hardware support; never the bottleneck. |
| Whole-object value for uploads | Free: combine the per-part CRCs at complete, in part order, in microseconds. |
| Collision resistance | None against an adversary; 2^-32 for random corruption, which is what it is for. Truncation and bit flips are detected. |
| SDK burden | A table-driven CRC is fifty lines, and every language has one in its standard or de facto library. Combine is another thirty lines, given in the vector file's terms. |
| Familiarity | The checksum S3 recommends for new integrations since 2022. |

**Pros**

- Whole-object checksum for every object, uploaded in one request or in
  parts, without a second pass. This is the property the product goal needs.
- Cheapest possible per-byte cost.
- One algorithm, one vector file, one header format.

**Cons**

- Not a cryptographic hash. A client wanting content-addressing or
  tamper-evidence needs to hash on its own side. That is a different feature
  and out of scope.
- Combine must be implemented in every SDK that verifies multipart uploads
  end to end. The vector file makes it testable.

### Option B: SHA-256, non-composable

The familiar cryptographic hash. Sixty-four hex characters.

| Dimension | Assessment |
|-----------|------------|
| Throughput | One to two gigabytes per second per core. Measurable next to an NVMe read; a real fraction of a core at line rate. |
| Whole-object value for uploads | Not free. Either read the object back at complete (doubles disk reads per upload), or keep an opportunistic running hash over the contiguous prefix and hash the tail (complex, and the tail is the whole object when parts arrive in reverse). |
| Collision resistance | Cryptographic. More than integrity needs. |
| SDK burden | Trivial; every language has it. |
| Familiarity | Universal. |

**Pros**

- Content-addressable identifiers for free.
- Nobody has to be told what it is.

**Cons**

- Pays cryptographic cost for a non-cryptographic job, on every byte.
- Forces the second read at complete or the opportunistic-hash complexity,
  both rejected by ADR-0001's analysis.

### Option C: Several algorithms, client's choice

Offer CRC32C, SHA-1, SHA-256 and let the header say which. S3's current
model.

Rejected before scoring. It multiplies every SDK's work by the number of
algorithms, makes the ETag's meaning depend on how the object was uploaded,
and buys flexibility nobody has asked for. If a cryptographic hash is ever
wanted, it is added as a second, optional header with its own vector file,
not as a mode of this one.

### Option D: Hash of per-part hashes

S3's classic multipart ETag: MD5 of the concatenated part MD5s, suffixed with
the part count. Composable in a sense, but the result depends on the part
size, so the same bytes uploaded with different part sizes have different
"checksums", and a single-request upload has a third. Rejected: a checksum
that varies with how the bytes arrived is not a checksum of the bytes.

## Trade-off analysis

**Composability decides it.** ADR-0001 chose any-order parts for throughput,
and that choice only pays off if complete stays cheap. Option A keeps
complete at a few microseconds of arithmetic; Option B makes it a full read
of the object. For the product goal, that is the whole comparison.

**The security argument for SHA-256 does not apply.** The checksum is
verified inside a request whose signature already binds the caller and the
header. An attacker who can replace the body can also compute a valid
CRC32C, or a valid SHA-256, for the replacement. Neither algorithm defends
against that; only the signature does, and it covers the header, not the
body, by ADR-0003's explicit choice. What the checksum defends against is
corruption, and CRC32C is built for corruption.

**ETag is the checksum.** With one algorithm producing one value per object
regardless of upload path, the ETag can simply be that value. Same bytes,
same ETag, however they arrived. Clients get change detection and
conditional requests from a value they can compute themselves.

**The header is optional on upload and always present on download.** A
client that knows the checksum sends it, signs it, and the server rejects
the body on mismatch at stage 5. A client that does not, sends nothing, and
the server computes the value while writing. Either way the stored object
has a checksum and every read reports it.

## Decision

**CRC32C as the one checksum, composable across parts, base64 on the wire,
and the object's ETag.**

Fixed by this record:

1. **Algorithm.** CRC-32 with the Castagnoli polynomial (reflected
   0x82F63B78), initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF. The check
   value for the ASCII string `123456789` is 0xE3069283. The vector file is
   the authority.
2. **Wire form.** The four-byte big-endian value, base64 standard encoding
   with padding, prefixed: `crc32c=<base64>`. The prefix exists so a second
   algorithm could be added later without a new header.
3. **Header.** `X-Tunna-Checksum`, on requests with a body and on every
   object response. On a request it is optional; when present it must be in
   the wire form, may be listed among the signed headers, and the body is
   rejected with `checksum_mismatch` if it does not match.
4. **Part uploads.** Each part response carries the part's own CRC32C in the
   same header. Complete may include the per-part values; when it does, the
   server checks them against what it recorded and rejects a mismatch with
   `checksum_mismatch` naming the part. The object's checksum is the combine
   of the recorded per-part values in part order.
5. **ETag.** The object's checksum, as a quoted string: `ETag: "crc32c=..."`.
   Strong, because same bytes yield the same value by construction.
6. **Storage.** The checksum is stored with the object's metadata and never
   recomputed on read. A read serves it from the row.

Not fixed by this record: whether reads verify the file against the stored
checksum (an integrity-scrub question for the disk adapter), and any
optional cryptographic hash (a separate feature with its own record).

## Consequences

Easier:

- Complete is metadata arithmetic. ADR-0001's throughput case stands.
- One header, one format, one vector file for every SDK.
- Every object has a checksum and an ETag, including ones uploaded without
  a client-supplied value.
- The Go standard library has CRC32C with hardware acceleration built in.

Harder:

- Combine is not in most standard libraries. Each SDK writes it, against the
  vector file. Roughly thirty lines of table arithmetic per language.
- CRC32C is not a content address. Anyone wanting one hashes on the client.
- The vectors must include combine cases whose expected values come from
  hashing the concatenated bytes directly, so no combine implementation is
  its own oracle. They do.

### Amendment 2026-09-22: the checksum may travel in the query

On a request with a body, the checksum may be given as the `checksum` query
parameter instead of the `X-Tunna-Checksum` header, with the same value and
the same checks. Both at once is `malformed_request`.

The reason is the browser. A cross-origin `PUT` goes out without a
preflight only if it carries no header outside the safelist, and
`X-Tunna-Checksum` is outside it. So every presigned part upload from a
page cost an `OPTIONS` round trip before its bytes moved, and because
each part has its own presigned URL the browser could cache none of them.
Measured 2026-09-22 on a 200 MB upload through the console: 35 to 199 ms
per part, in lockstep across the parts in flight, so the link idled
between waves. The PUT itself carried nothing else that needs a preflight
(no `Content-Type` on a part). With the checksum in the query the same PUT
is a simple request.

It costs nothing in binding: the presigned signature already covers the
canonical query, so `checksum` is signed exactly as `x-tunna-expires` is,
with no change to the signer or the canonical form. A conformance case
edits the parameter after signing and expects `bad_signature`. The
alternatives were a longer `Access-Control-Max-Age`, which cannot help
when every URL differs, and dropping the checksum from browser uploads,
which gives up integrity for speed. Named `checksum`, not
`x-tunna-checksum`, so it is visibly not one of the four credential
parameters of the presigned form.

The header stays the form for everything that is not a browser, and the
SDK sends it in header form and the query in provider mode.

## Follow-ups this decision creates

- **Wire contract section 8** becomes the definition above; the signing
  vector that used a placeholder header name is already correct since the
  name is `X-Tunna-Checksum`.
- **Error table:** `checksum_mismatch` gains `details.part` for the complete
  case; its `algorithm` detail is always `crc32c`.
- **Disk adapter:** compute the CRC while writing, with `hash/crc32` and the
  Castagnoli table, so the value costs no extra pass.
- **Read verification** is its own small decision when the disk adapter is
  written: verify on every read, on a sample, or never, measured.

## Action items

1. [ ] Maintainer accepts, amends, or rejects this record.
2. [x] `spec/vectors/crc32c.json`: six single cases and four combine cases,
       expected values from the standard library over the actual bytes.
3. [ ] Update wire.md section 8 and the `checksum_mismatch` row.
4. [ ] Combine implementation in `sig`, tested against the vectors, since it
       is pure arithmetic every SDK needs and the Go SDK will take `sig`.
