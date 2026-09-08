# ADR-0003: Authentication and presigned URLs

**Status:** Accepted
**Date:** 2026-09-09
**Deciders:** maintainer

## Context

Every request to the server is either anonymous (a public bucket), made by an
SDK or tool holding an API key, or made by a browser or third party holding a
URL that carries its own authorization for one request (a presigned URL).
The second and third cases need one design, because a presigned URL is an
API-key request with the proof moved from headers into the query string.

Forces:

- **Several SDKs.** Whatever the scheme is, it will be implemented in Rust, C#,
  and more. Each rule is written many times. The signer is also the first
  thing every SDK builds, and the vector file is its oracle, so the scheme must
  be fully expressible as test vectors: inputs in, canonical string and
  signature out.
- **Fast.** Auth runs on every request, ahead of the hot path. Its cost must
  be a key lookup from memory and a hash, not a signature verification
  measured in tens of microseconds and not a network call.
- **Bodies are large and streamed.** Signing the body means hashing it before
  sending, which means buffering or a second pass over data that the client is
  trying to stream. Downloads have no request body at all.
- **Proxies rewrite things.** Cloudflare and reverse proxies in front of the
  server may alter the `Host` header, add headers, and normalize URLs. Anything
  the signature covers that a proxy can change breaks in deployment and
  produces the least debuggable class of error.
- **A presigned URL is a credential.** It must expire, it must be bound to one
  method and one object, it must never contain the secret, and the server
  must never log it.
- **Revocation must work.** Deleting or disabling a key must invalidate every
  presigned URL it produced, immediately.
- **Clocks.** Expiry and replay windows depend on the client's clock being
  roughly right. Wrong clocks are common; the error must say so.
- S3 SigV4 compatibility is not a goal. Its design is instructive; its
  double-encoding rules, region and service scopes, and key derivation chain
  exist for reasons this project does not have.

Open question 4 (validation before auth, or the reverse) is decided
separately. This record only requires that the request's method, path and
query are parsed before the signature can be checked, since they are inputs
to it.

## Options considered

### Option A: HMAC-SHA256 over a canonical request, one secret per key

Each API key is an id and a secret. The client builds a canonical string from
the request (method, canonical path, canonical query, a chosen set of headers,
and a timestamp or expiry), computes `HMAC-SHA256(secret, canonical string)`,
and sends the key id, the list of signed headers, and the signature. In header
form the timestamp is a request header and the server accepts it within a skew
window. In presigned form the same fields travel as query parameters with an
absolute expiry instead of a timestamp, and the signature is the last
parameter.

The server looks up the key by id, rebuilds the canonical string from what it
received, recomputes the HMAC, and compares in constant time.

| Dimension | Assessment |
|-----------|------------|
| Verification cost | One HMAC-SHA256 over a few hundred bytes: about a microsecond. Key lookup from an in-memory cache. |
| Secret at rest | Stored server-side in a form the server can use. Cannot be hashed like a password, because the server must recompute the HMAC. Protected by file permissions and, optionally, encryption with a server key. |
| Presign | The same scheme with fields in the query string. One mechanism for both. |
| Replay | Header form: bounded by the skew window. Presigned form: replay within expiry is the feature. |
| Body integrity | Not covered by the signature. Covered when the client sends a content checksum header and includes it in the signed headers; the server then verifies the checksum on the body. |
| SDK burden | A canonicalizer and an HMAC. A few hundred lines, fully specified by vectors. |
| Familiarity | The idea behind every cloud object store's auth. Well understood by anyone who has debugged one. |

**Pros**

- One mechanism covers headers and presign. Every SDK writes one signer.
- Cheapest possible verification.
- The secret never travels. Signatures expire. A leaked request log leaks
  nothing reusable after the window.
- Vector-testable end to end.

**Cons**

- Canonicalization is where all the bugs live. Every encoding decision is a
  place two implementations can disagree, and disagreement is a 401 with no
  further detail. The vector file has to be thorough, and the server's error
  response for a bad signature should carry enough to diagnose without
  leaking anything useful to an attacker.
- Secrets are stored in usable form on the server. A copy of the database is
  a copy of every key.
- Requires roughly synchronized clocks.

### Option B: Bearer secret for API calls, HMAC only for presign

API clients send `Authorization: Bearer <secret>`. No canonicalization, no
clock. The secret can be stored hashed. Presigned URLs still need something
that proves authorization without carrying the secret, so HMAC over a
canonical request exists anyway, for that path only.

| Dimension | Assessment |
|-----------|------------|
| Verification cost | A hash of the presented secret plus a lookup. Comparable to A. |
| Secret at rest | Could be hashed for bearer use, but presign needs the raw secret, so it is stored in usable form regardless. The hashing benefit evaporates. |
| Presign | HMAC canonical request, separately specified. |
| Replay | The secret itself is on every request. Under TLS this is fine; without TLS it is total compromise on first observation. |
| Body integrity | Same as A. |
| SDK burden | Two mechanisms: trivially simple bearer, plus the same canonicalizer as A for presign. Nothing saved. |
| Familiarity | Bearer is the most familiar scheme in existence. |

**Pros**

- API calls with `curl` need no tooling.
- No clock dependency for API calls.

**Cons**

- The canonicalizer must exist anyway for presign, so SDKs do not get simpler;
  they get a second thing.
- Two auth code paths on the server, two error surfaces, two sets of vectors.
- The secret is on the wire on every request and lands in any log that
  captures headers.
- Storing secrets hashed is not actually possible while presign exists.

### Option C: Asymmetric signatures (Ed25519), public key at rest

The client holds a private key; the server stores only the public key. The
client signs the canonical request; the server verifies. A database leak
yields no signing capability.

| Dimension | Assessment |
|-----------|------------|
| Verification cost | Order of fifty to a hundred microseconds per Ed25519 verify. Ten to a hundred times an HMAC, on every request. |
| Secret at rest | Only public keys. The strongest property of any option. |
| Presign | Works the same way; the signature is longer (64 bytes) but that is cosmetic. |
| Replay | Same as A. |
| Body integrity | Same as A. |
| SDK burden | Canonicalizer plus an Ed25519 library, available in every target language. Key generation moves to the client: the client generates and registers a public key rather than the server issuing a secret. |
| Familiarity | SSH-shaped. Unusual for object storage; every existing tool and mental model expects an id and a secret. |

**Pros**

- A copied database cannot sign requests.
- No shared secret ever exists.

**Cons**

- Pays a real per-request CPU cost on the hottest path. At high request rates
  it is a measurable fraction of a core spent on verification alone.
- Key registration is client-side ceremony. Every SDK needs key generation and
  a registration flow instead of "paste the id and secret".
- Solves a threat (database exfiltration without server compromise) that is
  narrow for a single-binary deployment where the database sits next to the
  process and its config.

### Option D: External identity (OIDC, JWT from an identity provider)

Rejected before scoring. It introduces a network dependency on every request
or a public-key verification cost, requires an identity provider to operate,
and still needs a presign mechanism of its own. Nothing in the project's goals
asks for it. It can be layered on later as a way to *mint* API keys, without
touching the wire scheme.

## Trade-off analysis

**The presign requirement decides most of it.** Any scheme needs an
HMAC-style canonical request for presigned URLs. Once that exists, using it
for header auth too costs one field's difference (timestamp versus expiry)
and yields one mechanism, one signer per SDK, one vector file. Option B's
simplicity is an illusion; it is Option A plus a second door.

**Asymmetric is the right answer to the wrong threat.** Option C's advantage
is that a copy of the database is not a copy of the keys. For a single process
whose database, data directory and configuration live together, an attacker
with the database very likely has the rest. The cost is paid on every request
forever. HMAC wins on the product goal; the residual risk is addressed by file
permissions and, if wanted later, encrypting secrets at rest with a server
key, which changes nothing on the wire.

**The body stays unsigned, and this is deliberate.** Signing the payload
forces the client to hash before sending, which for a 90 MB part means either
buffering it or reading it twice. Instead, body integrity comes from the
content checksum mechanism (its own ADR, following from ADR-0001). When the
client sends a checksum header and lists it among the signed headers, the
signature binds the checksum and the server verifies the body against it. A
client that sends no checksum gets transport-level integrity only, which under
TLS is what it gets from every other scheme too.

**Do not sign what a proxy can change.** The `Host` header is the classic
casualty: SigV4 signs it, and every reverse proxy that rewrites it produces
signature failures nobody can diagnose from the error. Here the key id already
binds the request to one deployment; signing `Host` adds nothing but breakage.
The signature covers method, canonical path, canonical query, and only the
headers the client explicitly lists. The server must reject a request whose
listed headers are absent, and must not care about headers not listed.

**One encoding, defined by the vectors.** Path segments and query values are
percent-encoded once, with the RFC 3986 unreserved set, and the canonical
form is built from decoded values re-encoded that way, on both sides. This
removes the double-encoding trap. The vector file, not prose, is the
authority on every edge: empty query values, repeated parameters, unicode
keys, a `+` in a key, a `/` in a query value.

**Revocation is a lookup at use time.** Presigned URLs are verified against
the key as it exists when the URL is used, never against a snapshot at signing
time. A deleted or disabled key fails every URL it ever produced. This also
means the server must never mint a "presign token" that stands on its own.

**Clocks are a first-class error.** A request outside the skew window or a
presigned URL past its expiry gets a distinct error code that names the
problem and includes the server's current time, so the client can tell "my
clock is wrong" from "I am not authorized". Skew window and maximum presign
lifetime are server configuration with stated defaults, logged at startup.

## Decision

**Option A: HMAC-SHA256 over a canonical request, one secret per key, the same
scheme in header form and in presigned query form.**

Fixed by this record:

1. **Key model.** An API key is a public id and a secret. The server generates
   both, shows the secret once at creation, and stores it in usable form. What
   a key may do (scope, permissions) is the authorization model and is a
   separate decision; this record covers proving who is asking.
2. **Signed inputs.** Method, canonical path, canonical query (excluding the
   signature parameter itself), the client's explicit list of signed headers
   with their values, and either a request timestamp (header form) or an
   absolute expiry (presigned form). `Host` is not signed. The body is not
   signed.
3. **Body integrity** comes from a content checksum header that the client
   may include among the signed headers. The checksum scheme is decided in the
   checksum ADR.
4. **Header form** carries a request timestamp; the server accepts it within
   a configurable skew window, default fifteen minutes either way.
5. **Presigned form** carries the key id, the signed-headers list, an
   absolute expiry, and the signature as query parameters. Maximum lifetime is
   server configuration, default seven days. Verification looks up the key at
   use time.
6. **Encoding.** Percent-encode once, RFC 3986 unreserved set, canonical form
   built from decoded values on both sides. Query parameters sorted by name
   then value. Header names lowercased, values trimmed. The vector file is the
   authority.
7. **Comparison** is constant-time. Failure responses distinguish clock
   problems from bad signatures from unknown keys, and include the server time
   on clock problems. Nothing else about why a signature failed is disclosed.
8. **Logging.** Presigned query strings are never logged. Key ids may be
   logged; secrets and signatures may not.

Not fixed by this record: exact header and parameter names, the algorithm
identifier string, error codes, the authorization model. Those belong to the
wire contract, the error table, and their own records.

## Consequences

Easier:

- Every SDK builds exactly one signer, tested against one vector file, and it
  is the first thing built in each language.
- Presign is not a feature on top of auth; it is auth with the fields moved.
- Verification is a cache lookup and an HMAC. Nothing measurable on the hot
  path.
- Behind a proxy, the only things that must survive unchanged are method,
  path and query, which every proxy preserves.

Harder:

- The vector file must be exhaustive on encoding edges. Every gap is a
  cross-language 401 waiting to happen. Writing the vectors is real work and
  must be done before the second SDK, not after.
- Secrets are stored in usable form. File permissions on the database are a
  security boundary and must be set and checked at startup.
- The server's error for a bad signature has to balance diagnosability against
  disclosure. The chosen line: name the category (clock, unknown key, bad
  signature, missing signed header), never echo the expected value.
- Clock skew is now a support question. The error message does the explaining.

To revisit:

- Encrypting secrets at rest with a server-held key, if a deployment story
  emerges where the database is copied more freely than the process
  environment. No wire change.
- Asymmetric keys as an additional key *type*, if a multi-tenant or delegated
  scenario ever needs "the server cannot sign as you". Not expected.

## Follow-ups this decision creates

- **Checksum ADR**: per-part and whole-object scheme (from ADR-0001), and the
  header the signature binds (from this record). These are one decision.
- **Authorization model**: what a key may do, per bucket or globally, and
  whether presigned URLs can be narrower than the signing key.
- **Signing vectors** in the spec: canonical string and signature for a
  thorough set of requests, including every encoding edge named above and the
  presigned form.
- **Error table entries** for the failure categories in point 7.

## Action items

1. [ ] Maintainer accepts, amends, or rejects this record.
2. [ ] Write the signing section of the wire contract: names, the algorithm
       identifier, canonical string layout, presigned parameter set.
3. [ ] Write the signing vector file and its schema. Include at least: a
       plain GET, a PUT with a signed checksum header, a presigned GET, a
       presigned PUT, an expired presign, a key with unicode and reserved
       characters, repeated and empty query parameters, a listed header that
       is absent.
4. [ ] Decide the checksum scheme (checksum ADR).
5. [ ] Decide the authorization model.
